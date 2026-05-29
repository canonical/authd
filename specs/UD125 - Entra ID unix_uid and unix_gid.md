| Title | Entra ID: Support `unix_uid` and `unix_gid` directory extension attributes |  |  |

# Abstract

Allow authd users and groups to use stable, administrator-assigned UIDs and GIDs that are stored in Microsoft Entra ID as directory extension attributes, achieving consistent IDs across all machines managed by authd.

# Rationale

Today authd generates UIDs and GIDs within a configured range (default 10 000 – 60 000) on each machine independently. The same user logging into two different machines receives different UIDs, which causes permission mismatches on shared filesystems (NFS, Samba – although we document workarounds for those), complicates central auditing, and breaks assumptions of tools that correlate users by numeric ID.

Enterprises migrating from on-premises Active Directory with POSIX attributes, or organisations that maintain a central UID/GID registry, need a way to push those IDs into the identity provider and have authd honour them.

The mechanism for storing and retrieving custom attributes is IdP-specific. This specification covers the **Microsoft Entra ID** case using **directory extension properties** registered on the authd application.

# Specification

The feature is delivered in two iterations:

1. **Iteration 1** — The broker fetches `unix_uid` / `unix_gid` from Entra ID and caches them in `token.json`. Administrators can apply them using `authctl user set-uid` / `authctl group set-gid` (manually or via scripting).
2. **Iteration 2** — The broker passes the IdP-provided IDs to authd through the `userinfo` payload. authd uses them automatically when creating users and groups. On first SSH login (where the pre-auth UID was auto-generated), the PAM module terminates the session with an informational message, and the user must reconnect.

Both iterations share the same Entra ID attribute storage, Graph API integration, and broker configuration. The difference is only in how the IDs are applied to the system.

## Attribute storage: directory extensions

Among the custom-data options Entra ID offers (on-premises extension attributes, directory extensions, schema extensions, open extensions, custom security attributes), **directory extensions** are the best fit because they:

* can target both `User` and `Group` objects,
* use a strongly-typed `Integer` (signed 32-bit) data type that maps directly to a Unix UID/GID,
* are accessible via the Microsoft Graph API with the existing `User.Read` and `GroupMember.Read.All` delegated permissions (no additional permissions required),
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

## Reading the attributes (broker side)

### User UID

When `unix_uid_attribute` is configured, the broker calls `GET /me?$select=extension_{appId}_{unix_uid_attribute}` via the Graph API and reads the integer value from the response. If the attribute is absent or null for a particular user, the UID field is left unset.

### Group GID

When `unix_gid_attribute` is configured, the existing `fetchUserGroups` function extends the `$select` parameter of the `client.Me().TransitiveMemberOf().GraphGroup()` request to also include `extension_{appId}_{unix_gid_attribute}`. When iterating the returned groups, the extension property value is read from each group's additional data and set as `info.Group.GID`. If a group's attribute is absent or null, that group's GID is left unset.

### Graph API permissions

No new permissions are required beyond what authd already requests:

| Permission | Purpose |
|---|---|
| `User.Read` (delegated) | Read directory extensions on `/me` |
| `GroupMember.Read.All` (delegated) | Read directory extensions on groups via `/me/transitiveMemberOf` |

## Broker configuration

New configuration keys in the `[msentraid]` section of the broker configuration file (`broker.conf`) control this feature:

```ini
[msentraid]
...

# The name of the directory extension attribute that stores the Unix UID
# on User objects.
# Default: empty (feature disabled)
unix_uid_attribute = unix_uid

# The name of the directory extension attribute that stores the Unix GID
# on Group objects.
# Default: empty (feature disabled)
unix_gid_attribute = unix_gid
```

The feature is **disabled by default**. Administrators opt in by setting at least `unix_uid_attribute` or `unix_gid_attribute`. The values shown above match the recommended attribute names from the [Defining the extensions](#defining-the-extensions) section and are the expected defaults for most deployments.

## Changes to the broker

### `info.User` and `info.Group` structs

```go
// In authd-oidc-brokers/internal/providers/info/info.go

type User struct {
    Name   string  `json:"name"`
    UUID   string  `json:"uuid"`
    Home   string  `json:"dir"`
    Shell  string  `json:"shell"`
    Gecos  string  `json:"gecos"`
    Groups []Group `json:"groups"`
    UID    *uint32 `json:"uid,omitempty"`     // NEW — IdP-provided UID
}

type Group struct {
    Name string  `json:"name"`
    UGID string  `json:"ugid"`
    GID  *uint32 `json:"gid,omitempty"`     // NEW — IdP-provided GID
}
```

These fields are populated by the provider when reading the directory extensions and stored in `token.json` as part of `AuthCachedInfo.UserInfo`.

### `msentraid.go` — `GetUserInfo`

When `unix_uid_attribute` is configured, the broker makes a Graph API call to `GET /me?$select=extension_{appId}_{unix_uid_attribute}` after obtaining the token. If the attribute is present and holds a valid non-zero integer, it is converted to `uint32` and set on the returned `info.User.UID`.

### `msentraid.go` — `fetchUserGroups`

When `unix_gid_attribute` is configured, the full attribute name (`extension_{appId}_{unix_gid_attribute}`) is added to the `$select` parameter of the groups query. When iterating the returned groups, the extension property value is read and set as `info.Group.GID`.

### Token refresh

On token refresh, the broker re-fetches user info and groups. The UID and GID fields are populated in the refreshed `UserInfo` and written to `token.json`, so the latest IdP-provided IDs are always cached.

### `providers.go` — `Provider` interface

The `GetUserInfo` and `GetGroups` methods do not need signature changes, since the UID/GID are carried in the existing `info.User` and `info.Group` return types.

---

## Iteration 1: Manual/scripted ID application via `authctl`

### Caching in `token.json`

The broker already caches a `UserInfo` struct in `token.json` (at `$DATA_DIR/$ISSUER/$USERNAME/token.json`, where `$DATA_DIR` defaults to `/var/lib/authd-oidc`). With the new `UID` and `GID` fields on `info.User` and `info.Group`, the IdP-provided IDs are automatically persisted there after every authentication or token refresh.

Administrators can read these values and apply them using `authctl user set-uid` / `authctl group set-gid`. The `token.json` format is documented so that this can be scripted or integrated into site-specific provisioning tooling.

---

## Iteration 2: Automatic ID application during login

In this iteration, the broker passes the IdP-provided IDs to authd through the `userinfo` JSON payload, and authd uses them directly when creating or updating users and groups. This provides the best user experience: the correct UID/GID is applied from the first login onward (except for the first SSH session — see below).

### `userinfo` JSON payload

The broker includes the `uid` field in the JSON payload returned from `IsAuthenticated` when a UID was obtained from the IdP. Similarly, `gid` is included on each group entry.

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

### Changes to `types.UserInfo` (authd core)

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
```

**Note:** `GroupInfo.GID` is already a `*uint32`, so only `UserInfo.UID` needs the type change. All call sites that read or write `UID` as a plain `uint32` must be updated to handle the pointer.

### Changes to `UpdateUser` (authd user manager)

In `internal/users/manager.go`, the `UpdateUser` function logic becomes:

```
if user already exists in DB:
    keep existing UID (no change from today)
else if pre-auth UID exists AND broker provided a different UID:
    this is first SSH login with IdP UID mismatch → signal error (see SSH handling below)
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

#### Validation of broker-provided IDs

A broker-provided UID or GID must pass the following checks:

1. **Not a reserved ID** — Must not be 0 (root), 65534 (nobody), 65535, or MaxUint32 (same reserved list as `isReservedID`).
2. **Not conflicting with local entries** — Must not be in use in `/etc/passwd` or `/etc/group` (checked via `lockedEntries.IsUniqueUID` / `IsUniqueGID`).
3. **Not conflicting with the authd database** — Must not be assigned to a *different* user/group in the authd database.

A broker-provided UID/GID is **not** restricted to the configured `uid_min`/`uid_max` range. Those limits only apply to auto-generated IDs.

If validation fails, the broker-provided ID is rejected with a warning log, and authd falls back to auto-generation.

### SSH pre-auth handling: first-login disconnect

When a user logs in via SSH for the first time and the IdP provides a UID, the following sequence occurs:

1. sshd calls NSS → `RegisterUserPreAuth` generates a temporary UID (e.g. 10001).
2. sshd uses UID 10001 for the session.
3. PAM authentication succeeds → broker returns `userinfo` with IdP UID 50001.
4. authd's `UpdateUser` detects: pre-auth UID (10001) exists AND broker-provided UID (50001) differs.
5. authd creates the user with the IdP-provided UID (50001), discarding the pre-auth UID.
6. `UpdateUser` (or `IsAuthenticated` in the PAM service) returns a specific error indicating that the session must be restarted.
7. The PAM module sends an informational message to the user and returns an authentication failure.

The informational message displayed via PAM:

```
Your user account has been created. Please log in again.
```

The PAM module uses `showPamMessage(mTx, pam.TextInfo, msg)` before returning the error. OpenSSH displays `PAM_TEXT_INFO` messages to the client when `UsePAM yes` is set (which is the default).

On the second login, the user exists in the authd database with UID 50001. The SSH pre-auth NSS lookup finds them in the DB and returns the correct UID. No disconnect is needed.

**This disconnect only occurs:**
- On the very first SSH login for a user
- When the feature is enabled (IdP UID attribute is configured)
- When the IdP actually provides a UID for the user
- When the login method is SSH (GDM does not have pre-auth, so the IdP UID is used directly)

#### GDM and other non-SSH login methods

For GDM and other PAM applications that do not perform a pre-auth NSS lookup, there is no pre-auth record. `UpdateUser` takes the `broker provided a UID` branch directly, and the user is created with the correct UID on first login. No disconnect is needed.

### Behaviour on subsequent logins (UID/GID already stored)

Once a user or group has a UID/GID stored in the authd database, that ID is preserved on subsequent logins — even if the IdP attribute has changed. This prevents breaking file ownership. A warning is logged if the IdP-provided value differs from the stored one.

To change the UID/GID after it has been stored, administrators use `authctl user set-uid` / `authctl group set-gid`.

---

## Example broker

The example broker should be updated to optionally include `uid` and `gid` fields in the `userinfo` JSON and `token.json` for specific test users, to enable testing both iterations in integration tests.

## Documentation

The following documentation should be created or updated:

* **Setup guide**: Step-by-step instructions for administrators to:
  1. Create the directory extension properties in Entra ID.
  2. Assign UID/GID values to users and groups.
  3. Configure the broker (`unix_uid_attribute`, `unix_gid_attribute`).
  4. (Iteration 1) Use `authctl user set-uid` / `authctl group set-gid` to apply cached IDs, including guidance on scripting.
* **Broker configuration reference**: Document the new configuration keys.
* **First login behaviour**: Document that the first SSH login with a configured IdP UID will create the user account and require a re-login (iteration 2 only).
* **Troubleshooting**: Common issues (attribute not found, ID conflicts, value format errors, first-login disconnect) and how to diagnose them.

# Further Information

## Alternatives considered

### Fetching UID at pre-auth time via application credentials (rejected)

The broker could use a client_credentials grant to query the Graph API during `UserPreCheck` (before authentication), using `User.Read.All` application permission. This was rejected because:

* It requires the sensitive `User.Read.All` application permission (allows reading all users in the tenant).
* Not all installations have app credentials configured (some use only the public client OIDC flow).
* It adds latency to every SSH NSS lookup.
* It only works for the user UID, not group GIDs (groups aren't known until after auth).

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

## Related issues

* Consistent UIDs/GIDs across machines — the general cross-IdP tracking issue.
* Support for other IdPs (Google IAM, generic OIDC) will be handled in separate specifications with the attribute storage mechanism appropriate for each provider.

## References

* [Microsoft Graph extensibility overview](https://learn.microsoft.com/en-us/graph/extensibility-overview)
* [Create directory extension property](https://learn.microsoft.com/en-us/graph/api/application-post-extensionproperty)
* [systemd UID/GID ranges](https://systemd.io/UIDS-GIDS/)
