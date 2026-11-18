package pam

// Re-export DefaultConfig fields for use in tests.
var (
	AuthFailDelayThreshold = DefaultConfig.AuthFailDelayThreshold
	AuthFailDelay          = DefaultConfig.AuthFailDelay

	// AuthFailMaxTracked allows tests to override the tracker capacity.
	AuthFailMaxTracked = &authFailMaxTracked
)

// PAMServiceName exposes pamServiceName for tests.
var PAMServiceName = pamServiceName

// SetPAMDDirsForTests overrides the directories containing PAM service configuration files.
func SetPAMDDirsForTests(dirs []string) (restore func()) {
	prev := pamDDirs
	pamDDirs = dirs
	return func() { pamDDirs = prev }
}
