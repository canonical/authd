//go:build withmsentraid

package himmelblau

import "testing"

// SetAuthorityBaseURL sets the base URL for the token authority, used in tests to override the default.
// This is not thread-safe.
func SetAuthorityBaseURL(_ *testing.T, url string) {
	authorityBaseURLMu.Lock()
	authorityBaseURL = url
	authorityBaseURLMu.Unlock()
}
