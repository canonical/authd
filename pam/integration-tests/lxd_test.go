package main_test

import (
	"os"
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
	testutils.RegisterLXDProvisionHook(warmupRustBuildCache)
}

func warmupGoBuildCache(t *testing.T, containerName string) {
	t.Helper()

	projectRoot := testutils.ProjectRoot()

	t.Logf("Pre-warming Go build cache in %s", containerName)

	// The build args come from the same helpers the tests use, so the cache
	// keys match and the warmup isn't wasted. The output binaries are discarded.
	builds := [][]string{
		// authd daemon — built by TestMain via BuildAuthdWithExampleBroker.
		testutils.AuthdGoBuildArgs(os.DevNull),
		// PAM exec child — built by prepareSSHTests via buildPAMExecChild.
		pamExecChildGoBuildArgs(os.DevNull),
		// authctl — built by TestSSHAuthenticate via BuildAuthctl.
		testutils.AuthctlGoBuildArgs(os.DevNull),
	}
	for _, args := range builds {
		testutils.LXCExecFromDir(t, containerName, projectRoot, append([]string{"go"}, args...)...)
	}

	t.Logf("Go build cache pre-warmed in %s", containerName)
}

func warmupRustBuildCache(t *testing.T, containerName string) {
	t.Helper()

	projectRoot := testutils.ProjectRoot()

	t.Logf("Pre-warming Rust NSS build cache in %s", containerName)

	// NSS library — built by prepareSSHTests via BuildRustNSSLib. The build args
	// come from the same helper so the features and target dir (and thus the
	// cache key) match.
	args := append([]string{"cargo"}, testutils.RustNSSBuildArgs(testutils.LXDRustTargetDir)...)
	testutils.LXCExecFromDirAsUser(t, containerName, projectRoot,
		testutils.LXDUbuntuUserID, testutils.LXDUbuntuUserID,
		append([]string{"env", "HOME=/home/ubuntu"}, args...)...)

	t.Logf("Rust NSS build cache pre-warmed in %s", containerName)
}
