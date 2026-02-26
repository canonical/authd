package localentries

import (
	userslocking "github.com/canonical/authd/internal/users/locking"
	"github.com/canonical/authd/internal/users/types"
)

// WithGroupPath overrides the default /etc/group path for tests.
func WithGroupPath(p string) Option {
	return func(o *options) {
		o.inputGroupPath = p
		o.outputGroupPath = p
	}
}

// WithPasswdInputPath overrides the default /etc/passwd path for input in tests.
func WithPasswdInputPath(p string) Option {
	return func(o *options) {
		o.inputPasswdPath = p
	}
}

// WithGroupInputPath overrides the default /etc/group path for input in tests.
func WithGroupInputPath(p string) Option {
	return func(o *options) {
		o.inputGroupPath = p
	}
}

// WithGroupOutputPath overrides the default /etc/group path for output in tests.
func WithGroupOutputPath(p string) Option {
	return func(o *options) {
		o.outputGroupPath = p
	}
}

// WithMockUserDBLocking uses a mock implementation to lock the users database.
func WithMockUserDBLocking() Option {
	return func(o *options) {
		mock := userslocking.SimpleMock{}
		o.writeLockFunc = mock.WriteLock
		o.writeUnlockFunc = mock.WriteUnlock
	}
}

// WithUserDBLockedInstance allows to use a test-provided locked instance that
// can be used to verify the refcounting behavior instead of relying on the
// default instance that is used normally.
func WithUserDBLockedInstance(userDBLocked *UserDBLocked) Option {
	return func(o *options) {
		o.userDBLocked = userDBLocked
	}
}

// GroupFileBackupPath exposes the path to the group file backup for testing.
func GroupFileBackupPath(groupFilePath string) string {
	return groupFileBackupPath(groupFilePath)
}

// ValidateChangedGroups validates the new groups given the current, changed and new groups.
func ValidateChangedGroups(currentGroups, newGroups []types.GroupEntry) error {
	return validateChangedGroups(currentGroups, newGroups)
}
