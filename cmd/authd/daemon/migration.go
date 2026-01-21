package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/canonical/authd/internal/consts"
	"github.com/canonical/authd/internal/fileutils"
	"github.com/canonical/authd/internal/users/db"
	"github.com/canonical/authd/internal/users/db/bbolt"
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

func maybeMigrateBBoltToSQLite(dbDir string) (migrated bool, err error) {
	bboltPath := filepath.Join(dbDir, bbolt.DBFilename())
	sqlitePath := filepath.Join(dbDir, consts.DefaultDatabaseFileName)

	exists, err := fileutils.FileExists(bboltPath)
	if err != nil {
		// Let's not fail if we can't access the old bbolt database, but log a warning
		log.Warningf(context.Background(), "Error checking for existing bbolt database %q: %v", bboltPath, err)
		return false, nil
	}
	if !exists {
		// Nothing to migrate
		return false, nil
	}

	exists, err = fileutils.FileExists(sqlitePath)
	if err != nil {
		return false, fmt.Errorf("error checking for existing SQLite database %q: %w", sqlitePath, err)
	}
	if exists {
		// Both the bbolt and the SQLite databases exist, so we can't migrate
		log.Warningf(context.Background(), "Both bbolt and SQLite databases exist in %q, can't migrate", dbDir)
		return false, nil
	}

	if err := db.MigrateFromBBoltToSQLite(dbDir); err != nil {
		return false, fmt.Errorf("failed to migrate data: %w", err)
	}

	// Remove the bbolt database
	if err := bbolt.RemoveDb(dbDir); err != nil {
		log.Warningf(context.Background(), "Failed to remove bbolt database: %v", err)
	}

	return true, nil
}
