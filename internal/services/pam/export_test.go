package pam

// Re-export DefaultConfig fields for use in tests.
var (
	AuthFailDelayThreshold = DefaultConfig.AuthFailDelayThreshold
	AuthFailDelay          = DefaultConfig.AuthFailDelay

	// AuthFailMaxTracked allows tests to override the tracker capacity.
	AuthFailMaxTracked = &authFailMaxTracked
)

// Z_ForTests_AddSessionLogin registers a session login as if a PAM transaction had selected a
// broker for the user. The broker mock derives session IDs from the username, so concurrent
// transactions for the same user cannot be distinguished through SelectBroker.
func Z_ForTests_AddSessionLogin(s *Service, sessionID, name, leaseID string) { //nolint:revive // Test-only exports use the Z_ForTests_ prefix.
	s.sessionLogins.Store(sessionID, sessionLogin{name: name, aliasLeaseID: leaseID})
}
