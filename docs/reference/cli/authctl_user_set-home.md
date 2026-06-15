## authctl user set-home

Set the home directory of a user managed by authd

### Synopsis

Set the home directory of a user managed by authd to the specified path.

The path must be absolute. The command must be run as root.

If the user's current home directory exists, its contents are moved to the new
location. The move is performed with rename(2), so the new path must be on the
same filesystem as the current home directory; otherwise the command fails and
the directory must be moved manually.

The command fails without making any change if the new path already exists. If
the user's current home directory does not exist, only the database record is
updated and no directory is created.

The command refuses to run while the user has active processes.

```
authctl user set-home <user> <path> [flags]
```

### Examples

```
  # Set the home directory of user "alice"
  authctl user set-home alice /home/alice-new
```

### Options

```
  -h, --help   help for set-home
```

### SEE ALSO

* [authctl user](authctl_user.md)	 - Commands related to users

