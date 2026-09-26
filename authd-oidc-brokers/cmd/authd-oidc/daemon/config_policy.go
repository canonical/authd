package daemon

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const strictConfigValidationMarker = "strict-config-validation"

func allowLegacyConfigFromSnapData(snapData string) (bool, error) {
	if snapData == "" {
		return false, nil
	}

	//nolint:gosec // SNAP_DATA is provided by snapd, not derived from broker.conf.
	dataDir, err := os.Lstat(snapData)
	if err != nil {
		return false, fmt.Errorf("could not inspect SNAP_DATA directory %q: %w", snapData, err)
	}
	if !dataDir.IsDir() {
		return false, fmt.Errorf("SNAP_DATA path %q is not a directory", snapData)
	}

	markerPath := filepath.Join(snapData, strictConfigValidationMarker)
	//nolint:gosec // The marker path is constrained to the snapd-provided SNAP_DATA directory.
	marker, err := os.Lstat(markerPath)
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("could not inspect strict configuration marker %q: %w", markerPath, err)
	}
	if !marker.Mode().IsRegular() {
		return false, fmt.Errorf("strict configuration marker %q is not a regular file", markerPath)
	}

	return false, nil
}
