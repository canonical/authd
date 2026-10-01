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

Register one integer extension property on the Entra `User` object for the UID
and one on the `Group` object for the GID. The Graph API can create them with
requests like these, using the application registration that is configured in
the broker:

```text
POST https://graph.microsoft.com/v1.0/applications/{application-id}/extensionProperties
Content-Type: application/json

{
  "name": "uidNumber",
  "dataType": "Integer",
  "targetObjects": ["User"]
}
```

```text
POST https://graph.microsoft.com/v1.0/applications/{application-id}/extensionProperties
Content-Type: application/json

{
  "name": "gidNumber",
  "dataType": "Integer",
  "targetObjects": ["Group"]
}
```

Record the full names returned by Graph. For an application client ID such as
`abc-123`, a short name such as `uidNumber` resolves to
`extension_abc123_uidNumber`. A value that already starts with `extension_` is
used as a full name. Attribute names may contain only ASCII letters, digits,
and underscores.

## Discover full property names

If Entra Connect or Cloud Sync created the properties, they may belong to the
sync application's registration rather than the application configured in the
broker. Use Microsoft Graph PowerShell to list extension properties across the
tenant:

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

The `Name` column is the full Graph property name to use in the broker
configuration. The `AppId` column identifies which application registered it;
do not rebuild the name from the broker's client ID when these values belong to
Entra Connect or Cloud Sync. This query needs `Application.Read.All`, and an
administrator may need to grant consent. Look for the `uidNumber` and
`gidNumber` rows, then use their `Name` values as
`unix_uid_attribute` and `unix_gid_attribute`.

Assign positive integer values through Graph, PowerShell, or another controlled
provisioning process. Values must be in the positive signed 32-bit range.
The values `0`, `65534`, `65535`, and `4294967295` are rejected. Entra Connect
and Cloud Sync may create extension properties with a different application
prefix; discover and configure the full property names they actually use.

## Assign the extension values

The Microsoft Graph PowerShell module can assign values to individual users and
groups. These commands use delegated permissions: Graph acts as the signed-in
user, subject to both the granted Graph permissions and that user's Entra
permissions.

```powershell
Connect-MgGraph -Scopes "User.ReadWrite.All", "Group.ReadWrite.All"

Update-MgUser -UserId "<USER_UPN>" -AdditionalProperties @{
  "extension_<APP_ID_WITHOUT_HYPHENS>_uidNumber" = 50001
}

Update-MgGroup -GroupId "<GROUP_OBJECT_ID>" -AdditionalProperties @{
  "extension_<APP_ID_WITHOUT_HYPHENS>_gidNumber" = 60001
}
```

For user extension values, Microsoft Graph's extension guidance requires the
delegated `User.ReadWrite.All` permission and a supported Microsoft Entra
administrator role on the signed-in user. Use an account assigned the
`User Administrator` role to edit a user's extension value. The permission
alone is not enough.

For group extension values, grant the delegated `Group.ReadWrite.All`
permission and sign in as an owner of the target group. Group ownership does
not replace the Graph permission, and tenant policy or group type can impose
additional restrictions. Grant tenant admin consent for these delegated
permissions when required by your tenant.

After connecting, verify that the expected account, tenant, and delegated
permissions are active:

```powershell
Get-MgContext | Select-Object Account, TenantId, Scopes
```

The scopes shown here do not prove that the signed-in user has the required
Entra role. A `403` can therefore persist even when the expected scope is
present. See Microsoft's guidance for [directory extension permissions and
privileges](https://learn.microsoft.com/en-us/graph/extensibility-overview#permissions-and-privileges),
[updating users](https://learn.microsoft.com/en-us/graph/api/user-update?view=graph-rest-1.0),
and [updating groups](https://learn.microsoft.com/en-us/graph/api/group-update?view=graph-rest-1.0).

Replace `<USER_UPN>` with the user's Entra sign-in name, such as an address in
your organization's domain. `Update-MgUser` also accepts the user's Entra
object ID instead of a UPN. Replace `<GROUP_OBJECT_ID>` with the Entra object
ID of the group whose GID you are assigning.

Use the full property names returned when the properties were created. For
example, an attribute created by Entra Connect or Cloud Sync may use
`extension_<SYNC_APP_ID_WITHOUT_HYPHENS>_uidNumber` rather than the broker
application's prefix. For more than one user or group, put the same update
commands in a controlled provisioning loop and keep the UID and GID mapping
under change control.

## Configure the broker

The configured `[oidc] client_id` is used to resolve short names. It remains the
source of the extension prefix even when device registration uses the
Microsoft Broker App at runtime.

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

Use the full names when the properties were created by Entra Connect or Cloud
Sync:

```ini
[msentraid]
unix_uid_attribute = extension_abc123_uidNumber
unix_gid_attribute = extension_abc123_gidNumber
```

Set a required flag only when every applicable login must have the value. A
required flag with an empty attribute is invalid configuration. A missing,
null, malformed, fractional, negative, zero, out-of-range, or reserved value
fails the login when required. Optional values are omitted from the new cache
snapshot; a successful response with an absent or null value clears a previous
cached value.

Restart the broker after changing the configuration. See
[Configure authd](ref::config) for the service-specific restart command.

### Graph permissions and direct Entra authentication

Delegated Graph enrichment needs `User.Read` and `GroupMember.Read.All`.
`GroupMember.Read.All` requires tenant admin consent.

When direct `entra_auth` is used without device registration, the existing
app-only group fallback can read groups only when the configured OIDC
application has `GroupMember.Read.All` application permission, admin consent,
and a client secret. An app-only token cannot be used for `/me`, so UID lookup
is deliberately best effort: an optional UID remains unset, while
`unix_uid_required = true` rejects the login. The broker does not add a
`User.Read.All` application-credential UID lookup.

The client secret is used only for the Graph app-only fallback. Entra
public-client token refreshes do not send it, including when device
registration is disabled.

## Login and cache behavior

On an initial online login, the broker reads the user and group attributes and
caches one complete `UserInfo` snapshot. If enrichment fails, there is no
fabricated cache fallback and the login fails. On a returning login, an
enrichment failure may use the previous complete snapshot under the existing
authentication flow's fallback rules when
`force_access_check_with_provider = false`. It never combines old groups with
a new UID or the reverse. Required-attribute errors and forced access checks
still deny login.

Every cache fallback checks that required cached IDs are present. Refreshed
credentials and device-registration data are saved without replacing the
previous directory snapshot until enrichment succeeds. They are retained even
if enrichment later denies login. Falling back to the previous `UserInfo`
does not discard those credentials or mark an online session as offline.

Offline login does not issue Graph requests. It uses cached UID/GID values and
denies the login when a required UID is missing or a required remote group GID
is missing. Groups whose cached `ugid` is empty are local `linux-*` mappings and
are exempt from required-GID checks.

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

Review a cache first. The maintained script defaults to a dry run:

```shell
sudo scripts/apply-entra-unix-ids /path/to/token.json
```

Use `--apply` only after reviewing the planned changes:

```shell
sudo scripts/apply-entra-unix-ids --apply /path/to/token.json
```

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
group unchanged.

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