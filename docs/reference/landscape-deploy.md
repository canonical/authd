---
myst:
  html_meta:
    "description lang=en": "Deploy authd at scale with Landscape and remote script execution."
---

# Reference snippets for Landscape deployment script

[Landscape](https://documentation.ubuntu.com/landscape/) is a systems
management tool for the remote provisioning and management of Ubuntu machines.

Landscape can be used to [remotely execute
scripts](https://documentation.ubuntu.com/landscape/how-to-guides/web-portal/web-portal-24-04-or-later/use-script-profiles/)
on client machines.

This page provides example snippets that can be used in your own deployment
scripts to install and configure authd on Ubuntu machines at scale.

```{note}
When you deploy the final script using Landscape,
ensure that the script is run as the `root` user.
```

## Setup

Define the following environmental variables:

```bash
ISSUER_ID=<ISSUER_ID>
CLIENT_ID=<CLIENT_ID>
```

## Installation

Install the authd deb and the broker snap:

:::::{tab-set}
:sync-group: broker

::::{tab-item} Google IAM
:sync: google

```shell
add-apt-repository -y ppa:ubuntu-enterprise-desktop/authd
apt-get upgrade -y
apt-get install -y authd
snap install authd-google
```
::::


::::{tab-item} Microsoft Entra ID
:sync: msentraid

```shell
add-apt-repository -y ppa:ubuntu-enterprise-desktop/authd
apt-get upgrade -y
apt-get install -y authd
snap install authd-msentraid
```

::::


:::::

```{tip}
For more information on installing authd and its brokers, read the
[installation guide](howto::install).
```

## Configuration

Configure authd and the broker:

:::::{tab-set}
:sync-group: broker

::::{tab-item} Google IAM
:sync: google

```shell
sed -i "s|<CLIENT_ID>|$CLIENT_ID|g; s|<ISSUER_ID>|$ISSUER_ID|g" /var/snap/authd-google/current/broker.conf
echo "ssh_allowed_suffixes = @example.com" >> /var/snap/authd-google/current/broker.conf
mkdir -p /etc/authd/brokers.d/
cp /snap/authd-google/current/conf/authd/google.conf /etc/authd/brokers.d/
cat <<EOF >> /etc/ssh/sshd_config.d/authd.conf
UsePAM yes
Match User *@example.com
    KbdInteractiveAuthentication yes
EOF
```

::::

::::{tab-item} Microsoft Entra ID
:sync: msentraid

```shell
sed -i "s|<CLIENT_ID>|$CLIENT_ID|g; s|<ISSUER_ID>|$ISSUER_ID|g" /var/snap/authd-msentraid/current/broker.conf
echo "ssh_allowed_suffixes = @example.onmicrosoft.com" >> /var/snap/authd-msentraid/current/broker.conf
mkdir -p /etc/authd/brokers.d/
cp /snap/authd-msentraid/current/conf/authd/msentraid.conf /etc/authd/brokers.d/
cat <<EOF >> /etc/ssh/sshd_config.d/authd.conf
UsePAM yes
Match User *@example.onmicrosoft.com
    KbdInteractiveAuthentication yes
EOF
```

::::

:::::

```{tip}
For more information on configuring authd, read the [configuration
guide](ref::config).
```

## Restart the services

Restart the authd daemon, the broker snap, and the SSH service:

:::::{tab-set}
:sync-group: broker

::::{tab-item} Google IAM
:sync: google

```shell
systemctl restart authd ssh
snap restart authd-google
```

::::

::::{tab-item} Microsoft Entra ID
:sync: msentraid

```shell
systemctl restart authd ssh
snap restart authd-msentraid
```

::::

:::::

When you have a complete script, add it to the Landscape dashboard to run as
the `root` user before executing on the target machines.

## Authentication

Once the script is deployed, user login should be possible with authd.

For example, [using SSH](../howto/login-ssh.md):

```text
ssh <username>@<host>
```

## Additional information

* [Blog on Entra ID authentication on Ubuntu at scale](https://ubuntu.com/blog/entra-id-authentication-on-ubuntu-at-scale-with-landscape)
* [Video on Entra ID authentication on Ubuntu Desktop at scale](https://www.youtube.com/watch?v=1tYNEby5-hw)
