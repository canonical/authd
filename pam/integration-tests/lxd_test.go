package main_test

import (
	"testing"

	"github.com/canonical/authd/internal/testutils"
)

func init() {
	// Pre-warm the Go build cache for the binaries that are expensive to
	// compile. This runs once when a new LXD container is provisioned, so that
	// when test cases run in parallel inside the container they all get cache
	// hits instead of redundant full builds concurrently.
	//
	// The flags must match what the tests use; mismatched flags produce a
	// different cache key and the warmup would be wasted.
	testutils.RegisterLXDProvisionHook(warmupGoBuildCache)
}

func warmupGoBuildCache(t *testing.T, containerName string) {
	t.Helper()

	projectRoot := testutils.ProjectRoot()

	t.Logf("Pre-warming Go build cache in %s", containerName)

	// authd daemon — built by TestMain via BuildAuthdWithExampleBroker.
	testutils.LXCExecFromDir(t, containerName, projectRoot,
		"go", "build",
		"-gcflags=all=-N -l",
		"-tags=withexamplebroker,integrationtests",
		"-o", "/dev/null",
		"./cmd/authd")

	// PAM exec child — built by prepareSSHTests via buildPAMExecChild.
	testutils.LXCExecFromDir(t, containerName, projectRoot,
		"go", "build",
		"-gcflags=all=-N -l",
		"-tags=pam_debug",
		"-o", "/dev/null",
		"./pam")

	// authctl — built by TestSSHAuthenticate via BuildAuthctl.
	testutils.LXCExecFromDir(t, containerName, projectRoot,
		"go", "build",
		"-gcflags=all=-N -l",
		"-o", "/dev/null",
		"./cmd/authctl")

	t.Logf("Go build cache pre-warmed in %s", containerName)
}
