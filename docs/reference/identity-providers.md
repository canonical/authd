---
myst:
  html_meta:
    "description lang=en": "Identity providers supported by authd."
---

# Identity providers that authd supports

authd supports identity providers through its identity brokers.
Each broker is available as a snap.
Several brokers can be installed and enabled on a system.

| Provider       | Broker snap                                             | Install as a snap              | Configure                                                                            | Provider docs                                                            |
| ---            |---------------------------------------------------------|--------------------------------|--------------------------------------------------------------------------------------|--------------------------------------------------------------------------|
| Google IAM     | [authd-google](https://snapcraft.io/authd-google)       | `snap install authd-google`    | <a href="../../howto/configure-authd/?broker=google">Google IAM guide</a>            | [Google](https://cloud.google.com/iam/docs/overview)                     |
| Microsoft Entra ID    | [authd-msentraid](https://snapcraft.io/authd-msentraid) | `snap install authd-msentraid` | <a href="../../howto/configure-authd/?broker=msentraid">Microsoft Entra ID guide</a> | [Microsoft](https://learn.microsoft.com/en-us/entra/fundamentals/whatis) |
| Keycloak | [authd-oidc](https://snapcraft.io/authd-oidc)           | `snap install authd-oidc`      | <a href="../../howto/configure-authd/?broker=keycloak">Keycloak guide</a>            | [Keycloak](https://www.keycloak.org/documentation)  |


```{note}
Support for multiple additional providers is planned for future releases of authd.
```

## Authentication methods

### Google IAM

Google IAM supports device code authentication, where the user visits a URL
and enters a code to complete authentication.

### Microsoft Entra ID

Microsoft Entra ID supports the following authentication methods:

- **Device code authentication**: The user visits a URL and enters a code to
  authenticate. This is the traditional flow and works with all account types.
- **Entra password + MFA**: The user authenticates directly with their Entra ID
  password, followed by a multi-factor authentication (MFA) challenge. On
  success, authd reuses the same Entra password as the local cached password for
  subsequent logins.

Both methods are enabled by default and can be individually controlled via the
`[flows]` section of the broker configuration file. See
[Configure authentication flows](ref::config-auth-flows) for details.

The **Entra password + MFA** flow has additional requirements for resolving group
membership, depending on whether device registration is enabled. See
[Group membership resolution with Entra password + MFA](reference::group-membership-resolution).

### Keycloak

Keycloak supports device code authentication, where the user visits a URL and
enters a code to complete authentication.
