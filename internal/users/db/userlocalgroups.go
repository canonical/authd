package db

import (
	"fmt"
)

// UserLocalGroups returns all local groups for a given user or an error if the database is corrupted or no entry was found.
func (m *Manager) UserLocalGroups(uid uint32) ([]string, error) {
	return userLocalGroups(m.db, uid)
}

func userLocalGroups(db queryable, uid uint32) ([]string, error) {
	rows, err := db.Query(`SELECT group_name FROM users_to_local_groups WHERE uid = ?`, uid)
	if err != nil {
		return nil, fmt.Errorf("query error: %w", err)
	}
	defer closeRows(rows)

	var localGroups []string
	for rows.Next() {
		var groupName string
		if err := rows.Scan(&groupName); err != nil {
			return nil, fmt.Errorf("scan error: %w", err)
		}
		localGroups = append(localGroups, groupName)
	}

	// Check for errors from iteration
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return localGroups, nil
}

func addUserToLocalGroup(db queryable, uid uint32, groupName string) error {
	_, err := db.Exec(`INSERT INTO users_to_local_groups (uid, group_name) VALUES (?, ?)`, uid, groupName)
	if err != nil {
		return fmt.Errorf("failed to add user to local group: %w", err)
	}

	return nil
}

func removeUserFromAllLocalGroups(db queryable, uid uint32) error {
	_, err := db.Exec(`DELETE FROM users_to_local_groups WHERE uid = ?`, uid)
	return err
}
