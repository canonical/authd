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
The `msentraid` broker supports local and remote groups. The generic OIDC broker
(`authd-oidc`) supports only local groups, which it reads from a claim of the ID
token. See [Generic OIDC broker](reference::group-management-oidc).
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
### Group membership resolution with Entra authentication

Group membership is read from the Microsoft Graph API. The access token obtained
from the **Entra authentication** flow does not carry the `GroupMember.Read.All`
scope, so the groups are resolved in one of two ways:

- **With device registration** (`register_device = true`): the device's primary
  refresh token is exchanged for a Graph-scoped access token. No extra
  configuration is required.
- **Without device registration** (`register_device = false`) for the
  **Entra authentication** flow: a `client_secret` must be configured in the
  `[oidc]` section. authd then uses the OIDC app's client credentials to obtain
  an application-level Graph token. This requires the app registration to hold
  the `GroupMember.Read.All` **Application** permission with tenant admin
  consent.

If neither device registration nor a client secret is available while the
**Entra authentication** flow is enabled, the broker fails to start because
group membership cannot be resolved.

(reference::group-management-oidc)=
## Generic OIDC broker

The generic OIDC broker (`authd-oidc`) can read the groups of a user from a
claim of the ID token. This is off by default. To turn it on, set
`allowed_provider_groups` in the `users` section of the broker configuration
file. The `groups_claim` option in the `oidc` section sets the name of the
claim. The default is `groups`. It must be a top-level claim that holds a group
name or a list of group names.

A group in the claim is only used if its name starts with `linux-` and the rest
of the name is in `allowed_provider_groups`. The broker then adds the user to
the local group named after the rest of the name. For example, with
`allowed_provider_groups = sudo`, a user with the group `linux-sudo` in the
claim is added to the local `sudo` group. All other groups in the claim are
ignored: this broker does not create remote groups. The broker compares names
in lowercase, so the local groups must have lowercase names.

The local groups must exist before the users log in. A group that does not
exist is skipped without an error. If it is created later, users are only added
to it when their user info changes, for example when their groups in the claim
change.

The broker reads the groups each time a user logs in through authd with a
working network connection. If the claim is missing from the ID token, or holds
anything other than names, the user is removed from these groups. This does not
apply to a group that is also in `extra_groups`, or in `owner_extra_groups` for
the owner. If the identity provider does not return an ID token when the broker
refreshes the token, the groups are not updated. Without a network connection,
the groups of the last login with a network connection are kept.

Logins that do not go through authd, like SSH logins with a public key, do not
update the groups. A user is only removed from a group at their next login
through authd with a working network connection.

```{warning}
Anyone who can change what the groups claim holds for a user (with Keycloak, the
realm roles of the user) can then change the local groups of that user,
including **sudo** rights. Only allow groups that are safe to manage from the
identity provider.
```

### Keycloak

The built-in `microprofile-jwt` client scope of Keycloak adds a `groups` claim
to the ID token. The claim holds the realm roles of the user that are in the
role scope of the client, including the roles that the user gets through a
Keycloak group and composite roles. When {guilabel}`Full scope allowed` is on
for the client, as it is by default for a new client, all realm roles of the
user are in that scope. Keycloak 26.8 deprecates this option, and its upgrade
notes advise turning it off. If it is off, only the realm roles in the role
scope of the client reach the claim.

To add users to the local `sudo` group based on the realm role `linux-sudo`:

1. Create the realm role `linux-sudo` and give it to the users, directly or
   through a Keycloak group.
2. Check that the `microprofile-jwt` client scope is assigned to the client. In
   a new realm, Keycloak assigns it to new clients as an optional client scope.
3. If {guilabel}`Full scope allowed` is off for the client, add the realm role
   `linux-sudo` to the role scope of the client.
4. Request the scope and allow the group in the broker configuration file.
   `extra_scopes` is a comma-separated list, so add the scope to any scopes that
   you already have, for example
   `extra_scopes = offline_access,microprofile-jwt`:

   ```ini
   [oidc]
   ...
   extra_scopes = microprofile-jwt

   [users]
   ...
   allowed_provider_groups = sudo
   ```

If `microprofile-jwt` is in `extra_scopes` but the client scope is not assigned
to the client, Keycloak refuses to start the device code flow with an
`invalid_scope` error, so users cannot log in with a device code. Users who
logged in before can still log in with their local password, but they are
removed from the groups at their next login with a network connection.
