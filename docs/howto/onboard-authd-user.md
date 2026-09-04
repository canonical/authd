---
myst:
  html_meta:
    "description lang=en": "Give an identity provider user access to an Ubuntu system with authd."
---

# Onboard an authd user

authd creates a local user record the first time a user logs in successfully
through a configured identity broker. You do not create this record with
`authctl`.

Before you start, make sure that authd and a broker are installed and
configured. See [Install authd](install-authd.md) and
[Configure authd](ref::config).

## Allow the user to log in

The default broker configuration allows only the first user to log in. That
user becomes the owner of the machine. To allow all users who can authenticate
with the identity provider, set `allowed_users` to `ALL`:

:::::{tab-set}
:sync-group: broker

::::{tab-item} Microsoft Entra ID
:sync: msentraid

Edit `/var/snap/authd-msentraid/current/broker.conf`:

```ini
[users]
allowed_users = ALL
```
Then restart the broker:

```shell
sudo systemctl restart snap.authd-msentraid.authd-msentraid.service
```
::::

::::{tab-item} Google IAM
:sync: google

Edit `/var/snap/authd-google/current/broker.conf`:

```ini
[users]
allowed_users = ALL
```
Then restart the broker:

```shell
sudo systemctl restart snap.authd-google.authd-google.service
```
::::

::::{tab-item} Keycloak
:sync: keycloak

Edit `/var/snap/authd-oidc/current/broker.conf`:

```ini
[users]
allowed_users = ALL
```
Then restart the broker:

```shell
sudo systemctl restart snap.authd-oidc.authd-oidc.service
```
::::
:::::

See [Configure allowed users](ref::config-allowed-users) for other access
policies.

## Have the user log in

Have the user complete an online login through [GDM](login-gdm.md). To onboard
through [SSH](login-ssh.md), first configure `ssh_allowed_suffixes_first_auth`
as described in the SSH guide; first-time SSH login is disabled by default.

After the first successful login, authd creates the local account and its home
directory.

## Verify the account

You can verify that the account is available on the system with the following
commands:

```shell
getent passwd alice@example.com
id alice@example.com
```

To change the user's local shell, home directory, or UID after onboarding, use
the commands in the [`authctl` reference](reference::cli).
