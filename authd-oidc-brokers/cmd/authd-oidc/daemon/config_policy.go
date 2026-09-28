package daemon

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const allowLegacyConfigMarker = ".allow-legacy-config-pre-0.5.0"

func allowLegacyConfigFromSnapData(snapData string) (bool, error) {
	if snapData == "" {
		return false, nil
	}
	if err := validateSnapDataDirectory(snapData); err != nil {
		return false, err
	}

	return configurationMarkerExists(snapData, allowLegacyConfigMarker, "legacy configuration marker")
}

func removeAllowLegacyConfigMarker(snapData string) (bool, error) {
	if snapData == "" {
		return false, nil
	}
	if err := validateSnapDataDirectory(snapData); err != nil {
		return false, err
	}

	exists, err := configurationMarkerExists(snapData, allowLegacyConfigMarker, "legacy configuration marker")
	if err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}

	markerPath := filepath.Join(snapData, allowLegacyConfigMarker)
	//nolint:gosec // SNAP_DATA is provided by snapd and the path is fixed within it.
	if err := os.Remove(markerPath); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("could not remove legacy configuration marker %q: %w", markerPath, err)
	}

	return true, nil
}

func validateSnapDataDirectory(snapData string) error {
	//nolint:gosec // SNAP_DATA is provided by snapd, not derived from broker.conf.
	dataDir, err := os.Lstat(snapData)
	if err != nil {
		return fmt.Errorf("could not inspect SNAP_DATA directory %q: %w", snapData, err)
	}
	if !dataDir.IsDir() {
		return fmt.Errorf("SNAP_DATA path %q is not a directory", snapData)
	}

	return nil
}

func configurationMarkerExists(snapData, markerName, description string) (bool, error) {
	markerPath := filepath.Join(snapData, markerName)
	//nolint:gosec // The marker path is constrained to the snapd-provided SNAP_DATA directory.
	marker, err := os.Lstat(markerPath)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("could not inspect %s %q: %w", description, markerPath, err)
	}
	if !marker.Mode().IsRegular() {
		return false, fmt.Errorf("%s %q is not a regular file", description, markerPath)
	}

	return true, nil
}
