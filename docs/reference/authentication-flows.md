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
  password. See [Stored secrets](ref::stored-secrets) and
  [Cached Entra ID passwords](ref::cached-entra-passwords).

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
   matching challenge is offered. For passwordless FIDO2 challenges, Entra ID
   password entry is also available. If no passwordless method is enrolled, the
   user enters their Entra ID password.
3. **MFA challenge**: A password sign-in is followed by an MFA challenge: the
   user approves a push notification or a number-matching prompt, enters a
   time-based one-time password, or touches a FIDO2 security key. A
   passwordless sign-in has no separate MFA step. Security-key challenges,
   including PIN entry when required, are completed locally on the machine.
4. **Local password setup**: When the user authenticated without a password,
   authd asks them to create a local password on first login. Only a salted
   hash of this password is stored for subsequent offline logins.

A FIDO2 credential registered with Entra ID does not always live on a security
key that this computer can reach. A passkey synced to a phone, a browser
profile or the Microsoft Authenticator app is one example. Entra ID sends the
same security key challenge in both cases, so the broker cannot treat the
challenge as proof that a local ceremony can succeed. It offers the Entra ID
password beside the security-key step instead.

- With a key connected, the security-key step is offered first and the Entra
  ID password is listed next. A key that needs a PIN collects it first.
- With no key connected, the Entra ID password is offered first and the
  security-key step stays selectable. Selecting it waits up to 60 seconds for
  a key to be connected, then falls back.
- Once the Entra ID password has been accepted, the security key is a second
  factor and no longer has an alternative. A challenge that cannot be
  completed then uses the device code flow when it is enabled, or denies the
  login.

### Compatibility and requirements

The **device code flow** works with all Microsoft Entra ID account types.

The **Entra authentication** flow requires an MFA method enrolled on the account
that is supported by authd. The following account types cannot complete this
flow and fall back to the device code flow if it is enabled, or are denied
otherwise:

- Accounts without an MFA method enrolled
- Federated (on-premises AD FS) accounts

Falling back to the Entra ID password assumes the tenant allows password
sign-in for the account. Accounts that only allow passwordless FIDO sign-in
need a security key that holds the account's credential connected to the
machine, or must use the device code flow.

## Keycloak

Keycloak supports the **device code flow**, where the user visits a URL and
enters a code to complete authentication.
