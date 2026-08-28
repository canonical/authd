---
myst:
  html_meta:
    "description lang=en": "Authentication flows supported by authd brokers."
---

# Authentication flows

An authentication flow is the sequence of steps a user goes through to verify
their identity at login.

## Google IAM

Google IAM supports the **device code flow**, where the user visits a URL
and enters a code to complete authentication.

## Microsoft Entra ID

Microsoft Entra ID supports the following authentication flows:

- **Device code flow**: The user visits a URL and enters a code to authenticate.
- **Entra authentication**: The user signs in with a supported passwordless
  method, or with their Entra ID password followed by an MFA challenge.
  On success, authd caches the access tokens and user information locally.
  For offline login, it stores only a salted hash of the Entra ID or local
  password. See [Stored secrets](/explanation/security.md#stored-secrets).

The device code flow is enabled by default. If `entra_auth` is omitted, its
default follows `register_device`: it is enabled when device registration is
enabled and disabled otherwise. New Entra broker configurations explicitly set
`entra_auth = false`; enable it only after enabling device registration or
configuring a client secret. Both flows can be individually configured using
the `[flows]` section of the broker configuration file. See
[Configure authentication flows](ref::config-auth-flows) for details.

At least one authentication flow must be enabled. A configuration that
explicitly disables both flows is invalid, and the broker fails to start.

The **Entra authentication** flow has additional requirements for resolving group
membership, depending on whether device registration is enabled. Enabling it
without enabling device registration or configuring a client secret results in
an invalid configuration and the broker fails to start. See
[Group membership resolution with Entra authentication](reference::group-membership-resolution).

### Entra authentication steps

1. **Passwordless probe**: The broker asks Microsoft Entra ID whether the
   account can authenticate without a password.
2. **Passwordless sign-in or password**: If a passwordless method is enrolled,
   such as a FIDO2 security key (for example, a YubiKey), passwordless sign-in
   through the Microsoft Authenticator app, or a Temporary Access Pass, the
   matching challenge is presented directly and no password is entered.
   Otherwise, the user enters their Entra ID password.
3. **MFA challenge**: A password sign-in is followed by an MFA challenge: the
   user approves a push notification or a number-matching prompt, enters a
   time-based one-time password, or touches a FIDO2 security key. A
   passwordless sign-in has no separate MFA step. Security-key challenges,
   including PIN entry when required, are completed locally on the machine.
4. **Local password setup**: When the user authenticated without a password,
   authd asks them to create a local password on first login. Only a salted
   hash of this password is stored for subsequent offline logins.

If a security key challenge is expected but no key is connected, the broker
waits briefly for one to be plugged in. If none appears, login falls back to the
device code flow when it is enabled.

### Compatibility and requirements

The **device code flow** works with all Microsoft Entra ID account types.

The **Entra authentication** flow requires an MFA method enrolled on the account
that is supported by authd. The following account types cannot complete this
flow and fall back to the device code flow if it is enabled, or are denied
otherwise:

- Accounts without an MFA method enrolled
- Federated (on-premises AD FS) accounts

Accounts whose only enrolled MFA method is a FIDO2 security key need a
security key connected to the machine during login. Without one, they follow the
same fallback to the device code flow.

## Keycloak

Keycloak supports the **device code flow**, where the user visits a URL and
enters a code to complete authentication.
