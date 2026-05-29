| Title | Entra ID: Support `unix_uid` and `unix_gid` directory extension attributes |  |  |

# Abstract

Allow authd users and groups to use stable, administrator-assigned UIDs and GIDs that are stored in Microsoft Entra ID as directory extension attributes, so that the same user gets the same UID/GID on every machine managed by authd.

# Rationale

Today authd generates UIDs and GIDs within a configured range (default 10 000 – 60 000) on each machine independently. The same user logging into two different machines receives different UIDs, which causes permission mismatches on shared filesystems (NFS, Samba – although we document workarounds for those), complicates central auditing, and breaks assumptions of tools that correlate users by numeric ID.

Enterprises migrating from on-premises Active Directory with POSIX attributes, or organisations that maintain a central UID/GID registry, need a way to push those IDs into the identity provider and have authd honour them.

The mechanism for storing and retrieving custom attributes is IdP-specific. This specification covers the **Microsoft Entra ID** case using **directory extension properties** registered on the authd application.

# Specification

## Attribute storage: directory extensions

Among the custom-data options Entra ID offers (on-premises extension attributes, directory extensions, schema extensions, open extensions, custom security attributes), **directory extensions** are the best fit because they:

* can target both `User` and `Group` objects,
* use a strongly-typed `Integer` (signed 32-bit) data type that maps directly to a Unix UID/GID,
* are accessible via the Microsoft Graph API with the existing `User.Read` and `GroupMember.Read.All` delegated permissions (no additional permissions required),
* can be exposed as optional claims in the ID token (format `extn.<attributename>`), which avoids an extra Graph API call for the user UID,
* are scoped to the owning application, avoiding conflicts with extensions from other apps.

### Defining the extensions

The administrator registers two directory extension properties on the authd Entra ID app registration:

```http
POST https://graph.microsoft.com/v1.0/applications/{appObjectId}/extensionProperties

{
    "name": "unix_uid",
    "dataType": "Integer",
    "targetObjects": ["User"]
}
```

```http
POST https://graph.microsoft.com/v1.0/applications/{appObjectId}/extensionProperties

{
    "name": "unix_gid",
    "dataType": "Integer",
    "targetObjects": ["Group"]
}
```

This creates properties with canonical names of the form:

* `extension_{appClientIdWithoutHyphens}_unix_uid` (on users)
* `extension_{appClientIdWithoutHyphens}_unix_gid` (on groups)

The administrator then sets the values on individual users and groups via the Graph API, PowerShell (`Set-MgUser` / `Set-MgGroup`), or provisioning automation.

### Optional: exposing the UID as an ID-token claim

Administrators can add the user extension as an optional claim in the app registration's token configuration. When configured, the claim appears in the ID token as `extn.unix_uid`. This lets the broker read the UID from the token directly, without an extra Graph API call.

If the claim is not configured (or absent from a particular user's token), the broker falls back to the Graph API.

## Reading the attributes

### User UID

The broker reads the user UID in order of preference:

1. **ID-token claim** — If the token contains `extn.unix_uid` (or the configured claim name), use that value.
2. **Graph API** — Call `GET /me?$select=extension_{appId}_unix_uid` (or the configured attribute name) and read the value from the response.
3. **Absent** — If neither source provides a value, the UID field is left unset and authd falls through to its existing auto-generation logic.

### Group GID

Groups cannot appear in OIDC tokens, so their GIDs are always read via the Graph API. The existing `fetchUserGroups` function queries `client.Me().TransitiveMemberOf().GraphGroup()`. The `$select` parameter of that request is extended to also request the `extension_{appId}_unix_gid` property.

If a group's `unix_gid` value is absent (null), that group's GID is left unset and authd auto-generates one as it does today.

### Graph API permissions

No new permissions are required beyond what authd already requests:

| Permission | Purpose |
|---|---|
| `User.Read` (delegated) | Read directory extensions on `/me` |
| `GroupMember.Read.All` (delegated) | Read directory extensions on groups via `/me/transitiveMemberOf` |

## Broker configuration

A new section in the broker configuration file (`broker.conf`) controls this feature. Example:

```yaml
[msentraid]
# The name of the directory extension attribute that stores the Unix UID
# on User objects.
# Default: empty (feature disabled)
unix_uid_attribute = unix_uid

# The name of the directory extension attribute that stores the Unix GID
# on Group objects.
# Default: empty (feature disabled)
unix_gid_attribute = unix_gid
```

The feature is **disabled by default**. Administrators opt in by setting at least `unix_uid_attribute` or `unix_gid_attribute`.

## Changes to the broker–authd interface

### `info.User` and `info.Group` structs (OIDC broker)

```go
// In authd-oidc-brokers/internal/providers/info/info.go

type User struct {
    Name   string  `json:"name"`
    UUID   string  `json:"uuid"`
    Home   string  `json:"dir"`
    Shell  string  `json:"shell"`
    Gecos  string  `json:"gecos"`
    Groups []Group `json:"groups"`
    UID    *uint32 `json:"uid,omitempty"`     // NEW — broker-provided UID
}

type Group struct {
    Name string  `json:"name"`
    UGID string  `json:"ugid"`
    GID  *uint32 `json:"gid,omitempty"`     // NEW — broker-provided GID
}
```

### `userinfo` JSON payload

The broker includes the `uid` field in the JSON payload returned from `IsAuthenticated` when a UID was obtained from the IdP. Similarly, `gid` is included on each group entry that has a GID from the IdP.

Example payload:

```json
{
  "userinfo": {
    "name": "alice@contoso.com",
    "uuid": "aaaabbbb-1111-cccc-2222-ddddeeee3333",
    "dir": "/home/alice@contoso.com",
    "shell": "/usr/bin/bash",
    "gecos": "Alice Contoso",
    "uid": 50001,
    "groups": [
      {"name": "developers", "ugid": "group-obj-id-1", "gid": 60001},
      {"name": "sudo", "ugid": ""}
    ]
  }
}
```

### `types.UserInfo` and `types.GroupInfo` (authd core)

The existing `UserInfo.UID` field (`uint32`) does not distinguish between "broker provided UID 0" and "no UID provided". To support opt-in semantics, change the field to a pointer:

```go
// In internal/users/types/types.go

type UserInfo struct {
    Name  string
    UID   *uint32   // changed from uint32
    Gecos string
    Dir   string
    Shell string
    Groups []GroupInfo
}

type GroupInfo struct {
    Name string
    GID  *uint32
    UGID string
}
```

**Note:** `GroupInfo.GID` is already a `*uint32`, so only `UserInfo.UID` needs the type change. All call sites that read or write `UID` as a plain `uint32` must be updated to handle the pointer.

## Changes to authd user manager

In `internal/users/manager.go`, the `UpdateUser` function currently ignores `UserInfo.UID` for new users and always auto-generates. The logic becomes:

```
if user already exists in DB:
    keep existing UID (no change from today)
else if pre-auth UID exists:
    use pre-auth UID (no change from today)
else if broker provided a UID (UserInfo.UID != nil):
    validate the UID (see below)
    if valid: use it
    else: log warning, fall through to auto-generation
else:
    auto-generate UID (no change from today)
```

The same pattern applies to group GIDs:

```
if group already exists in DB:
    keep existing GID (no change from today)
else if broker provided a GID (GroupInfo.GID != nil):
    validate the GID (see below)
    if valid: use it
    else: log warning, fall through to auto-generation
else:
    auto-generate GID (no change from today)
```

### Validation of broker-provided IDs

A broker-provided UID or GID must pass the following checks before being used:

1. **Not a reserved ID** — Must not be 0 (root), 65534 (nobody), 65535, or MaxUint32 (same reserved list as `isReservedID`).
2. **Not conflicting with local entries** — Must not be in use in `/etc/passwd` or `/etc/group` (checked via `lockedEntries.IsUniqueUID` / `IsUniqueGID`).
3. **Not conflicting with the authd database** — Must not be assigned to a *different* user/group in the authd database.

A broker-provided UID/GID is **not** restricted to the configured `uid_min`/`uid_max` (`gid_min`/`gid_max`) range. Those limits only apply to auto-generated IDs. This is intentional: an administrator explicitly assigning IDs in the IdP is expected to know what range they want.

If validation fails, the broker-provided ID is rejected with a warning log, and authd falls back to auto-generation for that user or group.

### Behaviour on subsequent logins (UID/GID already stored)

Once a user or group has a UID/GID stored in the authd database, that ID is preserved on subsequent logins — even if the IdP attribute has changed. This prevents breaking file ownership. A warning is logged if the IdP-provided value differs from the stored one.

## Changes to the Entra ID provider

### `msentraid.go` — claims struct

Add an optional field for the UID claim:

```go
type claims struct {
    PreferredUserName string  `json:"preferred_username"`
    Sub               string  `json:"sub"`
    Home              string  `json:"home"`
    Shell             string  `json:"shell"`
    Gecos             string  `json:"name"`
    UnixUID           *int32  `json:"extn.unix_uid,omitempty"`
}
```

The claim name (`extn.unix_uid`) should be read from the broker configuration (the `unix_uid_claim` setting) to support custom names.

### `msentraid.go` — `GetUserInfo`

If the claims contain a non-nil `UnixUID`, convert it to `uint32` and set it on the returned `info.User.UID`. If the claim is absent and a `unix_uid_attribute` is configured, the broker makes a Graph API call to `/me?$select=<attribute>` to retrieve it.

### `msentraid.go` — `fetchUserGroups`

Extend the Graph API request for groups to also `$select` the `unix_gid` directory extension attribute. When iterating the returned groups, read the extension property from each group's additional data and set `info.Group.GID` accordingly.

### `providers.go` — `Provider` interface

The `GetUserInfo` and `GetGroups` methods should not need signature changes, since the UID/GID are carried in the existing `info.User` and `info.Group` return types.

## Example broker

The example broker should be updated to optionally include `uid` and `gid` fields in the `userinfo` JSON for specific test users, to enable testing the full flow in integration tests.

## Documentation

The following documentation should be created or updated:

* **Setup guide**: Step-by-step instructions for administrators to create the directory extension properties, assign values, and optionally configure the ID-token claim.
* **Broker configuration reference**: Document the new `unix_uid_attribute`, `unix_uid_claim`, and `unix_gid_attribute` configuration keys.
* **Troubleshooting**: Common issues (attribute not found, ID conflicts, value format errors) and how to diagnose them.

# Further Information

## Alternatives considered

### On-premises extension attributes (`extensionAttribute1`–`extensionAttribute15`)

These are the 15 predefined `onPremisesExtensionAttributes` on User objects. They were rejected because:

* They have generic names (`extensionAttribute1`), making it hard to know which one holds the UID without external documentation.
* They only support `String` values, requiring string-to-integer conversion with additional validation.
* They are **not available on Group objects**, so they cannot be used for GIDs.
* They are designed primarily for on-premises AD sync scenarios.

### Custom security attributes

These offer fine-grained RBAC and audit, but were rejected because:

* They require Entra ID P1 or P2 licensing.
* They require the `CustomSecAttributeAssignment.Read.All` permission, which is a privileged permission that many organisations are reluctant to grant.
* They are designed for governance and access-control metadata, not for operational attributes like UIDs.

### Schema extensions

These are conceptually similar to directory extensions but were rejected because:

* They use a complex-type wrapper (e.g., `contoso_unixAttributes/uid`) rather than a flat property, making the `$select` and JSON parsing slightly more involved.
* They have a lifecycle management model (InDevelopment → Available) that adds unnecessary complexity.
* They cannot be emitted as optional claims in OIDC tokens.

## Related issues

* Consistent UIDs/GIDs across machines — the general cross-IdP tracking issue.
* Support for other IdPs (Google IAM, generic OIDC) will be handled in separate specifications with the attribute storage mechanism appropriate for each provider.

## References

* [Microsoft Graph extensibility overview](https://learn.microsoft.com/en-us/graph/extensibility-overview)
* [Create directory extension property](https://learn.microsoft.com/en-us/graph/api/application-post-extensionproperty)
* [Directory extension optional claims](https://learn.microsoft.com/en-us/entra/identity-platform/optional-claims#directory-extension-optional-claims)
* [systemd UID/GID ranges](https://systemd.io/UIDS-GIDS/)
