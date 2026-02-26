## authctl user set-uid

Set the UID of a user managed by authd

### Synopsis

Set the UID of a user managed by authd to the specified value.

The new UID must be unique and non-negative. The command must be run as root.

The ownership of the user's home directory, and any files within the directory
that the user owns, will automatically be updated to the new UID.

Files outside the user's home directory are not updated and must be changed
manually. Note that changing a UID can be unsafe if files on the system are
still owned by the original UID: those files may become accessible to a
different account that is later assigned that UID.

```
authctl user set-uid <name> <uid> [flags]
```

### Examples

```
  # Set the UID of user "alice" to 15000
  authctl user set-uid alice 15000
```

### Options

```
  -h, --help   help for set-uid
```

### SEE ALSO

* [authctl user](authctl_user.md)	 - Commands related to users

