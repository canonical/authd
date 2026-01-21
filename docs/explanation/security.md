# Security overview for authd

authd lets Ubuntu systems verify user identities through trusted cloud identity
providers and integrate those identities into local logins securely.

This overview outlines the key design decisions, security practices, and
configuration options that affect the security of authd, and provides guidance
for administrators on deploying authd securely.

## Deploying authd securely as an administrator

The first section of this overview provides guidance for administrators on
deploying authd securely, and the second section explains design decisions that
were made to enhance the security of authd.

### Package updates

Keeping authd up to date is essential to ensure that security fixes are applied
as soon as they are available.

A full authd installation consists of both the authd Debian package and at least
one broker snap. Snaps are updated automatically. Further information is
provided in the [snap documentation on managing updates](https://snapcraft.io/docs/managing-updates).

Update Debian packages regularly, either manually or by enabling
[automatic updates](https://documentation.ubuntu.com/server/how-to/software/automatic-updates/).

### Security of the identity provider

As described in the [authd architecture](authd-architecture.md) explanation,
users authenticate with an identity provider, such as Microsoft Entra ID or
Google IAM. The security of any system using authd therefore depends on the
security of the configured identity provider.

Ensure that only authorized administrators have access to the identity provider.
Administrators can add or remove users or change user credentials, which allows
them to grant, revoke, or modify access to systems using authd.

### Allowed users

In addition to access control at the identity provider's side, allowed users can
be restricted locally. See the [Configure allowed users](ref::config-allowed-users)
section for details.

Ensure that only users who should have system access are configured as allowed
users.

### Local password

To improve usability, authd allows users to create a local password after
successfully authenticating with the identity provider. This password can then
be used for subsequent logins without repeating the full identity provider
authentication flow.

#### Password strength

Strong passwords are critical to prevent unauthorized access.

authd uses libpwquality to enforce password complexity requirements. See the
[Configure password quality](ref::config-pwquality) section for details.

#### Force provider authentication

If the identity provider is reachable during login, authd verifies that the user
is still allowed to authenticate with the identity provider. If the user’s
account has been disabled or removed, login is denied.

By default, if the identity provider cannot be reached (for example, due to
network issues), users can still log in with their local password. This is to
prevent accidental lockouts, but it also allows users whose access has been
revoked at the identity provider to log in while offline.

To enforce verification with the identity provider even when offline, enable the
[force_provider_authentication](ref::config-force-provider-auth) setting.

### Login via SSH

#### SSH public key authentication

If SSH public key authentication is enabled, users whose access has been revoked
at the identity provider can still log in using their SSH keys. This is because
SSH key authentication does not involve authd.

To prevent users with revoked access from logging in with SSH, disable public
key authentication for users managed by authd, by adding the following to by
adding the following to `/etc/ssh/sshd_config.d/authd.conf` or directly to
`/etc/ssh/sshd_config`:

```text
Match User *@example.com
    PubkeyAuthentication no
```

```{note}
Replace `@example.com` with the domain of your identity provider.
```

#### SSH password authentication

As described in the [SSH configuration](ref::ssh-configuration) section, PAM
integration must be enabled in sshd:

```text
UsePAM yes
KbdInteractiveAuthentication yes
```

This setup allows users managed by authd to log in through PAM.
It also enables PAM-based login for local Unix accounts, even when
`PasswordAuthentication no` is set.

Administrators who want to deviate from the default SSH configuration and
disallow password authentication for non-authd users can do so safely by using a
match block that re-enables keyboard-interactive authentication only for authd
users:

```text
KbdInteractiveAuthentication no
PasswordAuthentication no

Match User *@example.com
    KbdInteractiveAuthentication yes
```

```{note}
Replace `@example.com` with the domain of your identity provider.
```

### UID and GID conflicts

When a new user logs in for the first time, or when a user is added to a new
group in the identity provider (for providers that support group management, see
[Group management](https://documentation.ubuntu.com/authd/stable-docs/reference/group-management/)),
authd automatically assigns a unique user ID (UID) and group ID (GID).

Before assigning a UID or GID, authd checks that there are no collisions with
existing users or groups on the system. However, if a user or group is later
removed, or if the entire authd database (`/var/lib/authd/authd.sqlite3`) is
deleted, previously assigned IDs can be reused for new users or groups.

This reuse can allow unintended access to files or directories owned by the old
user, as the new user would inherit their numeric UID or GID.

To avoid this risk:

* Do not remove the authd database.
* Remove all files and directories owned by any users that you delete,
  especially if they contain sensitive data.

```{important}
A tool for removing authd users along with their home directories will be
provided in the future.
```

## How authd is designed for security

This section describes how authd is built to protect stored data and limit
system exposure.

### Stored secrets

authd stores user secrets under
`/var/snap/authd-<broker>/current/<issuer>/<user>/`.

That directory is created with mode `0700`, ensuring that only root can access
it.

The secrets that authd stores are described below.

#### Local password

A salted Argon2id hash of the local password is stored for verification. Hashing
parameters:
* Memory: 64 KB
* Iterations: 1
* Parallelism: 1

#### Tokens and user information

Tokens and user data retrieved during authentication — including the OAuth 2.0
refresh token and OpenID Connect `UserInfo` response — are cached to support
login with the local password. These values are currently stored in cleartext.

We recommend enabling [full disk encryption](https://documentation.ubuntu.com/security/docs/security-features/storage/encryption-full-disk/)
to protect these secrets in case of device theft or loss.

### Sandboxing

authd uses sandboxing to limit system exposure:

* The authd brokers run as [strictly confined](https://snapcraft.io/docs/snap-confinement)
  snaps. Their only granted interface is network, required to communicate with
  the identity provider.
* The authd service uses
  [systemd sandboxing options](https://manpages.ubuntu.com/manpages/noble/en/man5/systemd.exec.5.html#sandboxing)
  to restrict access to system resources.

Because authd acts as an authentication service, a vulnerability in authd could
still be exploited to gain full root privileges.

## Reporting a vulnerability

See the [authd security policy](https://github.com/canonical/authd?tab=security-ov-file#security-ov-file)
for details on how to report security vulnerabilities in authd.
