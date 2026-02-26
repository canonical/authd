---
myst:
  html_meta:
    "description lang=en": "Change to the edge release of authd to test new features and bug fixes."
---

(ref::changing-versions)=
# Changing authd versions

To test new features or check if a bug has been fixed in a new version,
you can switch to edge releases of authd and its brokers.

```{warning}
Do not use the edge PPA in a production system, because it may apply changes to
the authd database in a non-reversible way, which can make it difficult to roll
back to the stable version of authd.
```

## Switch authd to the edge PPA

The [edge
PPA](https://launchpad.net/~ubuntu-enterprise-desktop/+archive/ubuntu/authd-edge) contains
the latest fixes and features for authd, in addition to its GNOME Shell (GDM)
counterpart.

```shell
sudo add-apt-repository ppa:ubuntu-enterprise-desktop/authd-edge
sudo apt install authd gnome-shell
```

Keep in mind that this version is not tested and may be incompatible with the current released version of the brokers.

To switch back to the stable version of authd:

```shell
sudo apt install ppa-purge
sudo ppa-purge ppa:ubuntu-enterprise-desktop/authd-edge
```

## Switch broker snap to the edge channel

You can also switch to the edge channel of the broker snap:

::::{tab-set}
:sync-group: broker

:::{tab-item} Google IAM
:sync: google

```shell
sudo snap switch authd-google --edge
sudo snap refresh authd-google
```
:::

:::{tab-item} Microsoft Entra ID
:sync: msentraid

```shell
sudo snap switch authd-msentraid --edge
sudo snap refresh authd-msentraid
```
:::
::::

Keep in mind that this version is not tested and may be incompatible with the current released version of authd.

To switch back to stable after trying the edge channel:

::::{tab-set}
:sync-group: broker

:::{tab-item} Google IAM
:sync: google

```shell
sudo snap switch authd-google --stable
sudo snap refresh authd-google
```
:::

:::{tab-item} Microsoft Entra ID
:sync: msentraid

```shell
sudo snap switch authd-msentraid --stable
sudo snap refresh authd-msentraid
```
:::
::::

```{note}
If using an edge release, you can read the
[edge version of the documentation](https://documentation.ubuntu.com/authd/edge-docs/)
```
