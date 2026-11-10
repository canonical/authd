package pam

import "os/user"

// Re-export DefaultConfig fields for use in tests.
var (
	AuthFailDelayThreshold = DefaultConfig.AuthFailDelayThreshold
	AuthFailDelay          = DefaultConfig.AuthFailDelay

	// AuthFailMaxTracked allows tests to override the tracker capacity.
	AuthFailMaxTracked = &authFailMaxTracked
)

// SetLookupUserByName replaces the NSS name lookup and returns a restore function.
func SetLookupUserByName(fn func(string) (*user.User, error)) (restore func()) {
	old := lookupUserByName
	lookupUserByName = fn
	return func() { lookupUserByName = old }
}
