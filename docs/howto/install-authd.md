---
myst:
  html_meta:
    "description lang=en": "Install the authd authentication service and its identity brokers to enable Ubuntu devices to authenticate with multiple cloud identity providers, including Google IAM and Microsoft Entra ID."
---

(howto::install)=
# Install authd and brokers for identity providers

This project consists of two components:
* **authd**: The authentication daemon responsible for managing access to the authentication mechanism.
* **identity broker**: The services that handle the interface with an identity provider. There can be several identity brokers installed and enabled on the system.

authd is delivered as a Debian package for Ubuntu Desktop and Ubuntu Server.

## System requirements

* Ubuntu: Desktop or Server editions
* Release: 24.04 LTS or later
* Architectures: amd64, arm64

## Install authd

On Ubuntu 26.04 LTS, `authd` is available directly from the Ubuntu archive.

:::{admonition} Add PPA before installing on Ubuntu 24.04
:class: note
On Ubuntu 24.04 LTS, `authd` must be installed from the [stable PPA](https://launchpad.net/~ubuntu-enterprise-desktop/+archive/ubuntu/authd). Add the PPA before proceeding:

```shell
sudo add-apt-repository ppa:ubuntu-enterprise-desktop/authd
```
:::

Install authd and any additional Debian packages needed for your system of
choice:

:::::{tab-set}

::::{tab-item} Ubuntu Desktop
:sync: desktop

```shell
sudo apt install authd gnome-shell yaru-theme-gnome-shell
```
::::

::::{tab-item} Ubuntu Server
:sync: server

```shell
sudo apt install authd
```
::::
:::::

## Install brokers

The brokers are provided as snap packages and are available from the Snap Store.
Install the broker corresponding to the identity provider that you want to use:

:::::{tab-set}
:sync-group: broker

::::{tab-item} Google IAM
:sync: google

To install the Google IAM broker, run the following command:

```shell
sudo snap install authd-google
```
At this stage, you have installed the main service and an identity broker to
authenticate against Google IAM.

::::

::::{tab-item} Microsoft Entra ID
:sync: msentraid

To install the Microsoft Entra ID broker, run the following command:

```shell
sudo snap install authd-msentraid
```

At this stage, you have installed the main service and an identity broker to
authenticate against Microsoft Entra ID.

::::

::::{tab-item} Keycloak
:sync: keycloak

Keycloak can be used with the generic OIDC broker. Install the broker with the
following command:

```shell
sudo snap install authd-oidc
```

At this stage, you have installed the main service and an identity broker to
authenticate against Keycloak or any other OIDC provider.

::::

:::::
