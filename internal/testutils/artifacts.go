package testutils

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/canonical/authd/internal/fileutils"
	"github.com/stretchr/testify/require"
)

const (
	alwaysSaveArtifactsEnvVar = "AUTHD_TESTS_ARTIFACTS_ALWAYS_SAVE"
)

var (
	authdTestSessionTime  = time.Now()
	authdArtifactsDir     string
	authdArtifactsDirOnce sync.Once
)

// ArtifactsDir returns the path to the directory where artifacts are stored.
func ArtifactsDir(t *testing.T) string {
	t.Helper()

	authdArtifactsDirOnce.Do(func() {
		authdArtifactsDir = createArtifactsDir(t)
		t.Logf("Test artifacts directory: %s", authdArtifactsDir)
	})

	return authdArtifactsDir
}

func createArtifactsDir(t *testing.T) string {
	t.Helper()

	// We need to copy the artifacts to another directory, since the test directory will be cleaned up.
	if dir := os.Getenv("AUTHD_TESTS_ARTIFACTS_PATH"); dir != "" {
		if err := os.MkdirAll(dir, 0750); err != nil && !os.IsExist(err) {
			require.NoError(t, err, "Teardown: could not create artifacts directory %q", authdArtifactsDir)
		}
		return dir
	}

	st := authdTestSessionTime
	dirName := fmt.Sprintf("authd-test-artifacts-%d-%02d-%02dT%02d-%02d-%02d.%d-",
		st.Year(), st.Month(), st.Day(), st.Hour(), st.Minute(), st.Second(),
		st.UnixMilli())

	dir, err := os.MkdirTemp(os.TempDir(), dirName)
	require.NoError(t, err, "Teardown: could not create artifacts directory %q", authdArtifactsDir)

	return dir
}

// saveFilesAsArtifacts saves the specified artifacts to a temporary directory if the test failed.
func saveFilesAsArtifacts(t *testing.T, artifacts ...string) {
	t.Helper()

	dir := filepath.Join(ArtifactsDir(t), t.Name())
	err := os.MkdirAll(dir, 0750)
	require.NoError(t, err, "Teardown: could not create artifacts directory %q", dir)

	// Copy the artifacts to the artifacts directory.
	for _, artifact := range artifacts {
		target := filepath.Join(dir, filepath.Base(artifact))
		t.Logf("Teardown: Saving artifact: %q", target)
		err = fileutils.CopyFile(artifact, target)
		if err != nil {
			t.Logf("Teardown: failed to copy artifact %q to %q: %v", artifact, dir, err)
		}
	}
}

// MaybeSaveFilesAsArtifactsOnCleanup saves the specified artifacts to a temporary directory if the test failed
// or if the AUTHD_TESTS_ARTIFACTS_ALWAYS_SAVE environment variable is set.
func MaybeSaveFilesAsArtifactsOnCleanup(t *testing.T, artifacts ...string) {
	t.Helper()

	t.Cleanup(func() {
		if !t.Failed() && os.Getenv(alwaysSaveArtifactsEnvVar) == "" {
			return
		}
		saveFilesAsArtifacts(t, artifacts...)
	})
}

func saveBytesAsArtifact(t *testing.T, content []byte, filename string) {
	t.Helper()

	dir := filepath.Join(ArtifactsDir(t), t.Name())
	err := os.MkdirAll(dir, 0750)
	require.NoError(t, err, "Teardown: could not create artifacts directory %q", dir)

	target := filepath.Join(dir, filename)
	t.Logf("Teardown: Writing artifact: %q", target)

	// Write the bytes to the artifacts directory.
	err = os.WriteFile(target, content, 0600)
	if err != nil {
		t.Logf("Teardown: failed to write artifact %q to %q: %v", filename, dir, err)
	}
}

// MaybeSaveBytesAsArtifactOnCleanup saves the specified bytes to a temporary directory if the test failed
// or if the AUTHD_TESTS_ARTIFACTS_ALWAYS_SAVE environment variable is set.
func MaybeSaveBytesAsArtifactOnCleanup(t *testing.T, content []byte, filename string) {
	t.Helper()

	t.Cleanup(func() {
		if !t.Failed() && os.Getenv(alwaysSaveArtifactsEnvVar) == "" {
			return
		}
		saveBytesAsArtifact(t, content, filename)
	})
}

// MaybeSaveBufferAsArtifactOnCleanup saves the specified buffer to a temporary directory if the test failed
// or if the AUTHD_TESTS_ARTIFACTS_ALWAYS_SAVE environment variable is set.
func MaybeSaveBufferAsArtifactOnCleanup(t *testing.T, buf *SyncBuffer, filename string) {
	t.Helper()

	t.Cleanup(func() {
		if !t.Failed() && os.Getenv(alwaysSaveArtifactsEnvVar) == "" {
			return
		}
		saveBytesAsArtifact(t, buf.Bytes(), filename)
	})
}
