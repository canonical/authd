## authctl user delete

Delete a user managed by authd

### Synopsis

Delete a user from the authd database.

Warning: Deleting a user that still owns files on the filesystem can lead
to security issues.

Any existing files owned by this user may become accessible to a different
user that is later assigned the same UID.
You should manually check that no files remain owned by this user.

If you only want to prevent the user from logging in, consider using
'authctl user lock' instead. A locked user retains their UID, ensuring
no other user can be assigned the same UID.
The command must be run as root.

```
authctl user delete <user> [flags]
```

### Examples

```
  # Delete user "alice" from the authd database
  authctl user delete alice

  # Delete user "alice" without confirmation prompt
  authctl user delete --yes alice

  # Delete user "alice" and remove their home directory
  authctl user delete --remove alice
```

### Options

```
  -h, --help     help for delete
  -r, --remove   Remove the user's home directory
  -y, --yes      Skip confirmation prompt
```

### SEE ALSO

* [authctl user](authctl_user.md)	 - Commands related to users

