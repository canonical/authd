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

(reference::group-membership-resolution)=
### Group membership resolution with Entra password + MFA

Group membership is read from the Microsoft Graph API. The token obtained from
the **Entra password + MFA** flow is issued by the Microsoft Broker App and does
not carry the `GroupMember.Read.All` scope, so the groups are resolved in one of
two ways:

- **With device registration** (`register_device = true`): the device's primary
  refresh token is exchanged for a Graph-scoped access token. No extra
  configuration is required.
- **Without device registration** (`register_device = false`): a `client_secret`
  must be configured in the `[oidc]` section. authd then uses the OIDC app's
  client credentials to obtain an application-level Graph token. This requires
  the app registration to hold the `GroupMember.Read.All` **Application**
  permission with tenant admin consent.

If neither device registration nor a client secret is available, the
**Entra password + MFA** flow is disabled at startup, because group membership
could not be resolved.
