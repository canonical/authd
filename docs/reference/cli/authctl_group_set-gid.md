## authctl group set-gid

Set the GID of a group managed by authd

### Synopsis

Set the GID of a group managed by authd to the specified value.

The new GID must be unique and non-negative. The command must be run as root.

When a group's GID is changed, any users whose primary group is set to this
group will have the GID of their primary group updated. The home directories of
these users, and any files within these directories that are owned by the group,
will be updated to the new GID.

Files outside users' home directories are not updated and must be changed
manually. Note that changing a GID can be unsafe if files on the system are
still owned by the original GID: those files may become accessible to a
different group that is later assigned that GID.

```
authctl group set-gid <name> <gid> [flags]
```

### Examples

```
  # Set the GID of group "staff" to 30000
  authctl group set-gid staff 30000
```

### Options

```
  -h, --help   help for set-gid
```

### SEE ALSO

* [authctl group](authctl_group.md)	 - Commands related to groups

