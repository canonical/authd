package pam

// Re-export DefaultConfig fields for use in tests.
var (
	AuthFailDelayThreshold = DefaultConfig.AuthFailDelayThreshold
	AuthFailDelay          = DefaultConfig.AuthFailDelay
)
