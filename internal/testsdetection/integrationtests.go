// TiCS: disabled // Should only be built when running integration tests.

//go:build integrationtests

package testsdetection

func init() {
	integrationtests = true
}
