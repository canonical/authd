---
myst:
  html_meta:
    "description lang=en": "Configure and apply stable Unix IDs from Microsoft Entra ID."
---

(howto::configure-entra-unix-ids)=
# Configure stable Unix IDs from Entra ID

The Microsoft Entra broker can read a Unix UID from the authenticated user and
Unix GIDs from the user's remote groups. It stores the values in the broker's
`token.json` cache. The broker does not apply these values to the authd database
during login. Apply them explicitly with `authctl` or the provisioning script
after checking the cached values.

This feature is disabled unless `unix_uid_attribute` or
`unix_gid_attribute` is configured.

## Register extension properties

Create a directory extension with the `Integer` data type on the `User` object
for the UID and on the `Group` object for the GID. Microsoft's
[directory extensions overview](https://learn.microsoft.com/en-us/graph/extensibility-overview#directory-microsoft-entra-id-extensions)
and [Create `extensionProperty` API](https://learn.microsoft.com/en-us/graph/api/application-post-extensionproperty?view=graph-rest-1.0)
describe how to register the properties. The
[`extensionProperty` resource](https://learn.microsoft.com/en-us/graph/api/resources/extensionproperty?view=graph-rest-1.0)
lists supported data types and target objects.

Use names such as `uidNumber` and `gidNumber`. Graph returns each property's
full name, in the form `extension_<APP_ID_WITHOUT_HYPHENS>_<PROPERTY_NAME>`.
The app ID in the prefix belongs to the application that owns the extension.
Use the full name returned by Graph when configuring an extension owned by a
different application. See Microsoft's
[extension naming guidance](https://learn.microsoft.com/en-us/graph/extensibility-overview#directory-microsoft-entra-id-extensions).

## Discover full property names

Microsoft Entra Connect can register extensions under a separate
`Tenant Schema Extension App`; see Microsoft's
[directory extensions guidance](https://learn.microsoft.com/en-us/entra/identity/hybrid/connect/how-to-connect-sync-feature-directory-extensions#configuration-changes-in-microsoft-entra-id-made-by-the-wizard).
If Cloud Sync created the property, verify its actual owner rather than
assuming it is the application configured in the broker.

Use Microsoft Graph PowerShell to find the owner and full property name. See
the official references for
[Connect-MgGraph](https://learn.microsoft.com/en-us/powershell/module/microsoft.graph.authentication/connect-mggraph),
[Get-MgApplication](https://learn.microsoft.com/en-us/powershell/module/microsoft.graph.applications/get-mgapplication),
and
[Get-MgApplicationExtensionProperty](https://learn.microsoft.com/en-us/powershell/module/microsoft.graph.applications/get-mgapplicationextensionproperty).
For example:

```powershell
Connect-MgGraph -Scopes "Application.Read.All"

Get-MgApplication -All -Property DisplayName,AppId | ForEach-Object {
  $app = $_
  Get-MgApplicationExtensionProperty -ApplicationId $app.Id -All |
    Where-Object { $_.Name -match '(uidNumber|gidNumber)$' } |
    Select-Object @{Name = "AppDisplayName"; Expression = { $app.DisplayName } }, `
      @{Name = "AppId"; Expression = { $app.AppId } }, Name
} | Format-Table -AutoSize
```

The `ApplicationId` parameter is the owning application's object ID, while
`AppId` is its client ID; see the
[Get-MgApplicationExtensionProperty reference](https://learn.microsoft.com/en-us/powershell/module/microsoft.graph.applications/get-mgapplicationextensionproperty).
Use the returned `Name` as `unix_uid_attribute` or `unix_gid_attribute`.
`Application.Read.All` is required, and Microsoft marks it as requiring admin
consent in the [Graph permissions reference](https://learn.microsoft.com/en-us/graph/permissions-reference#applicationreadall).

Assign values through Graph, PowerShell, or another controlled provisioning
process. Microsoft defines `Integer` as a 32-bit value in the
[`extensionProperty` resource](https://learn.microsoft.com/en-us/graph/api/resources/extensionproperty?view=graph-rest-1.0).
authd further requires values in the positive signed 32-bit range and rejects
`0`, `65534`, `65535`, and `4294967295`.

## Assign the extension values

Use delegated Microsoft Graph access to assign values as the signed-in user.
The
[Update-MgUser](https://learn.microsoft.com/en-us/powershell/module/microsoft.graph.users/update-mguser)
and
[Update-MgGroup](https://learn.microsoft.com/en-us/powershell/module/microsoft.graph.groups/update-mggroup)
commands accept extension values through `-AdditionalProperties`:

```powershell
Connect-MgGraph -Scopes "User.ReadWrite.All", "Group.ReadWrite.All"

Update-MgUser -UserId "<USER_UPN>" -AdditionalProperties @{
  "extension_<APP_ID_WITHOUT_HYPHENS>_uidNumber" = 50001
}

Update-MgGroup -GroupId "<GROUP_OBJECT_ID>" -AdditionalProperties @{
  "extension_<APP_ID_WITHOUT_HYPHENS>_gidNumber" = 60001
}
```

Microsoft's
[extension permissions guidance](https://learn.microsoft.com/en-us/graph/extensibility-overview#permissions-and-privileges)
explains the Graph permissions and Entra roles for extension writes. User
extension updates require delegated `User.ReadWrite.All` and a supported
administrator role, such as `User Administrator`; see the
[built-in role reference](https://learn.microsoft.com/en-us/entra/identity/role-based-access-control/permissions-reference#user-administrator).
For group updates, see the
[Update group API](https://learn.microsoft.com/en-us/graph/api/group-update?view=graph-rest-1.0)
and the [Group.ReadWrite.All permission](https://learn.microsoft.com/en-us/graph/permissions-reference#groupreadwriteall)
for permission and ownership details. Microsoft marks these write permissions
as requiring admin consent in the
[Graph permissions reference](https://learn.microsoft.com/en-us/graph/permissions-reference).

After connecting, verify that the expected account, tenant, and delegated
permissions are active:

```powershell
Get-MgContext | Select-Object Account, TenantId, Scopes
```

See the
[Get-MgContext reference](https://learn.microsoft.com/en-us/powershell/module/microsoft.graph.authentication/get-mgcontext)
for the available context fields. The scopes shown here do not prove that the
signed-in user has the required Entra role. A `403` can therefore persist even
when the expected scope is present; see Microsoft's
[extension permissions guidance](https://learn.microsoft.com/en-us/graph/extensibility-overview#permissions-and-privileges).

Replace `<USER_UPN>` with the user's Entra sign-in name or object ID, and
`<GROUP_OBJECT_ID>` with the group's object ID. See the
[Update-MgUser](https://learn.microsoft.com/en-us/powershell/module/microsoft.graph.users/update-mguser)
and
[Update-MgGroup](https://learn.microsoft.com/en-us/powershell/module/microsoft.graph.groups/update-mggroup)
references for the supported parameter values.

Avoid giving an Entra group the same name as a user. authd already creates a
private group for each user, named after that user. An Entra group with the same
name collides with that private group. If the cache has no UID, the script
refuses to apply the cache, because applying the group's GID would change the
private group's GID. If the cache has a UID, the group's GID must match that UID.
Avoid other name collisions too. For example, two Entra groups whose names are
the same in lowercase conflict, and the script refuses them. Also avoid names
that match local groups. `authctl` is the final check for local and authd
database conflicts.

Use the full property names returned when the properties were created. For
example, an attribute created by Entra Connect or Cloud Sync may use
`extension_<SYNC_APP_ID_WITHOUT_HYPHENS>_uidNumber` rather than the broker
application's prefix. For more than one user or group, put the same update
commands in a controlled provisioning loop and keep the UID and GID mapping
under change control.

## Configure the broker

The configured `[oidc] client_id` is used to resolve short names. It remains the
source of the extension prefix even when device registration uses the
Microsoft Broker App at runtime. Microsoft's
[Primary Refresh Token overview](https://learn.microsoft.com/en-us/entra/identity/devices/concept-primary-refresh-token)
describes the general broker flow; the client ID and prefix behavior here are
authd-specific.

```ini
[oidc]
issuer = https://login.microsoftonline.com/<TENANT_ID>/v2.0
client_id = <APPLICATION_CLIENT_ID>

[msentraid]
unix_uid_attribute = uidNumber
unix_gid_attribute = gidNumber
unix_uid_required = false
unix_gid_required = false
```

The tenant-scoped issuer URL above follows Microsoft's
[OIDC endpoint guidance](https://learn.microsoft.com/en-us/entra/identity-platform/v2-protocols-oidc#oidc-endpoint-overview).

On a successful refresh, a missing or invalid required value denies login in
both modes. An unreadable required value and a refresh that cannot complete
follow `force_access_check_with_provider`: the forced check denies login;
without it, a returning login uses its cached snapshot. Offline login is denied
when checks are forced and uses cached values when they are not.

Use the full names when the properties were created by Entra Connect or Cloud
Sync:

```ini
[msentraid]
unix_uid_attribute = extension_abc123_uidNumber
unix_gid_attribute = extension_abc123_gidNumber
```

Set a required flag only when every applicable login must have the value. A
required flag with an empty attribute is invalid configuration. A successful
refresh that returns an absent, null, or invalid required value denies login in
both modes and clears the value in the refreshed snapshot before denial. An
unreadable required value denies with checks forced; without forced checks, the
cached value is used, or the value remains empty if none is cached. A refresh
that cannot complete denies with checks forced and uses the previous snapshot
without them. Optional values never deny login.

A required GID applies to every remote group. A remote group without a GID
denies login in both modes. Entra groups prefixed with `linux-` map to local
Linux groups; for example, `linux-sudo` adds the user to the local `sudo` group.
These mappings have no remote GID and are exempt from required-GID checks. See
the [group and privilege management reference](reference::group-management) for
details. An unforced GID requirement can therefore lock out users whose remote
groups lack a GID.

If you change `unix_uid_attribute` or `unix_gid_attribute`, the broker drops
the cached IDs that came from the old attribute. It does this the next time it
reads that user's cache during a login, and it rewrites `token.json` without
those IDs. The next login continues with the ID empty until an online login reads
the new attribute. With `force_access_check_with_provider = true`, the broker
never uses a cached snapshot for an offline login. A required ID that is still
missing after the online lookup denies the login.

If the broker cannot rewrite `token.json`, for example on a full or read-only
disk, the login is denied when `force_access_check_with_provider` is `true` and
`unix_uid_required` or `unix_gid_required` is `true`. In other cases the login
continues, and `apply-entra-unix-ids` refuses the cache until a later login
rewrites it.

Restart the broker after changing the configuration. See
[Configure authd](ref::config) for the service-specific restart command.

### Graph permissions and direct Entra authentication

The broker's delegated enrichment uses `User.Read` and
`GroupMember.Read.All`. See Microsoft's
[User.Read](https://learn.microsoft.com/en-us/graph/permissions-reference#userread)
and
[GroupMember.Read.All](https://learn.microsoft.com/en-us/graph/permissions-reference#groupmemberreadall)
permission entries for consent and permission details. Microsoft marks
`GroupMember.Read.All` as requiring admin consent.

When direct `entra_auth` is used without device registration, the existing
app-only group fallback can read groups only when the configured OIDC
application has `GroupMember.Read.All` application permission, admin consent,
and a client secret. This is the OAuth
[client credentials flow](https://learn.microsoft.com/en-us/entra/identity-platform/v2-oauth2-client-creds-grant-flow).
Microsoft documents that `/me` requires a signed-in user and delegated
permission in the [Update user API](https://learn.microsoft.com/en-us/graph/api/user-update?view=graph-rest-1.0);
application permissions are not supported for `/me`, so UID lookup is
unavailable on this path. An optional UID keeps its cached value. A required
UID denies login with checks forced and keeps its cached value without them.
The broker does not add a `User.Read.All` application-credential UID lookup.

The client secret is used only for the Graph app-only fallback. The
client-credentials flow is separate from Entra's public-client token refresh;
the broker does not send the secret during public-client refresh, including
when device registration is disabled.

## Login and cache behavior

On a first online login, a whole-refresh failure has no previous snapshot to
fall back to, so login fails. Credentials and device-registration data may
already be cached. Such a first-login entry has no completeness marker and may
have no groups or UID. A later unforced login can use it as the previous
snapshot when a refresh cannot complete, even if required IDs are missing. A
successful refresh that returns a missing or invalid required value denies login
in both modes. The next successful online login replaces the incomplete entry.

On a returning login, a successful refresh that returns a missing or invalid
required value denies login in both modes. The refreshed snapshot with the
cleared value is saved before denial. An unreadable required value denies with
checks forced; without them, the cached value is used, or left empty if none is
cached. If the whole refresh cannot complete, checks forced deny login and
checks not forced use the previous snapshot, even if it lacks a required ID. A
GID that cannot be read uses the cached GID for the same group, matched by
`ugid`. Refreshed credentials are retained even when enrichment denies login.

Offline login does not issue Graph requests. With checks forced, login is denied
because the provider must be reachable. Without forced checks, the cached UID
and GIDs are used even when some are missing. For the flow details, see the
[explanation](../explanation/entra-unix-id-flows).

The UID and GID fields are stored in the broker cache and are removed from the
response sent to authd. Normal authd ID generation remains unchanged.

## Locate and inspect `token.json`

The broker's configured data directory is represented by `$DATA_DIR` below. For
a snap installation, search under the broker's current data directory, for
example `/var/snap/authd-msentraid/current`:

```shell
sudo find -L /var/snap/authd-msentraid/current -type f -name token.json -print
```

If several cache files are found, identify the one for the user you are
provisioning by reading its cached name:

```shell
sudo jq -r '.UserInfo.name' /path/to/token.json
```

The authoritative cache layout is:

```text
$DATA_DIR/<normalized-issuer>/<provider-id>/token.json
```

The normalized issuer removes the URL scheme and replaces `/` and `:` with
`_`. For example, this issuer:

```text
https://login.microsoftonline.com/<TENANT_ID>/v2.0
```

becomes:

```text
login.microsoftonline.com_<TENANT_ID>_v2.0
```

The provider ID is the stable identity used by the broker for the user. A
username compatibility path may be a symlink left by cache migration;
provider-ID directories are authoritative after migration.

The file contains refresh credentials and must normally be readable only by
root or the broker service. The cache directory uses mode `0700` and
`token.json` uses mode `0600`.

The top-level JSON key is capitalized because it is the Go field name:

```shell
sudo jq '.UserInfo | {name, uid, groups: [(.groups // [])[] | {name, ugid, gid}]}' /path/to/token.json
```

The output contains:

* `name`: the cached Entra username.
* `uid`: the cached Unix UID for that user. It can be absent or null when the
  attribute was not available and is optional.
* `groups[].name`: the normalized group name used by authd.
* `groups[].ugid`: the stable Entra group object ID.
* `groups[].gid`: the cached Unix GID for that remote group.

The relevant paths are `.UserInfo.uid` and `.UserInfo.groups[].gid`.
`ugid` identifies the Entra group independently of its display name and is
used when reviewing assignments or reporting conflicts. A group with an empty
`ugid` is a local mapping and should not be applied with `authctl`. Groups with
no cached `gid` should also be skipped.

Because `token.json` contains refresh credentials, do not copy or share the
complete file. Inspect only the fields needed for provisioning and keep the
file accessible only to root or the broker service.

## Apply the cached values

After you change the attribute configuration, log in as the user before you
apply the values. The broker drops IDs from the old attribute only during a
login. Until then the script refuses the cache, because its attribute names
differ from the configured names.

Review a cache first. The `apply-entra-unix-ids` command is installed with the
`authd` package and needs `jq`, which the package recommends. See
`man apply-entra-unix-ids` for all options, environment variables, and exit
codes. Pass the attribute names from the `[msentraid]` section of the broker
configuration. An empty name means that the attribute is not configured. The
command defaults to a dry run:

```shell
sudo apply-entra-unix-ids --uid-attribute extension_abc123_uidNumber \
  --gid-attribute extension_abc123_gidNumber /path/to/token.json
```

From a source checkout, run `scripts/apply-entra-unix-ids` instead of the
installed `apply-entra-unix-ids` command.

If the configuration uses short names, also pass the `client_id` from the
`[oidc]` section:

```shell
sudo apply-entra-unix-ids --uid-attribute uidNumber --gid-attribute gidNumber \
  --client-id <APPLICATION_CLIENT_ID> /path/to/token.json
```

Use `--apply` only after reviewing the planned changes:

```shell
sudo apply-entra-unix-ids --apply --uid-attribute extension_abc123_uidNumber \
  --gid-attribute extension_abc123_gidNumber /path/to/token.json
```

The script refuses a cache that holds Unix IDs when its attribute names differ
from the names you pass, or when the names are missing. Log in again with the
current configuration to refresh the cache, then review it again.

The script reads the cache once and builds the entire plan from that captured
JSON document. It rejects malformed input or multiple JSON documents before
running any command. It validates numeric values, skips local groups and groups without a
cached GID, uses `ugid` for conflict reporting, and passes the normalized group
name to `authctl`. It aborts before any command when the input contains
conflicting UID/GID assignments. Application is not transactional: a later
`authctl` command may fail after earlier commands have succeeded.

When the cache contains a UID, the script first sets the user's UID, then sets
the GID of the user's private group (named after the user) to the same value.
It then applies remote group GIDs. Without a cached UID, it leaves the private
group unchanged. If a remote group has the same name as the user and the cache
has no UID, the script aborts before running any command, because applying that
group would change the private group's GID.

The underlying commands remain available for individual changes:

```shell
sudo authctl user set-uid <user> <uid>
sudo authctl group set-gid <user> <uid>
sudo authctl group set-gid <normalized-group-name> <gid>
```

Before changing an existing UID, stop the user's active sessions. Check for
duplicate IDs before applying a batch. `authctl` remains the final check for
local-account conflicts, authd database conflicts, ownership migration, and
primary-group migration. Handle files outside the user's home directory
manually after an ownership change.

## Shared filesystems

To use the same numeric identities across machines, authenticate and apply the
same cached UID and GIDs on each machine. This can make NFS and Samba file
ownership stable without replacing their existing ID-mapping guidance. Keep
using NFS ID mapping and Kerberos or Samba ID mapping when those mechanisms are
required by the deployment.