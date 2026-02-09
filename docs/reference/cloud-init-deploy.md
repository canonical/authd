---
myst:
  html_meta:
    "description lang=en": "Deploy authd at scale with cloud-init."
---

# Reference snippets for cloud-init provisioning

[Cloud-init](https://cloudinit.readthedocs.io/en/latest/) is an
industry-standard method for cloud instance initialization. It can also be used
to provision client machines during Ubuntu installation.

This page provides example snippets, which can be used in your own cloud config
YAML files to deploy and configure authd on Ubuntu at scale.

## Setup

If using these snippets as part of a
[cloud config](https://cloudinit.readthedocs.io/en/latest/explanation/about-cloud-config.html)
file, set the appropriate header, identifying the file to cloud-init with
`#cloud-config` and enabling Jinja templating with `## template: jinja`.

```yaml
## template: jinja
#cloud-config
```

## Variables

Define the necessary environmental variables:

```jinja
{% set ISSUER_ID = '<your_issuer_id>' %}
{% set CLIENT_ID = '<your_client_id>' %}
```

## Install authd

Add the authd PPA to the system's software sources and install authd:

```yaml
apt:
  sources:
      source1:
          source: 'ppa:ubuntu-enterprise-desktop/authd'

packages:
  - authd
```

## Install broker

Install the broker as a snap:

:::::{tab-set}
:sync-group: broker

::::{tab-item} Google IAM
:sync: google

```yaml
snap:
 commands:
   - ['install', 'authd-google']
```

::::

::::{tab-item} Microsoft Entra ID
:sync: msentraid

```yaml
snap:
 commands:
   - ['install', 'authd-msentraid']
```

::::
:::::


```{tip}
For more information on installing authd and its brokers, read the
[installation guide](howto::install).
```

## Install authd and apply configurations

To complete the setup:

* Configure SSH for user login
* Upgrade packages
* Configure authd and the broker
* Restart the services for the changes to take effect

```{important}
Edit the allowed suffixes as appropriate.
```

:::::{tab-set}
:sync-group: broker

::::{tab-item} Google IAM
:sync: google

```yaml
write_files:
  - path: /etc/ssh/sshd_config.d/authd.conf
    content: |
      UsePAM yes
      Match User *@example.com
          KbdInteractiveAuthentication yes

runcmd:
  - apt-get upgrade -y
  - sed -i 's|<CLIENT_ID>|{{ CLIENT_ID }}|g; s|<ISSUER_ID>|{{ ISSUER_ID }}|g' /var/snap/authd-google/current/broker.conf
  - echo 'ssh_allowed_suffixes = @example.com' >> /var/snap/authd-google/current/broker.conf
  - sed -i 's/^\(LOGIN_TIMEOUT\t\t\)[0-9]\+/\1360/' /etc/login.defs
  - mkdir -p /etc/authd/brokers.d/
  - cp /snap/authd-google/current/conf/authd/google.conf /etc/authd/brokers.d/
  - snap restart authd-google
  - systemctl restart authd ssh
```

::::

::::{tab-item} Microsoft Entra ID
:sync: msentraid


```yaml
write_files:
  - path: /etc/ssh/sshd_config.d/authd.conf
    content: |
      UsePAM yes
      Match User *@example.onmicrosoft.com
          KbdInteractiveAuthentication yes

runcmd:
  - apt-get upgrade -y
  - sed -i 's|<CLIENT_ID>|{{ CLIENT_ID }}|g; s|<ISSUER_ID>|{{ ISSUER_ID }}|g' /var/snap/authd-msentraid/current/broker.conf
  - echo 'ssh_allowed_suffixes = @example.onmicrosoft.com' >> /var/snap/authd-msentraid/current/broker.conf
  - sed -i 's/^\(LOGIN_TIMEOUT\t\t\)[0-9]\+/\1360/' /etc/login.defs
  - mkdir -p /etc/authd/brokers.d/
  - cp /snap/authd-msentraid/current/conf/authd/msentraid.conf /etc/authd/brokers.d/
  - snap restart authd-msentraid
  - systemctl restart authd ssh
```

::::

:::::


```{tip}
For more information on configuring authd, read the [configuration
guide](ref::config).
```

## Authentication

Once the script is deployed, user login should be possible with authd.

For example, [using SSH](../howto/login-ssh.md):

```text
ssh <username>@<host>
```

## Additional information

* [Blog on Entra ID authentication on Ubuntu at scale](https://ubuntu.com/blog/entra-id-authentication-on-ubuntu-at-scale-with-landscape)
* [Video on Entra ID authentication on Ubuntu Desktop at scale](https://www.youtube.com/watch?v=1tYNEby5-hw)
