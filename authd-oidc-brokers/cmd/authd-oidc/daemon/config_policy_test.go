package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAllowLegacyConfigFromSnapData(t *testing.T) {
	t.Run("non_snap_is_strict", func(t *testing.T) {
		allowLegacy, err := allowLegacyConfigFromSnapData("")
		require.NoError(t, err)
		require.False(t, allowLegacy)
	})

	t.Run("missing_marker_allows_legacy_config", func(t *testing.T) {
		allowLegacy, err := allowLegacyConfigFromSnapData(t.TempDir())
		require.NoError(t, err)
		require.True(t, allowLegacy)
	})

	t.Run("present_marker_enables_strict_config", func(t *testing.T) {
		snapData := t.TempDir()
		markerPath := filepath.Join(snapData, strictConfigValidationMarker)
		require.NoError(t, os.WriteFile(markerPath, nil, 0600))

		allowLegacy, err := allowLegacyConfigFromSnapData(snapData)
		require.NoError(t, err)
		require.False(t, allowLegacy)
	})

	t.Run("marker_symlink_is_not_trusted", func(t *testing.T) {
		snapData := t.TempDir()
		target := filepath.Join(snapData, "target")
		require.NoError(t, os.WriteFile(target, nil, 0600))
		markerPath := filepath.Join(snapData, strictConfigValidationMarker)
		require.NoError(t, os.Symlink(target, markerPath))

		_, err := allowLegacyConfigFromSnapData(snapData)
		require.ErrorContains(t, err, "is not a regular file")
	})

	t.Run("marker_directory_is_invalid", func(t *testing.T) {
		snapData := t.TempDir()
		markerPath := filepath.Join(snapData, strictConfigValidationMarker)
		require.NoError(t, os.Mkdir(markerPath, 0700))

		_, err := allowLegacyConfigFromSnapData(snapData)
		require.ErrorContains(t, err, "is not a regular file")
	})

	t.Run("missing_snap_data_is_an_error", func(t *testing.T) {
		missingPath := filepath.Join(t.TempDir(), "missing")

		_, err := allowLegacyConfigFromSnapData(missingPath)
		require.ErrorContains(t, err, "could not inspect SNAP_DATA directory")
	})

	t.Run("snap_data_symlink_is_not_trusted", func(t *testing.T) {
		target := t.TempDir()
		snapData := filepath.Join(t.TempDir(), "snap-data")
		require.NoError(t, os.Symlink(target, snapData))

		_, err := allowLegacyConfigFromSnapData(snapData)
		require.ErrorContains(t, err, "is not a directory")
	})
}
