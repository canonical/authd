## authctl group delete

Delete a group managed by authd

### Synopsis

Delete a group from the authd database.

Warning: Deleting a group that still owns files on the filesystem can lead
to security issues.

Any existing files owned by this group may become accessible to a different
group that is later assigned the same GID.
You should manually check that no files remain owned by this group.
The command must be run as root.

```
authctl group delete <group> [flags]
```

### Examples

```
  # Delete group "staff" from the authd database
  authctl group delete staff

  # Delete group "staff" without confirmation prompt
  authctl group delete --yes staff
```

### Options

```
  -h, --help   help for delete
  -y, --yes    Skip confirmation prompt
```

### SEE ALSO

* [authctl group](authctl_group.md)	 - Commands related to groups

