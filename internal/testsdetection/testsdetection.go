// Package testsdetection helps in deciding if we are currently running under integration or tests.
package testsdetection

import (
	"testing"
)

var integrationtests = false

// MustBeTesting panics if we are not running under tests or integration tests.
func MustBeTesting() {
	if !testing.Testing() && !integrationtests {
		panic("This can only be called in tests")
	}
}
