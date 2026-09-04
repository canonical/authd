---
myst:
  html_meta:
    "description lang=en":
      "Change or reset the local password for an authd user."
---

# Change or reset a local authd password

After the first login, an authd user has a local password. When the user signs
in with an Entra ID password through the Entra authentication flow, authd uses
that password as the local password. Passwordless Entra authentication and
other provider flows prompt the user to create a local password. The local
password can be used for subsequent logins. It can also be used for offline
logins when the broker supports them.

## Change a known password

As the user, run:

```shell
passwd
```

The password policy is configured through libpwquality. See
[Configure password quality](ref::config-pwquality).

## Reset a forgotten password

To reset a forgotten password, the user must complete an online authentication
flow and then choose a new local password:

1. Start a login through [GDM](login-gdm.md) or [SSH](login-ssh.md).
2. Return to the authentication-method selection instead of entering the
   current local password.
3. Select the device code flow, or another provider flow that can set a local
   password.
4. Complete the online authentication and enter the new local password when
   prompted.

In the SSH flow, enter `r` at the local-password prompt to return to the
authentication-method selection.
