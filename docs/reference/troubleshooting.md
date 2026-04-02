---
myst:
  html_meta:
    "description lang=en": "Troubleshoot issues with authd when authenticating Ubuntu devices with cloud identity providers like Google IAM and Microsoft Entra ID."
---

# Troubleshooting

This page includes links to authd documentation that may be helpful when
troubleshooting.

## Logging

Logs are generated for authd and its brokers, which can be useful when
troubleshooting and reporting bugs.

To learn how to get logs and configure logging behavior, read the dedicated
[guide on logging](ref::logging).

## Changing versions

To test new features or check if a bug has been fixed in a new version,
you can switch to edge releases for authd and its brokers.

This is described in the [guide on changing
versions](ref::changing-versions).

## Only the first logged-in user can get access

This is the expected behavior, as the first logged-in user becomes the owner
and only the owner has access by default.

To change access you can make the next logged-in user the owner or add more
allowed users.

Guidelines on [configuring allowed users](ref::config-allowed-users) are
outlined in the [configuring authd guide](ref::config).

## File ownership on shared network resources (NFS, Samba)

The user identifiers (UIDs) and group identifiers (GIDs) assigned by authd are
unique to each machine. This means that when using authd with NFS or Samba, the
UIDs and GIDs of users and groups on the server will not match those on the
client machines, which leads to permission issues.

To avoid these issues, you can use ID mapping. For more information, see the
dedicated guides on NFS and Samba:

* [Using authd with NFS](../howto/use-with-nfs)
* [Using authd with Samba](../howto/use-with-samba)

## Recovery mode for failed login

If authd and/or the broker are missing, corrupted, or broken in any way, a user may
be prevented from logging in.

When this occurs, you can [boot into recovery mode](../howto/enter-recovery-mode.md) to
access to the system for modifying configurations and installations.
