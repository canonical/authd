//go:build pam_debug

package adapter

func init() {
	debugMessageFormatter = testMessageFormatter
}
