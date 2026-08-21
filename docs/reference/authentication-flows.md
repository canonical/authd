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
- **Entra authentication**: The user authenticates directly with Microsoft Entra ID,
  using their password or a supported passwordless method, followed by an MFA
  challenge if required. On success, authd caches the credentials locally for
  subsequent logins.

The device code flow is enabled by default. If `entra_auth` is omitted, its
default follows `register_device`: it is enabled when device registration is
enabled and disabled otherwise. New Entra broker configurations explicitly set
`entra_auth = false`; enable it only after configuring device registration or
a client secret. Both flows can be individually configured using the `[flows]`
section of the broker configuration file. See [Configure authentication
flows](ref::config-auth-flows) for details.

At least one authentication flow must remain enabled.

The **Entra authentication** flow has additional requirements for resolving group
membership, depending on whether device registration is enabled. See
[Group membership resolution with Entra authentication](reference::group-membership-resolution).

### Compatibility and requirements

The **device code flow** works with all Microsoft Entra ID account types.

The **Entra authentication** flow requires an MFA method enrolled on the account
that is supported by authd. The following account types cannot complete this
flow and fall back to the device code flow if it is enabled, or are denied
otherwise:

- Accounts without an MFA method enrolled
- Accounts whose only MFA method is a FIDO2/passkey credential
- Federated (on-premises AD FS) accounts

## Keycloak

Keycloak supports the **device code flow**, where the user visits a URL and
enters a code to complete authentication.
