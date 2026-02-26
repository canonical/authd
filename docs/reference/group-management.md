---
myst:
  html_meta:
    "description lang=en": "Management of groups using authd with Microsoft Entra ID."
---

(reference::group-management)=
# Group management

Groups are used to manage users that all need the same access and permissions to resources.
Groups from the remote provider can be mapped into local Linux groups for the user.

In addition, you can configure extra groups
[in the broker configuration file](ref::config-user-groups).

```{note}
  Groups are currently supported for the `msentraid` broker.
```

## Microsoft Entra ID

Microsoft Entra ID supports creating groups and adding users to them.

> See [Manage Microsoft Entra groups and group membership](https://learn.microsoft.com/en-us/entra/fundamentals/how-to-manage-groups)

For example the user `authd test`, is a member of the Entra ID groups `Azure_OIDC_Test` and `linux-sudo`:

![Azure portal interface showing the Azure groups.](../assets/entraid-groups.png)

This translates to the following unix groups on the local machine:

```shell
~$ groups
aadtest-testauthd@uaadtest.onmicrosoft.com sudo azure_oidc_test
```

There are three types of groups:
1. **Primary group**: Created automatically based on the user name
1. **Local group**: Group local to the machine prefixed with `linux-`. For instance if the user is a member of the Azure group `linux-sudo`, they will be a member of the `sudo` group locally.
1. **Remote group**: All the other Azure groups the user is a member of.
