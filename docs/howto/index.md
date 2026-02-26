---
myst:
  html_meta:
    "description lang=en":
      "authd how-to guides covering installation, configuration, user login, network file systems, and contributing."
---

(howtos)=

# How-to guides

These guides walk you through key operations you can perform with authd.

## Installation and configuration

Installation of the authd daemon and an identity broker is required to support
authentication of Ubuntu devices, with various options available for
configuring authentication behavior and user management:

```{toctree}
:titlesonly:

Installing authd <install-authd>
Configuring authd <configure-authd>
```

## Login and authentication

Users can log in and authenticate on Ubuntu Desktop or Ubuntu Server, with
authd supporting both GDM and SSH:

```{toctree}
:titlesonly:

Logging in with GDM <login-gdm>
Logging in with SSH <login-ssh>
```

## Network file systems

If using a network file system to access shared directories from
authd-enabled machines, you can use ID mapping:

```{toctree}
:titlesonly:

Using authd with NFS <use-with-nfs>
Using authd with Samba <use-with-samba>
```

## Debugging and troubleshooting

When troubleshooting authd, you may need to work with logs or enter recovery
mode:

```{toctree}
:titlesonly:

Accessing and configuring logs <logging>
Entering recovery mode on failed login <enter-recovery-mode>
```

## Updating and upgrading

Use the stable version of authd for production use, or switch to the edge
version to try new features:

```{toctree}
:titlesonly:

Changing authd versions <changing-versions>
```

## Contributing to authd

Contribute to the development of authd and its brokers, in addition to the
authd documentation:

```{toctree}
:titlesonly:

Contributing to authd <contributing>
```
