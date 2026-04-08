package db

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/canonical/authd/internal/decorate"
	"github.com/canonical/authd/internal/users/localentries"
	"github.com/canonical/authd/log"
)

type schemaMigration struct {
	description string
	migrate     func(*Manager) error
}

var schemaMigrations = []schemaMigration{
	{
		description: "Migrate to lowercase user and group names",
		migrate: func(m *Manager) error {
			// Start a transaction to ensure atomicity
			tx, err := m.db.Begin()

			// Ensure the transaction is committed or rolled back
			defer func() {
				err = commitOrRollBackTransaction(err, tx)
			}()

			rows, err := tx.Query(`SELECT name FROM users`)
			if err != nil {
				return fmt.Errorf("failed to get users from database: %w", err)
			}
			defer rows.Close()

			var oldNames, newNames []string
			for rows.Next() {
				var name string
				if err := rows.Scan(&name); err != nil {
					return fmt.Errorf("failed to scan user name: %w", err)
				}
				oldNames = append(oldNames, name)
				newNames = append(newNames, strings.ToLower(name))
			}

			if err := renameUsersInGroupFile(oldNames, newNames); err != nil {
				return fmt.Errorf("failed to rename users in %s file: %w",
					localentries.GroupFile, err)
			}

			// Delete groups that would cause unique constraint violations
			if err := removeGroupsWithNameConflicts(tx); err != nil {
				return fmt.Errorf("failed to remove groups with name conflicts: %w", err)
			}

			query := `UPDATE users SET name = LOWER(name);
					  UPDATE groups SET ugid = LOWER(ugid) WHERE ugid = name;
					  UPDATE groups SET name = LOWER(name);`
			_, err = tx.Exec(query)
			return err
		},
	},
	{
		description: "Add column 'locked' to users table",
		migrate: func(m *Manager) error {
			// Start a transaction to ensure atomicity
			tx, err := m.db.Begin()
			if err != nil {
				return fmt.Errorf("failed to start transaction: %w", err)
			}

			// Ensure the transaction is committed or rolled back
			defer func() {
				err = commitOrRollBackTransaction(err, tx)
			}()

			// Check if the 'locked' column already exists
			var exists bool
			err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM pragma_table_info('users') WHERE name = 'locked')").Scan(&exists)
			if err != nil {
				return fmt.Errorf("failed to check if 'locked' column exists: %w", err)
			}
			if exists {
				log.Debug(context.Background(), "'locked' column already exists in users table, skipping migration")
				return nil
			}

			// Add the 'locked' column to the users table
			_, err = tx.Exec("ALTER TABLE users ADD COLUMN locked BOOLEAN DEFAULT FALSE")
			if err != nil {
				return fmt.Errorf("failed to add 'locked' column to users table: %w", err)
			}

			return nil
		},
	},
}

func (m *Manager) maybeApplyMigrations() error {
	currentVersion, err := getSchemaVersion(m.db)
	if err != nil {
		return err
	}

	if currentVersion >= len(schemaMigrations) {
		return nil
	}

	log.Debugf(context.Background(), "Schema version before migrations: %d", currentVersion)

	v := 0
	for _, migration := range schemaMigrations {
		v++
		if currentVersion >= v {
			continue
		}

		log.Infof(context.Background(), "Applying schema migration: %s", migration.description)
		if err := migration.migrate(m); err != nil {
			return fmt.Errorf("error applying schema migration: %w", err)
		}

		if err := setSchemaVersion(m.db, v); err != nil {
			return fmt.Errorf("failed to update schema version: %w", err)
		}
	}

	log.Debugf(context.Background(), "Schema version after migrations: %d", v)

	return nil
}

// renameUsersInGroupFile renames users in the /etc/group file.
func renameUsersInGroupFile(oldNames, newNames []string) (err error) {
	decorate.OnError(&err, "failed to rename users in local groups: %v -> %v",
		oldNames, newNames)

	log.Debugf(context.Background(), "Renaming users in local groups: %v -> %v",
		oldNames, newNames)

	if len(oldNames) == 0 && len(newNames) == 0 {
		// Nothing to do.
		return nil
	}

	entries, entriesUnlock, err := localentries.WithUserDBLock()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, entriesUnlock()) }()

	groups, err := localentries.GetGroupEntries(entries)
	if err != nil {
		return err
	}
	for idx, group := range groups {
		for j, user := range group.Users {
			for k, oldName := range oldNames {
				if user == oldName {
					groups[idx].Users[j] = newNames[k]
				}
			}
		}
	}

	return localentries.SaveGroupEntries(entries, groups)
}

func removeGroupsWithNameConflicts(db queryable) error {
	// Delete groups with conflicting names
	rows, err := db.Query(`
		SELECT name FROM groups
		WHERE rowid NOT IN (
			SELECT MIN(rowid)
			FROM groups
			GROUP BY LOWER(name)
		);`)
	if err != nil {
		return fmt.Errorf("failed to query for groups with name conflicts: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return fmt.Errorf("failed to scan group name: %w", err)
		}

		log.Noticef(context.Background(), "Deleting group due to name conflict: %s", name)
		if _, err := db.Exec("DELETE FROM groups WHERE name = ?", name); err != nil {
			return fmt.Errorf("failed to delete group %s: %w", name, err)
		}
	}

	return nil
}
