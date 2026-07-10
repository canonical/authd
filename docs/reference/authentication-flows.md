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
- **Entra password + MFA**: The user authenticates with their Entra ID password,
  followed by a multi-factor authentication (MFA) challenge. On success, authd
  reuses the same Entra password as the local cached password for subsequent
  logins.

Both flows are enabled by default and can be individually configured using the
`[flows]` section of the broker configuration file. See
[Configure authentication flows](ref::config-auth-flows) for details.

The **Entra password + MFA** flow has additional requirements for resolving group
membership, depending on whether device registration is enabled. See
[Group membership resolution with Entra password + MFA](reference::group-membership-resolution).

### Compatibility and requirements

The **device code flow** works with all Microsoft Entra ID account types.

The **Entra password + MFA** flow requires an MFA method enrolled on the account
that is supported by authd. The following account types cannot complete this
flow and fall back to the device code flow if it is enabled, or are denied
otherwise:

- Accounts without an MFA method enrolled
- Accounts whose only MFA method is a FIDO2/passkey credential
- Federated (on-premises AD FS) accounts

## Keycloak

Keycloak supports the **device code flow**, where the user visits a URL and
enters a code to complete authentication.
