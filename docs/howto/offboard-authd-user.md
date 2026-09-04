---
myst:
  html_meta:
    "description lang=en":
      "Permanently remove an authd user's access and local account from an Ubuntu host."
---

# Offboard an authd user

Offboarding has an identity provider step and a host step.

## Disable provider access

Disable or delete the user in the identity provider. Revoke active sessions
according to the provider's normal offboarding process.

Disabling provider access alone is sufficient to prevent new authd logins if
[the force provider access check](ref::config-force-provider-auth)
is enabled. Otherwise, the user may still log in with a cached local password
while the provider is unreachable, so delete the local authd account on
each affected host as described below.

## Remove the local account

Before deleting the local account, review [UID and GID
conflicts](/explanation/security.md#uid-and-gid-conflicts). Deleting a user or
group can allow authd to reuse its numeric ID, which may expose files left
behind under that ID. If you only need to prevent login, [lock the
account](lock-authd-user.md) instead to preserve its UID and GID and
avoid reusing those IDs.

On each affected host:

1. If the user is listed explicitly in the broker's `allowed_users` setting,
   remove the entry and restart the broker. See [Configure allowed
   users](ref::config-allowed-users).
2. End any existing sessions:

   ```shell
   sudo loginctl terminate-user alice@example.com
   ```

   This logs the user out of all sessions on that host and may discard unsaved
   work.
3. Decide whether to keep or remove the home directory and its data.
4. Delete the local authd record:

   ```shell
   sudo authctl user delete alice@example.com
   ```

   To remove the home directory as well, use `--remove`:

   ```shell
   sudo authctl user delete --remove alice@example.com
   ```

`authctl user delete` releases the user's UID. Handle any files that must be
kept, removed, or reassigned before deleting the record. The command does not
remove files outside the home directory. See the [`authctl` reference](reference::cli)
for the command warning and options.

You can verify that the local record has been removed with:

```shell
getent passwd alice@example.com
```

Deleting the local record does not delete the identity provider account. The
provider step must be completed to prevent the user from being registered
again on a later login.
