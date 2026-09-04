---
myst:
  html_meta:
    "description lang=en":
      "Block an authd user from logging in while keeping their local account,
      UID, and files."
---

# Lock an authd user

Locking a user stops them from logging in through authd, but keeps their local
account. Lock a user instead of [deleting](offboard-authd-user.md) them when
you want to:

* Suspend access temporarily and restore it later.
* Keep the user's UID and GID reserved, so that authd cannot assign them to
  another user while the first user's files are still on the system. See [UID
  and GID conflicts](/explanation/security.md#uid-and-gid-conflicts).

## Lock the account

Run this on each host where the user has logged in:

```shell
sudo authctl user lock alice@example.com
```

Locking does not end sessions that are already running. To log the user out of
the host:

```shell
sudo loginctl terminate-user alice@example.com
```

The lock stays in effect if the user is renamed in the identity provider,
because authd also matches the record by the provider's user ID.

```{note}
SSH public-key authentication does not involve authd, so a locked user can
still log in with an SSH key. See [SSH public key
authentication](ref::ssh-public-key-authentication).
```

## Unlock the account

```shell
sudo authctl user unlock alice@example.com
```
