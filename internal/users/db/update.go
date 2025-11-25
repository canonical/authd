package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/canonical/authd/log"
	"github.com/mattn/go-sqlite3"
)

// UpdateUserEntry inserts or updates user and group records from the user information.
func (m *Manager) UpdateUserEntry(user UserRow, authdGroups []GroupRow, localGroups []string) (err error) {
	// Start a transaction
	tx, err := m.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}

	// Ensure the transaction is committed or rolled back
	defer func() {
		err = commitOrRollBackTransaction(err, tx)
	}()

	/* 1. Handle user update */
	if err := handleUserUpdate(tx, user); err != nil {
		return err
	}

	/* 2. Handle groups update */
	if err := handleGroupsUpdate(tx, authdGroups); err != nil {
		return err
	}

	/* 3. Update the users to groups table  */
	if err := handleUsersToGroupsUpdate(tx, user.UID, authdGroups); err != nil {
		return err
	}

	/* 4. Update user to local groups table */
	if err := handleUsersToLocalGroupsUpdate(tx, user.UID, localGroups); err != nil {
		return err
	}

	return nil
}

// handleUserUpdate updates the user record in the database.
func handleUserUpdate(db queryable, u UserRow) error {
	log.Debugf(context.Background(), "Updating entry of user %q (UID: %d)", u.Name, u.UID)

	existingUser, err := userByID(db, u.UID)
	if err != nil && !errors.Is(err, NoDataFoundError{}) {
		return err
	}

	// If a user with the same UID exists, we need to ensure that it's the same user or fail the update otherwise.
	if existingUser.Name != "" && existingUser.Name != u.Name {
		log.Errorf(context.TODO(), "UID %d for user %q already in use by user %q",
			u.UID, u.Name, existingUser.Name)
		return fmt.Errorf("UID for user %q already in use by a different user %q",
			u.Name, existingUser.Name)
	}

	// Ensure that we use the same homedir as the one we have in the database.
	if existingUser.Dir != "" && existingUser.Dir != u.Dir {
		log.Warningf(context.TODO(), "User %q already has a homedir. The existing %q one will be kept instead of %q", u.Name, existingUser.Dir, u.Dir)
		u.Dir = existingUser.Dir
	}

	// Ensure that we use the same shell as the one we have in the database.
	if existingUser.Shell != "" && existingUser.Shell != u.Shell {
		log.Debugf(context.TODO(), "Not updating shell to %q because it's already set to %q", u.Shell, existingUser.Shell)
		u.Shell = existingUser.Shell
	}

	return insertOrUpdateUserByID(db, u)
}

// updateGroupByID updates the group records in the database.
func handleGroupsUpdate(db queryable, groups []GroupRow) error {
	for _, group := range groups {
		existingGroup, err := groupByID(db, group.GID)
		if err != nil && !errors.Is(err, NoDataFoundError{}) {
			return err
		}
		groupExists := !errors.Is(err, NoDataFoundError{})

		// If a group with the same GID exists, we need to ensure that it's the same group or fail the update otherwise.
		// Ignore the case that the UGID of the existing group is empty, which means that the group was stored without a
		// UGID, which was the case before https://github.com/canonical/authd/pull/647.
		if groupExists && existingGroup.UGID != "" && existingGroup.UGID != group.UGID {
			log.Errorf(context.TODO(), "GID %d for group with UGID %q already in use by a group with UGID %q", group.GID, group.UGID, existingGroup.UGID)
			return fmt.Errorf("GID for group %q already in use by a different group %q",
				group.Name, existingGroup.Name)
		}

		log.Debugf(context.Background(), "Updating entry of group %q (%+v)", group.Name, group)
		if err := insertOrUpdateGroupByID(db, group); err != nil {
			return err
		}
	}

	return nil
}

// handleUsersToGroupsUpdate updates the users_to_groups table. It adds the user to the groups they're a member of and
// removes it from all other groups.
func handleUsersToGroupsUpdate(db queryable, uid uint32, groups []GroupRow) error {
	// Remove the user from all groups
	err := removeUserFromAllGroups(db, uid)
	if err != nil && !errors.Is(err, NoDataFoundError{}) {
		return fmt.Errorf("failed to remove user from groups: %w", err)
	}

	// Add the user to the groups they're a member of
	for _, group := range groups {
		if err := addUserToGroup(db, uid, group.GID); err != nil {
			var sqliteErr sqlite3.Error
			if errors.As(err, &sqliteErr) && sqliteErr.ExtendedCode == sqlite3.ErrConstraintForeignKey {
				// A FOREIGN KEY constraint failed. The SQLite error does not tell us which column caused the constraint
				// to fail, so to make the error message more useful, we check if a user and group with the given UID and
				// GID exist.
				_, userErr := userByID(db, uid)
				if errors.Is(userErr, NoDataFoundError{}) {
					err = fmt.Errorf("%w (%w)", err, userErr)
				} else if userErr != nil {
					err = errors.Join(err, fmt.Errorf("failed to check if user with UID %d exists: %w", uid, userErr))
				}
				_, groupErr := groupByID(db, group.GID)
				if errors.Is(groupErr, NoDataFoundError{}) {
					err = fmt.Errorf("%w (%w)", err, groupErr)
				} else if groupErr != nil {
					err = errors.Join(err, fmt.Errorf("failed to check if group with GID %d exists: %w", group.GID, groupErr))
				}
			}
			return fmt.Errorf("failed to add user to group: %w", err)
		}
	}

	return nil
}

// handleUsersToLocalGroupsUpdate updates the users_to_local_groups table.
func handleUsersToLocalGroupsUpdate(db queryable, uid uint32, localGroups []string) error {
	// Remove the user from all local groups
	if err := removeUserFromAllLocalGroups(db, uid); err != nil {
		return fmt.Errorf("failed to remove user from local groups: %w", err)
	}

	// Add the user to the local groups
	for _, group := range localGroups {
		if err := addUserToLocalGroup(db, uid, group); err != nil {
			return fmt.Errorf("failed to add user to local group: %w", err)
		}
	}

	return nil
}

// UpdateBrokerForUser updates the last broker the user successfully authenticated with.
func (m *Manager) UpdateBrokerForUser(username, brokerID string) error {
	query := `UPDATE users SET broker_id = ? WHERE name = ?`
	res, err := m.db.Exec(query, brokerID, username)
	if err != nil {
		return fmt.Errorf("failed to update broker for user: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return NewUserNotFoundError(username)
	}

	return nil
}

// UpdateLockedFieldForUser sets the "locked" field of a user record.
func (m *Manager) UpdateLockedFieldForUser(username string, locked bool) error {
	query := `UPDATE users SET locked = ? WHERE name = ?`
	res, err := m.db.Exec(query, locked, username)
	if err != nil {
		return fmt.Errorf("failed to update locked field for user: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return NewUserNotFoundError(username)
	}

	return nil
}

// SetUserID updates the UID of a user.
func (m *Manager) SetUserID(username string, newUID uint32) error {
	// Temporarily disable foreign key constraints to allow updating the UID without violating constraints.
	// SQLite does not allow disabling foreign key constraints in a transaction,
	// so we do it before starting the transaction. See https://www.sqlite.org/foreignkeys.html#fk_enable
	if _, err := m.db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		return err
	}
	defer func() {
		// Re-enable foreign key constraints after the operation
		if _, err := m.db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
			log.Errorf(context.TODO(), "Failed to re-enable foreign keys: %v", err)
		}
	}()

	// Start a transaction
	tx, err := m.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}

	// Ensure the transaction is committed or rolled back
	defer func() {
		err = commitOrRollBackTransaction(err, tx)
	}()

	// Check if the new UID is already in use
	existingUser, err := userByID(tx, newUID)
	if err != nil && !errors.Is(err, NoDataFoundError{}) {
		return fmt.Errorf("failed to check if new UID is already in use: %w", err)
	}
	if existingUser.Name != "" && existingUser.Name != username {
		log.Errorf(context.TODO(), "UID %d already in use by user %q", newUID, existingUser.Name)
		return fmt.Errorf("UID %d already in use by a different user", newUID)
	}
	if existingUser.Name == username {
		log.Debugf(context.TODO(), "User %q already has UID %d, no update needed", username, newUID)
		return nil
	}

	// Get the old UID of the user
	oldUser, err := userByName(tx, username)
	if errors.Is(err, NoDataFoundError{}) {
		return err
	}
	if err != nil {
		return fmt.Errorf("failed to get user by name: %w", err)
	}
	oldUID := oldUser.UID

	// Update the users table
	if _, err := tx.Exec(`UPDATE users SET uid = ? WHERE name = ?`, newUID, username); err != nil {
		return err
	}

	// Update the users_to_groups table
	if _, err := tx.Exec(`UPDATE users_to_groups SET uid = ? WHERE uid = ?`, newUID, oldUID); err != nil {
		return err
	}

	// Update the users_to_local_groups table
	if _, err := tx.Exec(`UPDATE users_to_local_groups SET uid = ? WHERE uid = ?`, newUID, oldUID); err != nil {
		return err
	}

	return nil
}

// SetGroupID updates the GID of a group and returns the list of users whose primary group was updated.
func (m *Manager) SetGroupID(groupName string, newGID uint32) ([]UserRow, error) {
	// Temporarily disable foreign key constraints to allow updating the GID without violating constraints.
	// SQLite does not allow disabling foreign key constraints in a transaction,
	// so we do it before starting the transaction. See https://www.sqlite.org/foreignkeys.html#fk_enable
	if _, err := m.db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		return nil, err
	}
	defer func() {
		// Re-enable foreign key constraints after the operation
		if _, err := m.db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
			log.Errorf(context.TODO(), "Failed to re-enable foreign keys: %v", err)
		}
	}()

	// Start a transaction
	tx, err := m.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("failed to start transaction: %w", err)
	}

	// Ensure the transaction is committed or rolled back
	defer func() {
		err = commitOrRollBackTransaction(err, tx)
	}()

	// Check if the new GID is already in use
	existingGroup, err := groupByID(tx, newGID)
	if err != nil && !errors.Is(err, NoDataFoundError{}) {
		return nil, fmt.Errorf("failed to check if new GID is already in use: %w", err)
	}
	if existingGroup.Name != "" && existingGroup.Name != groupName {
		log.Errorf(context.TODO(), "GID %d already in use by group %q", newGID, existingGroup.Name)
		return nil, fmt.Errorf("GID %d already in use by a different group", newGID)
	}
	if existingGroup.Name == groupName {
		log.Debugf(context.TODO(), "Group %q already has GID %d, no update needed", groupName, newGID)
		return nil, nil
	}

	// Get the old GID of the group
	oldGroup, err := groupByName(tx, groupName)
	if errors.Is(err, NoDataFoundError{}) {
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get group by name: %w", err)
	}
	oldGID := oldGroup.GID

	// Get the list of users whose primary group is the old GID
	query := `SELECT name, uid, gid, gecos, dir, shell, broker_id, locked FROM users WHERE gid = ?`
	rows, err := tx.Query(query, oldGID)
	if err != nil {
		return nil, fmt.Errorf("failed to get users with old group as primary group: %w", err)
	}
	defer closeRows(rows)

	var users []UserRow
	for rows.Next() {
		var u UserRow
		err := rows.Scan(&u.Name, &u.UID, &u.GID, &u.Gecos, &u.Dir, &u.Shell, &u.BrokerID, &u.Locked)
		if err != nil {
			return nil, fmt.Errorf("scan error: %w", err)
		}
		users = append(users, u)
	}

	// Update the groups table
	if _, err := tx.Exec(`UPDATE groups SET gid = ? WHERE name = ?`, newGID, groupName); err != nil {
		return nil, err
	}

	// Update the primary groups of the users table
	if _, err := tx.Exec(`UPDATE users SET gid = ? WHERE gid = ?`, newGID, oldGID); err != nil {
		return nil, err
	}

	// Update the users_to_groups table
	if _, err := tx.Exec(`UPDATE users_to_groups SET gid = ? WHERE gid = ?`, newGID, oldGID); err != nil {
		// If a foreign key error occurs, enrich it similarly to users handling if needed.
		var sqliteErr sqlite3.Error
		if errors.As(err, &sqliteErr) && sqliteErr.ExtendedCode == sqlite3.ErrConstraintForeignKey {
			// Check existence to provide clearer message
			_, groupErr := groupByID(tx, newGID)
			if errors.Is(groupErr, NoDataFoundError{}) {
				err = fmt.Errorf("%w (%w)", err, groupErr)
			} else if groupErr != nil {
				err = errors.Join(err, fmt.Errorf("failed to check if group with GID %d exists: %w", newGID, groupErr))
			}
		}
		return nil, fmt.Errorf("failed to update users_to_groups for GID change: %w", err)
	}

	return users, nil
}
