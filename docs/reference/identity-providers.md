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
