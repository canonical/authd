---
myst:
  html_meta:
    "description lang=en": "Configure and apply stable Unix IDs from Microsoft Entra ID."
---

(howto::configure-entra-unix-ids)=
# Configure stable Unix IDs from Entra ID

The Microsoft Entra broker can read a Unix UID from the authenticated user and
Unix GIDs from the user's remote groups. It stores the values in the broker's
`token.json` cache.

The broker does not apply these values to the authd database
during login. After reviewing the cached values, you can apply them individually
with `authctl` or use the [provisioning helper](#apply-the-cached-values) to
prepare the user's UID and remote-group GID updates from one cache for review.
The helper is installed with the `authd` package; from a source checkout, run
`scripts/apply-entra-unix-ids`.

```{note}
This feature is disabled unless `unix_uid_attribute` or
`unix_gid_attribute` is configured.
```

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
[extension naming guidance](https://learn.microsoft.com/en-us/graph/extensibility-overview#directory-microsoft-entra-id-extensions)
for more information.

## Discover full property names

Microsoft Entra Connect can register extensions under a separate
`Tenant Schema Extension App`. Microsoft's
[directory extensions guidance](https://learn.microsoft.com/en-us/entra/identity/hybrid/connect/how-to-connect-sync-feature-directory-extensions#configuration-changes-in-microsoft-entra-id-made-by-the-wizard)
describes this in more detail.
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
administrator role, such as `User Administrator`. The
[built-in role reference](https://learn.microsoft.com/en-us/entra/identity/role-based-access-control/permissions-reference#user-administrator)
lists the role's permissions.
For group updates, check the
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

The
[Get-MgContext reference](https://learn.microsoft.com/en-us/powershell/module/microsoft.graph.authentication/get-mgcontext)
lists the available context fields. The scopes shown here do not prove that the
signed-in user has the required Entra role. A `403` can therefore persist even
when the expected scope is present; see Microsoft's
[extension permissions guidance](https://learn.microsoft.com/en-us/graph/extensibility-overview#permissions-and-privileges).

Replace `<USER_UPN>` with the user's Entra sign-in name or object ID, and
`<GROUP_OBJECT_ID>` with the group's object ID.

### Naming conventions

An Entra group with the same name as the user collides with the private group
automatically created by authd.
If the cache has no UID, the [script](#apply-the-cached-values) refuses to apply
the cache, because applying the group's GID would change the private group's
GID.
If the cache has a UID, the group's GID must match that UID.
Entra groups whose names are the same in lowercase conflict, and the script
refuses them. Also avoid names that match local groups. `authctl` is the final
check for local and authd database conflicts.

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

With `force_access_check_with_provider = true`, login requires a provider
check; with it set to `false`, returning or offline logins can use cached IDs.
A successful refresh that returns a missing or invalid required ID denies login
with either setting. To configure this option, see
[Force remote access check with the identity provider](ref::config-force-provider-auth).
For behavior by login type, consult the
[Entra Unix ID flow explanation](../explanation/entra-unix-id-flows).

Use the full names when the properties were created by Entra Connect or Cloud
Sync:

```ini
[msentraid]
unix_uid_attribute = extension_abc123_uidNumber
unix_gid_attribute = extension_abc123_gidNumber
```

Set a required flag only when every applicable user or group should have a
value from the provider. A required flag with an empty attribute is invalid
configuration.
Optional values do not deny login.

A required GID applies to every remote group. Groups prefixed with `linux-` map
to local Linux groups and have no remote GID, so they are exempt. For details
on local group mappings, consult the
[group and privilege management reference](reference::group-management).

Changing an attribute name invalidates cached IDs read under the old name on
the next login. Log in again with the updated configuration before applying
values from the cache. For cache invalidation and provider-failure behavior,
refer to the [flow explanation](../explanation/entra-unix-id-flows).

Restart the broker after changing the configuration. See
[Configure authd](ref::config) for the service-specific restart command.

### Graph permissions and direct Entra authentication

The broker's delegated enrichment needs `User.Read` and `GroupMember.Read.All`;
the latter requires admin consent. See Microsoft's
[User.Read](https://learn.microsoft.com/en-us/graph/permissions-reference#userread)
and
[GroupMember.Read.All](https://learn.microsoft.com/en-us/graph/permissions-reference#groupmemberreadall)
permission references.

For direct `entra_auth` without device registration, group lookup needs a
client secret and `GroupMember.Read.All` as an application permission with
admin consent. This path cannot read the UID. The
[Entra Unix ID flow explanation](../explanation/entra-unix-id-flows) describes
the Graph paths and their login behavior.

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

The file contains refresh credentials. Do not copy or share the complete file.
Keep it accessible only to root or the broker service, and inspect only the
fields needed for provisioning. For cache ownership and file modes, refer to
the [flow explanation](../explanation/entra-unix-id-flows).

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

## Apply the cached values

After you change the attribute configuration, log in as the user before you
apply the values. The broker drops IDs from the old attribute only during a
login.

Review the cache first. The `apply-entra-unix-ids` command is installed with the
`authd` package and needs `jq`, which the package recommends. See
`man apply-entra-unix-ids` for all options, environment variables, and exit
codes. Pass the attribute names from the `[msentraid]` section of the broker
configuration:

```shell
sudo apply-entra-unix-ids --uid-attribute extension_abc123_uidNumber \
  --gid-attribute extension_abc123_gidNumber /path/to/token.json
```

From a source checkout, run `scripts/apply-entra-unix-ids` instead of the
installed `apply-entra-unix-ids` command.

For short attribute names, use the `client_id` from the `[oidc]` section:

```shell
sudo apply-entra-unix-ids --uid-attribute uidNumber --gid-attribute gidNumber \
  --client-id <APPLICATION_CLIENT_ID> /path/to/token.json
```

Use `--apply` only after reviewing the planned changes:

```shell
sudo apply-entra-unix-ids --apply --uid-attribute extension_abc123_uidNumber \
  --gid-attribute extension_abc123_gidNumber /path/to/token.json
```

If the command refuses the cache because its attribute names do not match the
current configuration, log in again with that configuration to refresh the
cache, then review it again.

When the cache contains a UID, applying it also sets the GID of the user's
private group to that UID. Details about local groups and conflict handling are
in the
[flow explanation](../explanation/entra-unix-id-flows).

For individual changes, use:

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