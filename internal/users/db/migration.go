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

func backfillEmptyFullUsernames(q queryable) error {
	// A downgrade can write rows without full_username after the column migration has already run.
	// Do not claim a full username that another row already owns, since that would violate the
	// unique index and prevent authd from starting.
	_, err := q.Exec(`
		UPDATE users
		SET full_username = name
		WHERE (full_username IS NULL OR full_username = '')
		  AND NOT EXISTS (
			  SELECT 1
			  FROM users AS existing
			  WHERE existing.uid != users.uid
			    AND existing.full_username = users.name
		  )`)
	if err != nil {
		return fmt.Errorf("failed to populate 'full_username' column: %w", err)
	}
	return nil
}

var schemaMigrations = []schemaMigration{
	{
		description: "Migrate to lowercase user and group names",
		migrate: func(m *Manager) (err error) {
			// Start a transaction to ensure atomicity
			tx, err := m.db.Begin()
			if err != nil {
				return fmt.Errorf("failed to start transaction: %w", err)
			}

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
		migrate: func(m *Manager) (err error) {
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
	{
		description: "Add column 'provider_id' to users table for stable provider identifier",
		migrate: func(m *Manager) (err error) {
			tx, err := m.db.Begin()
			if err != nil {
				return fmt.Errorf("failed to start transaction: %w", err)
			}

			// Ensure the transaction is committed or rolled back
			defer func() {
				err = commitOrRollBackTransaction(err, tx)
			}()

			var exists bool
			err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM pragma_table_info('users') WHERE name = 'provider_id')").Scan(&exists)
			if err != nil {
				return fmt.Errorf("failed to check if 'provider_id' column exists: %w", err)
			}
			if !exists {
				if _, err = tx.Exec(`ALTER TABLE users ADD COLUMN provider_id TEXT DEFAULT ''`); err != nil {
					return fmt.Errorf("failed to add 'provider_id' column to users table: %w", err)
				}
			}

			// Partial unique index: only enforce uniqueness when broker ID and provider ID are non-empty,
			// allowing multiple existing users without provider identity (pre-migration state).
			// String literals use single quotes ('') so the predicate does not rely on SQLite's
			// legacy double-quoted-string fallback, where "" would be parsed as an identifier first.
			_, err = tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS "idx_user_broker_provider_id" ON users ("broker_id", "provider_id") WHERE broker_id != '' AND provider_id != ''`)
			if err != nil {
				return fmt.Errorf("failed to create provider ID index: %w", err)
			}
			if exists {
				log.Debug(context.Background(), "'provider_id' column already exists in users table, ensured provider ID index")
			}
			return nil
		},
	},
	{
		description: "Add column 'full_username' to users table",
		migrate: func(m *Manager) (err error) {
			tx, err := m.db.Begin()
			if err != nil {
				return fmt.Errorf("failed to start transaction: %w", err)
			}

			// Ensure the transaction is committed or rolled back
			defer func() {
				err = commitOrRollBackTransaction(err, tx)
			}()

			var exists bool
			err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM pragma_table_info('users') WHERE name = 'full_username')").Scan(&exists)
			if err != nil {
				return fmt.Errorf("failed to check if 'full_username' column exists: %w", err)
			}
			if !exists {
				if _, err = tx.Exec(`ALTER TABLE users ADD COLUMN full_username TEXT DEFAULT ''`); err != nil {
					return fmt.Errorf("failed to add 'full_username' column to users table: %w", err)
				}
			}

			// Until now users could only authenticate with their full username, so the
			// stored name is also their full username.
			if err = backfillEmptyFullUsernames(tx); err != nil {
				return err
			}

			var emptyCount int
			err = tx.QueryRow(`SELECT COUNT(*) FROM users WHERE full_username IS NULL OR full_username = ''`).Scan(&emptyCount)
			if err != nil {
				return fmt.Errorf("failed to check for empty 'full_username' values: %w", err)
			}
			if emptyCount > 0 {
				return fmt.Errorf("cannot create unique index: found %d user(s) with empty 'full_username'", emptyCount)
			}

			// Looking a user up by full username is the fallback of every name lookup that misses,
			// which includes the frequent NSS requests for users authd does not know about. Index
			// the column so those misses do not scan the whole table.
			//
			// The index is unique because the full username identifies the user: it is what maps
			// the name authd stores them under back to the name the brokers know them by, and it
			// authorises renaming a row between the two forms. Two rows claiming the same one would
			// make that mapping ambiguous, and the lookup would silently pick either of them.
			// Dropping any previous index first keeps the name free for the unique one.
			if _, err = tx.Exec(`DROP INDEX IF EXISTS "idx_user_full_username"`); err != nil {
				return fmt.Errorf("failed to drop the previous full username index: %w", err)
			}
			if _, err = tx.Exec(`CREATE UNIQUE INDEX "idx_user_full_username" ON users ("full_username")`); err != nil {
				return fmt.Errorf("failed to create full username index: %w", err)
			}

			return nil
		},
	},
	{
		description: "Backfill full usernames left empty by older authd versions",
		migrate: func(m *Manager) (err error) {
			tx, err := m.db.Begin()
			if err != nil {
				return fmt.Errorf("failed to start transaction: %w", err)
			}

			defer func() {
				err = commitOrRollBackTransaction(err, tx)
			}()

			return backfillEmptyFullUsernames(tx)
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
