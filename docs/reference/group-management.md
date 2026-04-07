---
myst:
  html_meta:
    "description lang=en": "Management of groups, and privileges like sudo and docker, using authd."
---

(reference::group-management)=
# Group and privilege management

Groups are used to manage users that all need the same access and permissions to resources.
For example, you can manage **sudo** and **docker** rights of users based on group membership.

Groups from the identity provider can be mapped into local Linux groups for the user.
You can also configure extra groups in the broker configuration file,
as described in the [configuration guide](ref::config-user-groups).

```{admonition} Broker support for group management
:class: important
Groups are currently supported for the `msentraid` broker.
```

## Microsoft Entra ID

Microsoft Entra ID supports creating groups and adding users to them.

> See [Manage Microsoft Entra groups and group membership](https://learn.microsoft.com/en-us/entra/fundamentals/how-to-manage-groups)

For example, the user `authd test` is a member of the Entra ID groups `Azure_OIDC_Test` and `linux-sudo`:

![Azure portal interface showing the Azure groups.](../assets/entraid-groups.png)

This translates to the following Linux groups on the local machine:

```shell
~$ groups
aadtest-testauthd@uaadtest.onmicrosoft.com sudo azure_oidc_test
```

There are three types of groups:
1. **Primary group**: Created automatically based on the user name
1. **Local group**: Group local to the machine prefixed with `linux-`. For example, if the user is a member of the Azure group `linux-sudo`, they will be a member of the `sudo` group locally.
1. **Remote group**: All the other Azure groups the user is a member of.
