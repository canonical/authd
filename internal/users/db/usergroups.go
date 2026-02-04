package db

import (
	"context"
	"fmt"

	"github.com/canonical/authd/log"
)

// UserGroups returns all groups for a given user or an error if the database is corrupted or no entry was found.
func (m *Manager) UserGroups(uid uint32) ([]GroupRow, error) {
	return userGroups(m.db, uid)
}

func userGroups(db queryable, uid uint32) ([]GroupRow, error) {
	query := `
		SELECT g.name, g.gid, g.ugid 
		FROM users_to_groups ug
		JOIN groups g ON ug.gid = g.gid
		WHERE ug.uid = ?
	`
	rows, err := db.Query(query, uid)
	if err != nil {
		return nil, fmt.Errorf("query error: %w", err)
	}
	defer closeRows(rows)

	var groups []GroupRow
	for rows.Next() {
		var g GroupRow
		err := rows.Scan(&g.Name, &g.GID, &g.UGID)
		if err != nil {
			return nil, fmt.Errorf("scan error: %w", err)
		}

		groups = append(groups, g)
	}

	// Check for errors from iteration
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	if len(groups) == 0 {
		return nil, NewUIDNotFoundError(uid)
	}

	return groups, nil
}

// RemoveUserFromGroup removes a user from a group.
func (m *Manager) RemoveUserFromGroup(uid, gid uint32) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	query := `DELETE FROM users_to_groups WHERE uid = ? AND gid = ?`
	_, err := m.db.Exec(query, uid, gid)
	return err
}

func allUserGroupsInternal(db queryable) ([]userToGroupRow, error) {
	query := `SELECT uid, gid FROM users_to_groups`
	rows, err := db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("query error: %w", err)
	}
	defer closeRows(rows)

	var userGroups []userToGroupRow
	for rows.Next() {
		var ug userToGroupRow
		err := rows.Scan(&ug.UID, &ug.GID)
		if err != nil {
			return nil, fmt.Errorf("scan error: %w", err)
		}
		userGroups = append(userGroups, ug)
	}

	// Check for errors from iteration
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return userGroups, nil
}

func getGroupMembers(db queryable, gid uint32) ([]string, error) {
	var members []string
	query := `
			SELECT u.name 
			FROM users_to_groups ug
			JOIN users u ON ug.uid = u.uid
			WHERE ug.gid = ?
		`
	rows, err := db.Query(query, gid)
	if err != nil {
		return nil, fmt.Errorf("query error while fetching users: %w", err)
	}
	defer closeRows(rows)

	for rows.Next() {
		var userName string
		if err := rows.Scan(&userName); err != nil {
			return nil, fmt.Errorf("scan error while fetching users: %w", err)
		}
		members = append(members, userName)
	}

	// Check for errors from iteration
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error while fetching users: %w", err)
	}

	return members, nil
}

func removeUserFromAllGroups(db queryable, uid uint32) error {
	res, err := db.Exec(`DELETE FROM users_to_groups WHERE uid = ?`, uid)
	if err != nil {
		return fmt.Errorf("query error: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return NoDataFoundError{fmt.Sprintf("no groups found for user with UID %d", uid)}
	}

	return nil
}

func addUserToGroup(db queryable, uid, gid uint32) error {
	log.Debugf(context.Background(), "Adding user %d to group %d", uid, gid)
	_, err := db.Exec(`INSERT INTO users_to_groups (uid, gid) VALUES (?, ?)`, uid, gid)
	return err
}
