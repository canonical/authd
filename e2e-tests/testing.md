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
    cloud-image-utils
    libvirt-clients-qemu
    libvirt-daemon-system
    qemu-kvm
    socat
    wget
    ```

    Those are all part of the archive and can be installed on Ubuntu with:

    ```bash
    sudo ./e2e-tests/vm/install-provision-deps.sh
    ```

- Test-run dependencies:

    ```text
    ffmpeg
    gir1.2-webkit2-4.1
    libxkbcommon-dev
    libcairo2-dev
    libgirepository-2.0-dev
    python3-tk
    python3-gi
    python3-cairo
    xvfb    
    ```

    Those are all part of the archive and can be installed on Ubuntu with:

    ```bash
    sudo ./e2e-tests/install-deps.sh
    ```

### 2. Setup the VM

The tests need a VM to run. This can be easily setup by using the domain definition and cloud-init configuration provided in the repository.

1. Download the latest Ubuntu Desktop image (and resize it):

    ```bash
    wget https://cloud-images.ubuntu.com/questing/current/questing-server-cloudimg-amd64.img

    qemu-img resize questing-server-cloudimg-amd64.img 10G
    ```

2. Create the cloud-init iso using the provided configuration in `e2e-tests/vm/cloud-init-template.yaml`:
   1. Update the file with the ssh key that will be used to access the VM.
   2. Create a directory and copy the YAML file there. The file must be named `user-data`.
   3. Create the `seed.iso` file using `cloud-localds`.

        ```bash

        mkdir -p /tmp/seed/

        SSH_PUBLIC_KEY=$(cat "${SSH_PUBLIC_KEY_FILE}") \
            envsubst < e2e-tests/vm/cloud-init-template.yaml > /tmp/seed/user-data

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
    virsh attach-disk --domain e2e-runner --source /tmp/seed.iso --target sda --type cdrom --config
    ```

5. Start the domain and wait for it to finish the initial setup:

    ```bash
    virsh start e2e-runner
    ```

    - The cloud-init process will take a few moments to finish. You can check the progress by connecting to the VM's console:

        ```bash
        virsh console e2e-runner
        ```

    - The domain will automatically shutdown once the initial setup is complete, so you need to start it again.

        ```bash
        virsh start e2e-runner

        # It will take a while, so make sure to wait until it's up and running
        sleep 180s
        ```

    - Now that the initial setup is done, we need to create a snapshot of this fresh state, so we can revert to it later when installing authd.

        ```bash
        virsh snapshot-create-as e2e-runner --name initial-setup --reuse-external
        ```

### 3. Install authd and the brokers

Now that the VM is ready, we need to install authd and the brokers we want to test.

:memo: **Note:** You can find scripts for the installation processes at the [end of the section](#Scripts-to-automate-the-installation-and-configuration-of-authd-and-brokers).

#### 1. Install authd (stable and edge versions)

- This can be done either through GUI or by SSH. For simplicity, we will use SSH here.

- The default username is `ubuntu` and the password is whatever you set in the cloud-init file.
- The VM uses `socat` to forward the SSH port over VSOCK, so the SSH command will look like this:

    ```bash
    ssh -o ProxyCommand="socat - VSOCK-CONNECT:1000:22" -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no -o LogLevel=ERROR ubuntu@localhost
    ```

- From now on, the command will be referred to as `ssh_vm` for simplicity. You can use the `e2e-tests/vm/ssh.sh` script instead of typing the full command every time.
- Now we can proceed to install authd and the brokers. The following steps need to be repeated for each version of authd we want to test (stable and edge).

##### Install authd

1. Revert to the "fresh install" snapshot:

    ```bash
    virsh snapshot-revert e2e-runner fresh-install
    ```

2. Install authd:

    ```bash
    ssh_vm "sudo DEBIAN_FRONTEND=noninteractive add-apt-repository -y ppa:ubuntu-enterprise-desktop/authd-edge"
    ssh_vm "sudo DEBIAN_FRONTEND=noninteractive apt-get install -y authd"
    ```

3. Create a snapshot of this state, so we can revert to it later when we proceed to configure the brokers.

    ```bash
    virsh snapshot-create-as e2e-runner --name authd-edge-installed --reuse-external
    ```

4. Repeat the above steps for the stable version of authd, just changing the PPA to `ppa:ubuntu-enterprise-desktop/authd` and naming the snapshot `authd-stable-installed`.

#### 2. Install the desired brokers

Repeat the following steps for each version of each broker you want to test;

1. Revert to the desired authd snapshot (either stable or edge) to ensure a clean state:

    ```bash
    virsh snapshot-revert e2e-runner "authd-<version>-installed"
    ```

2. Install the desired broker (replace `${broker}` and `${channel}` with the desired values):

    ```bash
    ssh_vm "sudo snap install ${broker} --channel=${channel}"
    ```

3. Configure authd to recognize the installed broker.

    ```bash
    ssh_vm "sudo mkdir -p /etc/authd/brokers.d"
    ssh_vm "sudo cp /snap/${broker}/current/conf/authd/${broker}.conf /etc/authd/brokers.d/"
    ```

4. Configure the installed broker:

    ```bash
    ssh_vm "sudo sed -i -e 's|<ISSUER_ID>|'${ISSUER_ID}'|g' \
                          -e 's|<CLIENT_ID>|'${CLIENT_ID}'|g' \
                          -e 's|#ssh_allowed_suffixes_first_auth =|ssh_allowed_suffixes_first_auth = '${E2E_USER}|g' \
                          /var/snap/${broker}/current/broker.conf"
    ```

5. Restart authd and the broker to apply the changes.

    ```bash
    ssh_vm "sudo systemctl restart authd.service"

    # This could fail sometimes if the restart is triggered too quickly, so don't be afraid to wait a couple of seconds and retry
    ssh_vm "sudo snap restart ${broker}"
    ```

6. Reboot the VM to ensure the snapshot is taken from the login screen.

    ```bash
    virsh reboot e2e-runner

    # Wait a while for the VM to reboot
    sleep 180s
    ```

7. Create a snapshot of this state, so we can revert to it later when running the tests.

    ```bash
    virsh snapshot-create-as e2e-runner --name "${broker}-${channel}-configured" --reuse-external
    ```

#### Scripts to automate the installation and configuration of authd and brokers

##### Script to install authd (both stable and edge versions)

```bash
#!/usr/bin/env bash

set -eux

declare -a versions=("edge" "stable")
for version in "${versions[@]}"; do
    echo "Setting up authd - $version"

    virsh snapshot-revert e2e-runner initial-setup

    PPA="ppa:ubuntu-enterprise-desktop/authd-edge"
    if [[ "$version" == "stable" ]]; then
    PPA="ppa:ubuntu-enterprise-desktop/authd"
    fi

    ssh_vm "sudo DEBIAN_FRONTEND=noninteractive add-apt-repository -y $PPA"
    ssh_vm "sudo DEBIAN_FRONTEND=noninteractive apt-get install -y authd"

    # Create snapshot to be used as base for broker configuration
    virsh snapshot-create-as e2e-runner --name "authd-$version-installed" --reuse-external
done
```

##### Script to automate the above steps for both stable and edge versions of authd and the broker

Update the variables at the top of the script to match your desired broker and configuration.

```bash
#!/usr/bin/env bash

set -eux

broker="desired_broker_name" # Change to the desired broker name

declare -a channels=("edge" "stable")
for channel in "${channels[@]}"; do
    echo "Setting up $broker broker - $channel"

    virsh snapshot-revert e2e-runner "authd-$channel-installed"

    # Install broker, configure and restart services
    ssh_vm "sudo snap install ${broker} --channel=${channel} && \
            sudo mkdir -p /etc/authd/brokers.d && \
            sudo cp /snap/${broker}/current/conf/authd/${broker_config} /etc/authd/brokers.d/ && \
            sudo sed -i -e 's|<ISSUER_ID>|'${ISSUER_ID}'|g' \
                        -e 's|<CLIENT_ID>|'${CLIENT_ID}'|g' \
                        -e 's|#ssh_allowed_suffixes_first_auth =|ssh_allowed_suffixes_first_auth = '${AUTHD_USER}|g' \
                        /var/snap/${broker}/current/broker.conf && \
            sudo systemctl restart authd.service && \
            sudo snap restart ${broker}"

    # Reboot VM and wait until it's back
    virsh reboot e2e-runner

    retry --times 10 --delay 3 -- ssh_vm "systemctl is-system-running --wait"

    # Create snapshot for broker configured state
    virsh snapshot-create-as e2e-runner --name "$broker-$channel-configured" --reuse-external
done
```

## Building YARF

Now that the VM is ready with authd and the desired brokers installed and configured, we need to build YARF in order to run the tests.

:memo: **Note:** This process is automated through the `e2e-tests/setup-yarf.sh` script, which you can use instead of following the steps below manually.

1. Clone the YARF repository (either directly or by forking it first):

    ```bash
    git clone https://github.com/canonical/yarf
    ```

2. Build and set up the virtual environment
    1. Install `uv`

        ```bash
        sudo snap install --classic astral-uv
        ```

    2. Build YARF

        ```bash
        cd {path_to_yarf_repo}

        uv sync

        uv pip install '.[develop]'
        source .venv/bin/activate
        uv pip install pygobject
        ```

        :bulb: **Tip:** YARF will be built inside a virtual environment, so every time you want to run it, the environment needs to be activated

        ```bash
        source {path_to_yarf_repo}/.venv/bin/activate
        ```

## Running the tests

Now that everything is set up, we can finally run the tests. Make sure to activate the YARF virtual environment first.
In order to facilitate running the tests, the `e2e-tests/run-tests.sh` script is provided.

- Some environment variables need to be set before running the script:
  - `E2E_USER` - The username to use for the tests
  - `E2E_PASSWORD` - The remote password to use for the tests
  - `BROKER` - The broker to test (e.g., authd-msentraid)

- Optionally, you can set:
  - `SNAPSHOT_ON_FAIL` - If set, a snapshot will be taken if a test fail. Default is "false".
  - `SHOW_WEBVIEW` - If set, the tests will run with a visible window. Default is "false" (headless).

- If the script is run without arguments, it will run all the tests. You can also provide a specific test file and it will run only that test.

 ```bash
 export E2E_USER="your_username"
 export E2E_PASSWORD="your_password"
 export BROKER="your_broker_name" # e.g. authd-msentraid
 export SNAPSHOT_ON_FAIL="true" # optional
 export SHOW_WEBVIEW="true" # optional

 # To run all tests
 ./e2e-tests/run-tests.sh

 # To run a single test file
 ./e2e-tests/run-tests.sh tests/test_name.robot
 ```

It will take care of creating and linking the necessary directories and files, reverting to the correct snapshot, and running YARF with the correct parameters. By default, the files will be saved in `/tmp/e2e-testrun-${BROKER}`.
