---
myst:
  html_meta:
    "description lang=en":
      "authd is an authentication service for Ubuntu, offering integration with multiple cloud identity providers, including Google IAM and Microsoft Entra ID."
---

# authd

authd is an authentication service for Ubuntu that integrates with multiple
cloud identity providers. It offers a secure interface for system
authentication, enabling cloud-based identity management for Ubuntu Desktop and
Server.

authd has a modular design, comprising an authentication daemon and various
identity brokers. This enables authd to support a growing list of identity 
providers. Currently, authd supports authentication with both [MS
Entra ID](https://learn.microsoft.com/en-us/entra/fundamentals/whatis) and
[Google IAM](https://cloud.google.com/iam/docs/overview). An example broker is
also provided to help developers create new brokers for additional identity
services.

If an organization is pursuing cloud-based authentication of Ubuntu
workstations and servers, authd is a secure and versatile service to support a
full transition to the cloud.

## Supported identity providers

::::{tab-set}
:sync-group: broker

:::{tab-item} Google IAM
:sync: google

* <a href="howto/install-authd/?broker=google">Install authd and the Google IAM broker</a>
* <a href="howto/configure-authd/?broker=google">Configure the Google IAM broker</a>
:::

:::{tab-item} Microsoft Entra ID
:sync: msentraid

* <a href="howto/install-authd/?broker=msentraid">Install authd and the Microsoft Entra ID broker</a>
* <a href="howto/configure-authd/?broker=msentraid">Configure the Microsoft Entra ID broker</a>
:::
:::{tab-item} Keycloak
:sync: keycloak

* <a href="howto/install-authd/?broker=keycloak">Install authd and the generic OIDC broker</a>
* <a href="howto/configure-authd/?broker=keycloak">Configure the generic OIDC broker for Keycloak</a>
:::

:::::

## In this documentation

* **Setup**: [Installing authd](/howto/install-authd/) • [Configuring authd](/howto/configure-authd/) • [Changing authd versions](/howto/changing-versions/)
* **User login**: [Logging in with GDM](/howto/login-gdm/) • [Logging in with SSH](/howto/login-ssh/)
* **Deployment**: [Deploying with Landscape](/reference/landscape-deploy/) • [Deploying with cloud-init](/reference/cloud-init-deploy/)
* **Network file systems**: [Using with NFS](/howto/use-with-nfs/) • [Using with Samba](/howto/use-with-samba/)
* **authd design**: [Architecture](/explanation/authd-architecture/) • [Security overview](/explanation/security/)
* **Troubleshooting**: [Accessing logs](/howto/logging/) • [Entering recovery mode on failed login](/howto/enter-recovery-mode/)
* **Documentation**: [How this documentation is structured](/explanation/structure-of-authd-documentation)

## Project and community

authd is a member of the Ubuntu family. It’s an open source project that warmly welcomes community projects, contributions, suggestions, fixes and constructive feedback.

* [Code of conduct](https://ubuntu.com/community/ethos/code-of-conduct)
* [Contribute](/howto/contributing)

Thinking about using authd for your next project? Get in touch!

```{toctree}
:hidden:
:maxdepth: 2

authd <self>
How-to guides </howto/index>
Reference </reference/index>
Explanation </explanation/index>
```
