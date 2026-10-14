---
myst:
  html_meta:
    "description lang=en":
      "Microsoft Entra UID and GID discovery flows in authd."
---

# Microsoft Entra Unix ID flows

This document describes the Microsoft Entra UID and GID feature implemented in
this branch. It covers the authentication, Microsoft Graph, caching, offline,
and administrator application flows.

The branch implements **Iteration 1**:

- The broker reads Unix IDs from Entra extension attributes.
- The broker stores them in `token.json`.
- Administrators inspect and apply them with `authctl` or the provisioning
  script.
- The broker does not apply the values to the authd database automatically
  during login.

## Values and configuration

The broker can read three related values:

- **UID**: the numeric Unix user ID from an attribute on the Entra user object.
- **GID**: the numeric Unix group ID from an attribute on each remote Entra
  group.
- **UGID**: the stable Entra Graph object ID of a group. This is not a Unix
  GID. It identifies the group when cached values are reviewed or conflicts
  are reported.

The settings are in the `[msentraid]` section:

```ini
unix_uid_attribute = uidNumber
unix_gid_attribute = gidNumber
unix_uid_required = false
unix_gid_required = false
```

The feature is disabled when both attribute names are empty. A short attribute
name is resolved using the configured `[oidc] client_id`:

```text
uidNumber -> extension_<client-id-without-hyphens>_uidNumber
```

A full name beginning with `extension_` is used unchanged. This supports
attributes created by Entra Connect or Cloud Sync under a different app
registration.

A required flag without its matching attribute is invalid configuration. When
an attribute is optional, a missing or invalid value is omitted. The required
flags are enforced only when `force_access_check_with_provider = true`. In that
mode, a required value that is missing, invalid, or cannot be read fails the
corresponding login. Otherwise, the login continues. A value that cannot be read
keeps its cached value, and a missing or invalid value is omitted. Optional
values behave the same way in both modes.

## Authentication and Graph access matrix

The UID and GID are retrieved from Microsoft Graph. Authentication and Graph access
are separate steps: a token that proves the user's identity is not necessarily
usable for the Graph requests.

| Authentication flow | `register_device` | `client_secret` | Graph path | UID | Group GID and UGID |
| --- | --- | --- | --- | --- | --- |
| Device code | false | no | Current delegated token, if it has the Graph scopes | Available when the token has `User.Read` and `GroupMember.Read.All` | Available when the token has `GroupMember.Read.All` |
| Device code | true | no | Device-registration refresh-token exchange | Available | Available |
| Device code | false | yes | App-only fallback if the delegated token lacks `GroupMember.Read.All` | Not available through the app-only token | Available |
| `entra_auth` | true | no | Device-registration refresh-token exchange | Available | Available |
| `entra_auth` | false | yes | App-only client-credentials token | Not available through the app-only token | Available |
| `entra_auth` | false | no | Invalid configuration | Not available | Not available |

Therefore, `register_device=true` is not a universal requirement for Unix ID
enrichment. It is one way to obtain a delegated Graph token. For the
`entra_auth` flow, either device registration or a client secret is required
because the native Entra authentication token cannot resolve group membership
by itself.

Delegated enrichment needs `User.Read` and `GroupMember.Read.All` with the
necessary consent. The app-only fallback needs the OIDC application to have
`GroupMember.Read.All` as an application permission with tenant admin consent.
The implementation does not add a `User.Read.All` application-permission path,
so an app-only token cannot be used for the user UID lookup through `/me`.

## Graph token selection

The Microsoft Entra provider selects a Graph access path in this order:

1. **Use the current token directly.** If the access token already contains
   `GroupMember.Read.All`, it is used for Graph requests. The token also needs
   `User.Read` for the UID request.
2. **Exchange registered-device data.** If the current token lacks the Graph
   scope and valid device-registration data is available, the broker uses the
   cached refresh token and device keys to obtain a delegated Graph token. The
   exchange requests:

   ```text
   GroupMember.Read.All
   User.Read
   ```

   `GroupMember.Read.All` enables group membership and group attributes.
   `User.Read` enables the `/me` UID lookup.
3. **Use app-only client credentials.** If there is no device-registration
   exchange and a client secret is configured, the broker obtains an app-only
   Graph token. This path is used for group lookup only. It uses the user's
   object ID with `/users/<id>/transitiveMemberOf` because an app-only token
   cannot call `/me` as the signed-in user.

If none of these paths provides the required access, Graph enrichment fails.
For `entra_auth` without registration or a client secret, the broker rejects
the configuration at startup rather than waiting for a login to fail.

If the exchange in step 2 fails for `User.Read`, the broker retries it for
`GroupMember.Read.All` only, so group requests can still succeed. Device-disabled
and invalid-redirect-URI errors are not retried. The UID is not read, so an
optional UID keeps its cached value. A required UID fails the login only with the
forced check, unless the broker asks for device authentication again. If the
retry for groups also fails, enrichment fails.

## UID and group requests

For a delegated Graph token, the broker reads the user attribute with:

```text
GET /me?$select=<unix_uid_attribute>
```

It reads the user's transitive security groups with a request under:

```text
GET /me/transitiveMemberOf/...
```

For an app-only token, it reads groups with:

```text
GET /users/<user-object-id>/transitiveMemberOf/...
```

The group query selects the normal group identity fields and the configured
GID extension attribute. The broker filters to security groups, normalizes
names, and stores each group's Graph `id` as `ugid`.

Groups whose Entra name starts with `linux-` are local mappings. They keep an
empty `ugid` and do not receive a remote GID. They are exempt from required
remote-GID checks.

## ID validation

A directory value is accepted only when it is a positive integer in the
signed 32-bit range. The broker rejects:

- zero;
- negative values;
- fractional or non-finite values;
- `65534` and `65535`;
- `4294967295`; and
- values greater than `2147483647`.

The Microsoft Graph client reads every JSON number as a 64-bit float before the
broker validates it. A fraction that rounds to a whole number in a 64-bit float,
such as `1.0000000000000000001`, is therefore accepted as that whole number. The
Unix ID attributes are registered as integers, so Graph should not return such
values.

Invalid data is logged and omitted. For a required attribute with the forced
check, the login fails with a required Unix attribute error instead.

If duplicate normalized group names have different GIDs, the broker does not
choose one based on response order. It omits the ambiguous GID. With
`unix_gid_required = true` and the forced check, the login fails.

## Device-code flow

### Device code with registration disabled

1. The user completes the device-code flow using the configured OIDC
   application.
2. No device-registration data is created.
3. The broker examines the returned access token.
4. If it already has the Graph scopes, the broker uses it directly for UID and
   groups.
5. If it lacks `GroupMember.Read.All` and a client secret is configured, the
   broker can use the app-only fallback for groups. An optional UID keeps its
   cached value, because an app-only token cannot read it. A required UID fails
   the login only with the forced check.
6. If no usable Graph path exists, enrichment fails.

This flow does not require `register_device=true` when the configured OIDC app
returns a suitable delegated Graph token.

### Device code with registration enabled

1. The user completes the device-code flow.
2. The broker registers the machine when registration data is not already
   cached.
3. The registration data is persisted in `token.json`.
4. The broker exchanges the refresh token and device keys for a delegated Graph
   token requesting `GroupMember.Read.All` and `User.Read`.
5. The broker fetches the UID and groups, including GIDs and one GUID for each
   group.
6. The complete enriched snapshot is cached.
7. The response sent to authd excludes the cache-only UID and GID fields.

The registration cleanup is kept alive until Graph enrichment finishes because
its state is needed by the token exchange.

## `entra_auth` flow

### `entra_auth` with registration enabled

1. The user completes the native Entra passwordless or password-plus-MFA flow.
2. The broker registers the device, or reuses valid registration data already
   in the cache.
3. The broker exchanges the registered-device refresh token for a Graph token.
4. The broker fetches the UID through `/me` and groups through the delegated
   group endpoint.
5. The enriched snapshot is cached and projected before the response is sent
   to authd.

### `entra_auth` with registration disabled and a client secret

1. The user completes the native Entra authentication flow.
2. No device registration is performed.
3. The broker uses client credentials to obtain an app-only Graph token when
   the native token lacks `GroupMember.Read.All`.
4. The broker fetches groups through the user's object ID.
5. The broker can cache group GIDs and one GUID for each group.
6. The UID lookup is unavailable through the app-only token, so an optional UID
   keeps its cached value. With the forced check, `unix_uid_required = true`
   denies the login.

The broker allows this flow only when the client secret is configured. If both
registration and the client secret are absent while `entra_auth` is enabled,
startup fails with an invalid configuration error.

## Online login and cache behavior

### Initial online login

On a first online login, the broker authenticates the user, obtains a usable
Graph path, and performs one enrichment operation. The operation creates a
complete user snapshot containing the current UID and groups. The snapshot is
written to `token.json`.

If the initial enrichment fails as a whole, there is no cached snapshot to fall
back to, so the login fails.

Credentials are saved before enrichment. A failed first login can therefore
leave `token.json` with device-registration data but no groups or UID. The
broker does not mark this entry as incomplete. A later login can then use it as
the previous snapshot when a lookup fails completely. The device-code flow does
this only without the forced check. This is a known limitation. The next
successful online login replaces the entry with a complete snapshot.

### Returning online login

On a returning login, the broker obtains a provider token and then refreshes
the UID and group data. A successful enrichment replaces the
cached snapshot.

The broker keeps the cached value of each Unix ID that it cannot read, with or
without `force_access_check_with_provider`. A UID is kept when the token lacks
`User.Read` or the UID request fails. A GID is kept when the GID attribute cannot
be read: the broker reads the groups without it and uses the cached GID of the
same group, matched by `ugid`. When enrichment fails as a whole, the broker uses
the previous complete snapshot, except that the device-code flow denies the login
with the forced check. Cached IDs from attribute names that are no longer
configured are dropped first, as described under Offline login. With the forced
check, a required value that is missing, invalid, or cannot be read denies the
login. So does a required value that the snapshot lacks when enrichment fails.
The exception is an entry left by a failed first login. See
Initial online login.

Credential freshness is separate from directory-data freshness. After token
refresh or device registration, intermediate cache writes save the new
credentials with the previous `UserInfo`. On a returning login, only enrichment
that returns a snapshot replaces that directory snapshot, even when the forced
check then denies the login. On a first login there is no previous `UserInfo`, so
these writes store no groups and no UID. This preserves rotated refresh tokens
and registration data even when enrichment denies login. On a first login, the
forced check denies the login without storing the enriched snapshot, even when
enrichment returned one. An online login that falls back to cached directory data
remains online and persists its refreshed credentials.

A successful response with a missing or invalid value clears that field in the
newly cached snapshot. A failed lookup keeps the cached value. In both cases, the
forced check denies the login when the field is required.

### Offline login

Offline login performs no Graph requests. It uses the values already present in
`token.json` and grants the login even when some cached IDs are missing. Missing
UIDs and GIDs stay empty.

The forced check is the only mode that enforces the required flags. It also
denies offline logins, because the provider must be reachable for that check.

Cached IDs are valid only for the attribute names they were read from. Before
an offline login or a fallback uses a snapshot, the broker drops each cached
UID or GID whose attribute name differs from the current configuration, and it
rewrites `token.json`. A snapshot without recorded names counts as mismatched,
so its IDs are dropped too. Group memberships are kept. The login then continues
with the dropped IDs left empty. With `force_access_check_with_provider = true`,
the broker never uses a snapshot for an offline login. A required ID that is still
missing after the online lookup denies the login.

## Cache and authd response

The authoritative cache is the provider-ID token path under the broker data
directory. A compatibility username path may exist during migration. The
cache directory is expected to use mode `0700`, and `token.json` mode `0600`.

The cached `UserInfo` contains:

```json
{
  "uid": 41001,
  "groups": [
    {"name": "engineering", "ugid": "<entra-group-id>", "gid": 42001}
  ]
}
```

Before returning the successful authentication result to authd, the broker
creates a copy of the user information and removes `uid` and every remote
`gid` from that copy. The cached copy remains enriched. This keeps the current
Iteration 1 behavior from changing authd's normal local ID allocation.

## Applying cached values

After reviewing `token.json`, an administrator can apply the values manually:

```shell
authctl user set-uid <user> <uid>
authctl group set-gid <user> <uid>
authctl group set-gid <normalized-group-name> <gid>
```

These commands skip the attribute-name check that `apply-entra-unix-ids` runs.
Before you apply values by hand, check the names that the cache was read with:

```shell
sudo jq '.UnixAttributeNames' /path/to/token.json
```

The output must match the configured attribute names. A short name resolves to
`extension_<client-id-without-hyphens>_<name>`. If the output is `null` or the
names differ, do not apply values from that cache. Log in again with the current
configuration, then check again. A failed cache rewrite can leave IDs from the
old attribute in `token.json`.

The `apply-entra-unix-ids` command reads one
explicit `token.json` file. It defaults to a dry run and requires `--apply` for
mutations. It also requires the configured attribute names and refuses a cache
whose names differ. When a cached UID is present, it sets the user's UID and
then the GID of the user's private group to that same value before applying
remote group
GIDs. It skips local groups and groups without a cached GID, uses `ugid`
for conflict reporting, and aborts before mutation if the input contains
conflicting assignments. The operation is not transactional; `authctl` remains
the final check for local and authd database conflicts.

## What this branch does not implement

Automatic application of Entra UID/GID values during login is not part of this
branch. In particular, it does not yet:

- send UID/GID as authoritative IDs in the authd login payload;
- change authd's core user model to represent an optional provider UID;
- replace an automatically allocated UID/GID during user creation; or
- terminate and require a second SSH login after an ID changes.

Those behaviors belong to Iteration 2.
