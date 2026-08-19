---
myst:
    html_meta:
        "description lang=en":
            "The recommended way to manage users with authd is with the dedicated authctl tool."
---

(explanation::user-management)=
# User management with authd

authd is packaged with a Name Service Switch (NSS) module that allows
applications on the system to query authd-managed users and groups through
standard Linux user and group lookup interfaces. For administrators managing
users and groups, authd provides the `authctl` tool.

Other tools for managing Linux users, which rely on local files, may be
incompatible with authd. This page provides a brief explanation of how user
management works on authd and why `authctl` is the recommended tool for managing
users.

## Tools that modify `/etc/passwd` or `/etc/group` may not work

In Linux systems, `/etc/passwd` stores user account information required during
login, while `/etc/group` defines groups to which users belong.

Information about users managed by authd, and their remote groups, never need
to be written to `/etc/passwd` or `/etc/group`. For this reason, common tools
like `usermod`, `userdel`, and `groupmod`, which modify these files directly,
may not work consistently for authd-managed users.

Similarly, scripts or software that query `/etc/passwd` to get the home
directory of `<user>` by their ID will fail for authd-managed users. This also
applies to `/etc/group` for remote groups (groups from the identity provider
that aren't mapped to a local group).

```{admonition} Local and remote groups
:class: note
Groups from the identity provider can be mapped into local Linux groups for the
user. See the [group and privilege management
reference](reference::group-management) for details.
```

## authd provides an NSS module to get user information

Linux systems can resolve user and group information through NSS.
This enables administrators to specify which files to query for information,
such as user account details.

authd's NSS module gets information from its local cache of the identity
provider's users and groups. This is discussed in the [overview of authd's
architecture](explanation::authd-architecture).

## authd users should be managed using authctl

For authd-managed users and groups, use [`authctl`](reference::cli),
a dedicated command-line tool for user management.

`authctl` supports operations including locking users, deleting users, and
modifying user home directories.

Group membership and privileges are managed through the identity provider, as
described in the [group and privilege management guide](reference::group-management).

## Further reading

* [System databases and NSS in the GNU C library manual](https://sourceware.org/glibc/manual/latest/html_mono/libc.html#Name-Service-Switch)
* [authctl CLI reference documentation](reference::cli)
* [Group and privilege management with authd](reference::group-management)

