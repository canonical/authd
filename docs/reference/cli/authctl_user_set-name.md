## authctl user set-name

Rename a user managed by authd

### Synopsis

Rename a user managed by authd to a new username.

The new username must be unique. The command must be run as root.

Note: This command does NOT rename the user's home directory. The home
directory path remains unchanged to avoid potential data loss and permission
issues. You will need to manually rename the home directory if desired.

The user's private group is renamed along with the user, because authd names
private groups after the user they belong to.

The new username is kept across logins: authd remembers that it was set
locally, so the broker does not restore the username reported by the identity
provider. The user must log in with the new username from now on.

Only users that authd identifies by a stable provider ID can be renamed,
because that ID is what ties the new name to the account. Users stored before
authd started recording it get one on their next login with a broker that
provides it.

The user must not have any active processes when renaming.

```
authctl user set-name <old-name> <new-name> [flags]
```

### Examples

```
  # Rename user "alice" to "alice-new"
  authctl user set-name alice alice-new
```

### Options

```
  -h, --help   help for set-name
```

### SEE ALSO

* [authctl user](authctl_user.md)	 - Commands related to users

