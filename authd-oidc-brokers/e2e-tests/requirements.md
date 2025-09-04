# Requirements for running the authd tests

<!-- TODO: Add instructions on how to build YARF from source and install the required dependencies -->

- 2 VMs with Ubuntu 24.04 installed;

- For the latest versions VM:
  - Install authd and the authd-msentraid broker from their respective PPA and snap edge channels:
    - `sudo add-apt-repository ppa:ubuntu-enterprise-desktop/authd-edge`;
    - `sudo apt update && sudo apt install authd`;
    - `sudo snap install authd-msentraid --channel=edge`;

- For the stable versions VM:
  - Install authd and the authd-msentraid broker from their respective PPA and snap stable channels:
    - `sudo add-apt-repository ppa:ubuntu-enterprise-desktop/authd`;
    - `sudo apt update && sudo apt install authd`;
    - `sudo snap install authd-msentraid`;

- Disable apport and the apt auto-update timers, as these could cause unexpected prompts to show on the screen:
  - `sudo systemctl disable --now apt-daily.timer`;
  - `sudo systemctl disable --now apt-daily-upgrade.timer`;
  - `sudo systemctl disable --now apport.service`;
  - Edit `/etc/default/apport` and set `enabled = 0`;

- Disable screen blank, as it could cause some of the long-running tests to fail:
  - Can be done easily either by using the `gsettings` command or by running:
    - `gsettings set org.gnome.desktop.session idle-delay 0`;

- Install and configure `openssh-server` in order to support SSH authentication:
  - `sudo apt install openssh-server`
  - Edit the `/etc/ssh/sshd_config` file to allow authentication through authd:
    - Set `PasswordAuthentication yes`
    - Set `UsePAM yes`
    - Set `KbdInteractiveAuthentication yes`
    - Restart the SSH service: `sudo systemctl restart ssh.service`

- Configure authd to recognize the msentraid broker and the broker to point to our test tenant;
  - To simplify the test execution, we assume a "standard" set of configuration values for the broker:
    - `issuer = <test issuer ID>`
    - `client_id = <test client ID>`
    - `client_secret = <test client secret>`
    - `ssh_allowed_suffixes = <full username of the test user>`
    - `allowed_users = OWNER`
    - `owner = <full username of the test user>`
  - This way, we can avoid needing to touch the broker configuration when we reach some of the tests that
    are meant to fail.
