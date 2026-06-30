# End-to-end tests

## Explanation

The end-to-end tests are implemented using [YARF](https://github.com/canonical/yarf). They cover a wide range of scenarios,
both for authd and the brokers.

## Setting up the environment

Running the tests locally requires a bit of setting up. This is a step-by-step guide to get you started.

:memo: **Note:** This process is automated through the `e2e-tests/vm/provision.sh` script, which you can use instead of following the steps below manually.

### 1. Install the required dependencies

The tests have mainly two sets of dependencies: one required to configure and run the VM and one to build YARF from source.

- Virtualization dependencies:

    ```text
    bsdutils
    cloud-image-utils
    guestfish
    libvirt-clients-qemu
    libvirt-daemon-system
    qemu-kvm
    retry
    socat
    wget
    xvfb
    ```

    Those are all part of the archive and can be installed on Ubuntu with:

    ```bash
    sudo ./e2e-tests/vm/install-provision-deps.sh
    ```

- Test-run dependencies:

    ```text
    bsdutils
    ffmpeg
    gir1.2-webkit2-4.1
    libcairo2-dev
    libgirepository-2.0-dev
    libvirt-clients-qemu
    libvirt-daemon-system
    libxkbcommon-dev
    python3-cairo
    python3-gi
    python3-tk
    qemu-kvm
    socat
    systemd-journal-remote
    unclutter
    xtightvncviewer
    xvfb
    ```

    Those are all part of the archive and can be installed on Ubuntu with:

    ```bash
    sudo ./e2e-tests/install-deps.sh
    ```

### 2. Setup the VM

The tests need a VM to run. This can be easily setup by using the domain definition and cloud-init configuration provided in the repository.

1. Download the Ubuntu cloud image for the target release (and resize it):

    ```bash
    RELEASE=resolute  # Replace with your target release (e.g. noble, resolute)
    wget https://cloud-images.ubuntu.com/${RELEASE}/current/${RELEASE}-server-cloudimg-amd64.img

    qemu-img resize ${RELEASE}-server-cloudimg-amd64.img 10G
    ```

2. Create the cloud-init iso using the release-specific configuration from `e2e-tests/vm/cloud-init-template-${RELEASE}.yaml`:
   1. Update the file with the ssh key that will be used to access the VM.
   2. Create a directory and copy the YAML file there. The file must be named `user-data`.
   3. Create the `seed.iso` file using `cloud-localds`.

        ```bash

        mkdir -p /tmp/seed/

        SSH_PUBLIC_KEY=$(cat "${SSH_PUBLIC_KEY_FILE}") \
            envsubst < e2e-tests/vm/cloud-init-template-${RELEASE}.yaml > /tmp/seed/user-data

        cloud-localds /tmp/seed.iso /tmp/seed/user-data
        ```

3. Define the VM using the provided XML in `e2e-tests/vm/e2e-runner-template.xml`:
   1. Edit the XML and set the correct paths for the disk image.

        ```bash
        IMAGE_FILE=${IMAGE_FILE} \
        envsubst < e2e-tests/vm/e2e-runner-template.xml > /tmp/e2e-runner.xml
        ```

   2. Define the VM with `virsh`:

        ```bash
        virsh define /tmp/e2e-runner.xml
        ```

4. Attach the cloud-init iso to the VM:

    ```bash
    virsh attach-disk e2e-runner-${RELEASE} /tmp/seed.iso vdb --targetbus virtio --type disk --mode readonly --config
    ```

5. Start the domain and wait for it to finish the initial setup:

    ```bash
    virsh start e2e-runner-${RELEASE}
    ```

    - The cloud-init process will take a few moments to finish. You can check the progress by connecting to the VM's console:

        ```bash
        virsh console e2e-runner-${RELEASE}
        ```

    - The domain will automatically shutdown once the initial setup is complete, so you need to start it again.

        ```bash
        virsh start e2e-runner-${RELEASE}

        # It will take a while, so make sure to wait until it's up and running
        sleep 180s
        ```

    - Now that the initial setup is done, we need to create a snapshot of this fresh state, so we can revert to it later when installing authd.

        ```bash
        virsh snapshot-create-as --domain e2e-runner-${RELEASE} --name initial-setup --disk-only
        ```

### 3. Install authd and the brokers

Now that the VM is ready, we need to install authd and the brokers we want to test.

:memo: **Note:** You can find a script for the installation process at the [end of the section](#Script-to-automate-the-installation-and-configuration-of-authd-and-the-broker).

#### Install authd and the broker

- The SSH commands below connect to the VM as root via the `e2e-tests/vm/ssh.sh` helper, which looks up the VM's VSOCK CID dynamically:

    ```bash
    ./e2e-tests/vm/ssh.sh --release ${RELEASE} [command]
    ```

- For convenience, set up an alias for the duration of your session:

    ```bash
    alias ssh_vm="./e2e-tests/vm/ssh.sh --release ${RELEASE}"
    ```

- The tests use two snapshots per broker:
  - `${BROKER}-installed` — authd (edge) + broker (edge)
  - `${BROKER}-stable-installed` — authd (stable) + broker (stable)

  The following steps must be performed once for the edge combination and once for the stable combination.

1. Revert to the `initial-setup` snapshot:

    ```bash
    virsh snapshot-revert e2e-runner-${RELEASE} initial-setup
    ```

2. Install authd (use `ppa:ubuntu-enterprise-desktop/authd-edge` for edge, `ppa:ubuntu-enterprise-desktop/authd` for stable):

    ```bash
    ssh_vm "DEBIAN_FRONTEND=noninteractive add-apt-repository -y ppa:ubuntu-enterprise-desktop/authd-edge"
    ssh_vm "DEBIAN_FRONTEND=noninteractive apt-get install -y authd"
    ```

3. Install the desired broker (replace `${broker}` and `${channel}` with the desired values):

    ```bash
    ssh_vm "snap install ${broker} --channel=${channel}"
    ```

4. Configure authd to recognize the installed broker:

    ```bash
    ssh_vm "cp /snap/${broker}/current/conf/authd/${broker}.conf /etc/authd/brokers.d/"
    ```

5. Configure the installed broker (set `${ISSUER_ID}`, `${CLIENT_ID}`, `${CLIENT_SECRET}` to your identity provider's values — not all brokers use all three):

    ```bash
    ssh_vm "sed -i -e 's|<ISSUER_ID>|'${ISSUER_ID}'|g' \
                     -e 's|<CLIENT_ID>|'${CLIENT_ID}'|g' \
                     -e 's|<CLIENT_SECRET>|'${CLIENT_SECRET}'|g' \
                     /var/snap/${broker}/current/broker.conf"
    ```

6. Restart authd and the broker to apply the changes:

    ```bash
    ssh_vm "systemctl restart authd.service"

    # This could fail sometimes if the restart is triggered too quickly, so don't be afraid to wait a couple of seconds and retry
    ssh_vm "snap restart ${broker}"
    ```

7. Reboot the VM to ensure the snapshot is taken from the login screen:

    ```bash
    virsh reboot e2e-runner-${RELEASE}

    # Wait a while for the VM to reboot
    sleep 180s
    ```

8. Create a snapshot. Name it `${broker}-installed` for the edge combination and `${broker}-stable-installed` for the stable combination:

    ```bash
    # Edge:
    virsh snapshot-create-as --domain e2e-runner-${RELEASE} --name "${broker}-installed"

    # Stable:
    virsh snapshot-create-as --domain e2e-runner-${RELEASE} --name "${broker}-stable-installed"
    ```

#### Script to automate the installation and configuration of authd and the broker

Update the variables at the top of the script to match your desired broker and configuration.

```bash
#!/usr/bin/env bash

set -eux

RELEASE="resolute"   # Change to the desired Ubuntu release
broker="desired_broker_name" # Change to the desired broker name

# Identity provider credentials
ISSUER_ID=""
CLIENT_ID=""
CLIENT_SECRET=""

alias ssh_vm="./e2e-tests/vm/ssh.sh --release ${RELEASE}"

declare -a channels=("edge" "stable")
for channel in "${channels[@]}"; do
    echo "Setting up $broker broker - $channel"

    virsh snapshot-revert "e2e-runner-${RELEASE}" initial-setup

    # Install authd from the appropriate PPA
    PPA="ubuntu-enterprise-desktop/authd-edge"
    if [[ "$channel" == "stable" ]]; then
        PPA="ubuntu-enterprise-desktop/authd"
    fi
    ssh_vm "DEBIAN_FRONTEND=noninteractive add-apt-repository -y ppa:${PPA}"
    ssh_vm "DEBIAN_FRONTEND=noninteractive apt-get install -y authd"

    # Install broker, configure and restart services
    ssh_vm "snap install ${broker} --channel=${channel}"
    ssh_vm "cp /snap/${broker}/current/conf/authd/${broker#authd-}.conf /etc/authd/brokers.d/"
    ssh_vm "sed -i \
        -e 's|<ISSUER_ID>|'${ISSUER_ID}'|g' \
        -e 's|<CLIENT_ID>|'${CLIENT_ID}'|g' \
        -e 's|<CLIENT_SECRET>|'${CLIENT_SECRET}'|g' \
        /var/snap/${broker}/current/broker.conf"
    ssh_vm "systemctl restart authd.service"
    ssh_vm "snap restart ${broker}"

    # Reboot VM and wait until it's back
    virsh reboot "e2e-runner-${RELEASE}"
    retry --times 30 --delay 3 -- ./e2e-tests/vm/ssh.sh --release "${RELEASE}" -- true

    # Create snapshot for the installed state
    SNAPSHOT="${broker}-installed"
    if [[ "$channel" == "stable" ]]; then
        SNAPSHOT="${broker}-stable-installed"
    fi
    virsh snapshot-create-as --domain "e2e-runner-${RELEASE}" --name "${SNAPSHOT}"
done
```

## Building YARF

Now that the VM is ready with authd and the desired brokers installed and configured, we need to build YARF in order to run the tests.

:memo: **Note:** This process is automated through the `e2e-tests/setup-yarf.sh` script, which you can use instead of following the steps below manually.

1. Initialize the YARF submodule (YARF is bundled as a git submodule at `e2e-tests/.yarf`):

    ```bash
    git submodule update --init --depth=1 e2e-tests/.yarf
    ```

2. Build and set up the virtual environment
    1. Install `uv`

        ```bash
        sudo snap install --classic astral-uv
        ```

    2. Build YARF

        ```bash
        cd e2e-tests/.yarf

        uv sync

        uv pip install '.[develop]'
        uv pip install pygobject
        uv pip install ansi2html
        uv pip install .
        ```

        :bulb: **Tip:** YARF will be built inside a virtual environment, so every time you want to run it, the environment needs to be activated

        ```bash
        source e2e-tests/.yarf/.venv/bin/activate
        ```

## Running the tests

Now that everything is set up, we can finally run the tests. Make sure to activate the YARF virtual environment first.
In order to facilitate running the tests, the `e2e-tests/run-tests.sh` script is provided.

- The following environment variables (or equivalent command-line options) must be set before running the script:
  - `E2E_USER` - The username to use for the tests
  - `E2E_PASSWORD` - The password for the user account
  - `TOTP_SECRET` - The TOTP secret used to generate OTP codes for the user's MFA
  - `BROKER` - The broker to test (e.g., authd-msentraid)
  - `RELEASE` - The Ubuntu release the VM is running (e.g., resolute)

- If the script is run without arguments, it will run all the tests. You can also provide a specific test file and it will run only that test.

 ```bash
 export E2E_USER="your_username"
 export E2E_PASSWORD="your_password"
 export TOTP_SECRET="your_totp_secret"
 export BROKER="your_broker_name" # e.g. authd-msentraid
 export RELEASE="resolute"

 # To run all tests
 ./e2e-tests/run-tests.sh

 # To run a single test file
 ./e2e-tests/run-tests.sh tests/test_name.robot
 ```

Additional options include `--rerunfailed` to re-run only the tests that failed in the previous run, and `--output-dir` to specify a custom output directory.

It will take care of creating and linking the necessary directories and files, reverting to the correct snapshot, and running YARF with the correct parameters. By default, the test output is saved in a subdirectory of `${XDG_RUNTIME_DIR}/authd-e2e-test-runs`.
