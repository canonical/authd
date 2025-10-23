# End-to-end tests

## Explanation

The end-to-end tests are implemented using [YARF](https://github.com/canonical/yarf). They cover a wide range of scenarios,
both for authd and the brokers.

## Running the tests locally

Running the tests locally requires a bit of setting up. This is a step-by-step guide to get you started.

### 1. Install the required dependencies

The tests have mainly two sets of dependencies: one required to configure and run the VM and one to build YARF from source.

- Virtualization dependencies:

    ```text
    libvirt0
    libvirt-clients
    libvirt-clients-qemu
    libvirt-daemon
    libvirt-daemon-system
    libvirt-daemon-driver-qemu
    qemu-system-x86
    qemu-utils
    qemu-kvm
    socat
    sshpass
    wget
    cloud-image-utils
    ```

    Those are all part of the archive and can be installed on Ubuntu with:

    ```bash
    sudo apt install libvirt0 libvirt-clients libvirt-clients-qemu libvirt-daemon libvirt-daemon-system libvirt-daemon-driver-qemu qemu-system-x86 qemu-utils qemu-kvm socat sshpass wget cloud-image-utils
    ```

- Test-run dependencies:

    ```text
    clang
    libxkbcommon-dev
    libcairo2-dev
    libgirepository-2.0-dev
    python3-tk
    python3-gi
    python3-cairo
    xvfb
    ffmpeg
    gir1.2-webkit2-4.1
    ```

    Those are all part of the archive and can be installed on Ubuntu with:

    ```bash
    sudo apt install clang libxkbcommon-dev libcairo2-dev libgirepository-2.0-dev python3-tk python3-gi python3-cairo xvfb ffmpeg gir1.2-webkit2-4.1
    ```

### 2. Setup the VM

The tests need a VM to run. This can be easily setup by using the domain definition and cloud-init configuration provided in the repository.

1. Download the latest Ubuntu Desktop image (and resize it):

    ```bash
    wget https://cloud-images.ubuntu.com/questing/current/questing-server-cloudimg-amd64.img

    qemu-img resize questing-server-cloudimg-amd64.img 10G
    ```

2. Create the cloud-init iso using the provided configuration in `e2e-tests/vm/runner-cloud-cfg.yaml`:
   1. Create a directory called `seed/` and copy the YAML file there. The file must be named `user-data`.
   2. Create the `seed.iso` file using `cloud-localds`.

    ```bash
    mkdir -p seed/

    cp ./e2e-tests/vm/runner-cloud-cfg.yaml seed/user-data

    cloud-localds seed.iso seed/user-data
    ```

3. Define the VM using the provided XML in `e2e-tests/vm/e2e-runner.xml`:
   1. Edit the XML and set the correct paths for the disk image. Look for something like:

        ```xml
        <disk type='file' device='disk'>
            <driver name='qemu' type='qcow2' />
            <source file='{path_to_downloaded_img}' />
            <target dev='vda' bus='virtio' />
            <address type='pci' domain='0x0000' bus='0x04' slot='0x00' function='0x0' />
        </disk>
        ```

   2. Define the VM with `virsh`:

        ```bash
        virsh define {path_to_authd_oidc_brokers_root}/e2e-tests/vm/e2e-runner.xml
        ```

4. Attach the cloud-init iso to the VM:

    ```bash
    virsh attach-disk --domain e2e-runner --source {path_to_the_cloud-init_iso}/seed.iso --target sda --type cdrom --config
    ```

5. Start the domain and wait for it to finish the initial setup:

    ```bash
    virsh start e2e-runner
    ```

    - The cloud-init process will take a few moments to finish. You can check the progress by connecting to the VM's console:

        ```bash
        virsh console e2e-runner
        ```

    - The domain will automatically shutdown once the initial setup is complete. You can then start it again with `virsh start e2e-runner`.
    - Now that the initial setup is done, we need to create a snapshot of this "fresh install" state, so we can revert to it later when installing authd.

        ```bash
        virsh snapshot-create-as e2e-runner --name fresh-install --reuse-external
        ```

### 3. Install authd and the brokers

Now that the VM is ready, we need to install authd and the brokers we want to test.

:memo: **Note:** You can find scripts for the installation processes at the [end of the section](#Scripts-to-automate-the-installation-and-configuration-of-authd-and-brokers).

#### 1. Install authd (stable and edge versions)

- This can be done either through GUI or by SSH. For simplicity, we will use SSH here.

- The default username is `ubuntu` and the password is whatever you set in the cloud-init file.
- To avoid having to interactively type the password every time, we will use `sshpass` to provide it non-interactively.
- The VM uses `socat` to forward the SSH port over VSOCK, so the SSH command will look like this:

    ```bash
    sshpass -p <vm_password> ssh -o StrictHostKeyChecking=no -o ProxyCommand="socat - VSOCK-CONNECT:1000:22" ubuntu@localhost
    ```

- From now on, the command will be referred to as `ssh_vm` for simplicity. You can create an alias for it:

    ```bash
    alias ssh_vm='sshpass -p <vm_password> ssh -o StrictHostKeyChecking=no -o ProxyCommand="socat - VSOCK-CONNECT:1000:22" ubuntu@localhost'
    ```

- Now we can proceed to install authd and the brokers. The following steps need to be repeated for each version of authd we want to test (stable and edge).

##### Install authd

1. Revert to the "fresh install" snapshot:
    - And start the domain if the snapshot was taken with the domain powered off.

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

2. Install the desired broker (replace `<broker>` and `<channel>` with the desired values):

    ```bash
    ssh_vm "sudo snap install <broker> --channel=<channel>"
    ```

3. Configure authd to recognize the installed broker.

    ```bash
    ssh_vm "sudo mkdir -p /etc/authd/brokers.d"
    ssh_vm "sudo cp /snap/<broker>/current/conf/authd/<broker>.conf /etc/authd/brokers.d/"
    ```

4. Configure the installed broker:
   1. Write the configuration file for the broker
       - Writing the file to /tmp/ first and then moving it is easier.
       - Some brokers may require different configuration options. Please refer to the broker documentation for more details.

       ```bash
       ssh_vm "sudo printf \"\
       [oidc]\n\
       issuer = <desired_issuer>\n\
       client_id = <desired_client_id>\n\
       force_provider_authentication = false\n\
       [users]\n\
       ssh_allowed_suffixes = <desired_username>\n\
       allowed_users = OWNER\n\
       owner = <desired_username>\n\" | sudo tee /tmp/broker.conf"
       ```

   2. Move the configuration file to the correct location and ensure the ownership and permissions are correct.

       ```bash
       ssh_vm "sudo install -o root -g root -m 600 /tmp/broker.conf /var/snap/<broker>/current/broker.conf"
       ```

   3. Restart authd and the broker to apply the changes.

       ```bash
       ssh_vm "sudo systemctl restart authd.service"

       # This could fail sometimes if the restart is triggered too quickly, so don't be afraid to wait a couple of seconds and retry
       ssh_vm "sudo snap restart <broker>"
       ```

5. Reboot the VM to ensure the snapshot is taken from the login screen.

    ```bash
    virsh reboot e2e-runner

    # Wait a while for the VM to reboot
    sleep 120s
    ```

6. Create a snapshot of this state, so we can revert to it later when running the tests.

    ```bash
    virsh snapshot-create-as e2e-runner --name "<broker>-<channel>-configured" --reuse-external
    ```

#### Scripts to automate the installation and configuration of authd and brokers

##### Script to install authd (both stable and edge versions)

```bash
#!/usr/bin/env bash

set -eux

declare -a versions=("edge" "stable")
for version in "${versions[@]}"; do
    echo "Setting up authd - $version"

    virsh snapshot-revert e2e-runner fresh-install

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

broker="desired_broker_name" # e.g. authd-msentraid
authd_broker_config="authd_related_broker_cfg_file"
issuer="your_oidc_issuer"
client_id="your_oidc_client_id"
username="your_allowed_username"

declare -a versions=("edge" "stable")
for version in "${versions[@]}"; do
    echo "Setting up $broker broker - $version"

    sudo virsh snapshot-revert e2e-runner "authd-$version-installed"

    ssh_vm "sudo snap install $broker --channel=$version"

    # Configure authd
    ssh_vm "sudo mkdir -p /etc/authd/brokers.d"
    ssh_vm "sudo cp /snap/$broker/current/conf/authd/$authd_broker_config /etc/authd/brokers.d/"

    # Configure the broker
    ssh_vm "sudo printf \"\
    [oidc]\n\
    issuer = $issuer\n\
    client_id = $client_id\n\
    force_provider_authentication = false\n\
    [users]\n\
    ssh_allowed_suffixes = $username\n\
    allowed_users = OWNER\n\
    owner = $username\n\" | sudo tee /tmp/broker.conf"

    # Move file while ensuring correct ownership and permissions
    ssh_vm "sudo install -o root -g root -m 600 /tmp/broker.conf /var/snap/$broker/current/broker.conf"

    # Restart authd and broker to apply the changes
    ssh_vm "sudo systemctl restart authd.service"

    # Retry restarting the broker snap a few times, as it may fail if the restart happens too quickly
    set +e
    for i in {1..10}; do
    ssh_vm "sudo snap restart $broker"
    exit_code=$?
    if [[ "$exit_code" == 0 || "$exit_code" == 255 ]]; then
        echo "Broker service was restarted successfully"
        break
    fi
    echo "Restart failed, retrying in a few seconds..."
    sleep 6s
    done

    # Reboot the VM to ensure a clean snapshot
    sudo virsh reboot e2e-runner

    sleep 120s

    # Create snapshot for broker configured state
    sudo virsh snapshot-create-as e2e-runner --name "$broker-$version-configured" --reuse-external
done
```

### 4. Building YARF

Now that the VM is ready with authd and the desired brokers installed and configured, we need to build YARF in order to run the tests.

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

### 5. Running the tests

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
