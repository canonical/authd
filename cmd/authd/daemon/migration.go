package daemon

import (
	"context"
	"fmt"
	"os"

	"github.com/canonical/authd/internal/fileutils"
	"github.com/canonical/authd/log"
)

func maybeMigrateOldDBDir(oldPath, newPath string) error {
	exists, err := fileutils.FileExists(oldPath)
	if err != nil {
		// Let's not fail if we can't access the old database dir, but log a warning
		log.Warningf(context.Background(), "Can't access old database directory %q: %v", oldPath, err)
		return nil
	}
	if !exists {
		return nil
	}

	exists, err = fileutils.FileExists(newPath)
	if err != nil {
		// We can't continue if we can't access the new database dir
		return fmt.Errorf("can't access database directory %q: %w", newPath, err)
	}
	if exists {
		// Both the old and the new database directories exist, so we can't migrate
		log.Warningf(context.Background(), "Both old and new database directories exist, can't migrate %q to %q", oldPath, newPath)
		return nil
	}

	if err := os.Rename(oldPath, newPath); err != nil {
		return fmt.Errorf("can't migrate database directory from %q to %q: %w", oldPath, newPath, err)
	}

	log.Infof(context.Background(), "Migrated database directory from %q to %q", oldPath, newPath)

	return nil
}
