package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/canonical/authd/log"
)

const allUserColumns = "name, uid, gid, gecos, dir, shell, broker_id, locked"
const publicUserColumns = "name, uid, gid, gecos, dir, shell, broker_id, locked"
const allUserColumnsWithPlaceholders = "name = ?, uid = ?, gid = ?, gecos = ?, dir = ?, shell = ?, broker_id = ?, locked = ?"

// UserRow represents a user row in the database.
type UserRow struct {
	Name  string
	UID   uint32
	GID   uint32
	Gecos string // Gecos is an optional field. It can be empty.
	Dir   string
	Shell string

	// BrokerID specifies the broker the user last successfully authenticated with.
	BrokerID string `yaml:"broker_id,omitempty"`

	Locked bool `yaml:"locked,omitempty"`
}

// NewUserRow creates a new UserRow.
func NewUserRow(name string, uid, gid uint32, gecos, dir, shell string) UserRow {
	return UserRow{
		Name:  name,
		UID:   uid,
		GID:   gid,
		Gecos: gecos,
		Dir:   dir,
		Shell: shell,
	}
}

// UserByID returns a user matching this uid or an error if the database is corrupted or no entry was found.
func (m *Manager) UserByID(uid uint32) (UserRow, error) {
	return userByID(m.db, uid)
}

func userByID(db queryable, uid uint32) (UserRow, error) {
	query := fmt.Sprintf(`SELECT %s FROM users WHERE uid = ?`, publicUserColumns)
	row := db.QueryRow(query, uid)

	var u UserRow
	err := row.Scan(&u.Name, &u.UID, &u.GID, &u.Gecos, &u.Dir, &u.Shell, &u.BrokerID, &u.Locked)
	if errors.Is(err, sql.ErrNoRows) {
		return UserRow{}, NewUIDNotFoundError(uid)
	}
	if err != nil {
		return UserRow{}, fmt.Errorf("query error: %w", err)
	}

	return u, nil
}

// UserByName returns a user matching this name or an error if the database is corrupted or no entry was found.
func (m *Manager) UserByName(name string) (UserRow, error) {
	return userByName(m.db, name)
}

func userByName(db queryable, name string) (UserRow, error) {
	query := fmt.Sprintf(`SELECT %s FROM users WHERE name = ?`, publicUserColumns)
	row := db.QueryRow(query, name)

	var u UserRow
	err := row.Scan(&u.Name, &u.UID, &u.GID, &u.Gecos, &u.Dir, &u.Shell, &u.BrokerID, &u.Locked)
	if errors.Is(err, sql.ErrNoRows) {
		return UserRow{}, NewUserNotFoundError(name)
	}
	if err != nil {
		return UserRow{}, fmt.Errorf("query error: %w", err)
	}

	return u, nil
}

// AllUsers returns all users or an error if the database is corrupted.
func (m *Manager) AllUsers() ([]UserRow, error) {
	return allUsers(m.db)
}

func allUsers(db queryable) ([]UserRow, error) {
	query := fmt.Sprintf(`SELECT %s FROM users`, allUserColumns)
	rows, err := db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("query error: %w", err)
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

	// Check for errors from iteration
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return users, nil
}

// insertOrUpdateUserByID inserts or, if a user with the same name or UID already exists, updates the user in the database.
func insertOrUpdateUserByID(db queryable, u UserRow) error {
	exists, err := userExists(db, u)
	if err != nil {
		return fmt.Errorf("failed to check if user exists: %w", err)
	}

	if !exists {
		return insertUser(db, u)
	}

	return updateUserByID(db, u)
}

// userExists checks if a user with the same name or UID already exists in the database.
func userExists(db queryable, u UserRow) (bool, error) {
	query := `
		SELECT 1 FROM users 
		WHERE name = ? OR uid = ? 
		LIMIT 1`
	row := db.QueryRow(query, u.Name, u.UID)

	var exists int
	err := row.Scan(&exists)

	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("query error: %w", err)
	}

	return true, nil
}

// insertUser inserts a new user into the database.
func insertUser(db queryable, u UserRow) error {
	log.Debugf(context.Background(), "Inserting user %v", u.Name)
	query := fmt.Sprintf(`INSERT INTO users (%s) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, allUserColumns)
	_, err := db.Exec(query, u.Name, u.UID, u.GID, u.Gecos, u.Dir, u.Shell, u.BrokerID, u.Locked)
	if err != nil {
		return fmt.Errorf("insert user error: %w", err)
	}
	return nil
}

// updateUserByID updates the user with the same UID in the database.
func updateUserByID(db queryable, u UserRow) error {
	log.Debugf(context.Background(), "Updating user %v", u.Name)
	query := fmt.Sprintf(`UPDATE users SET %s WHERE uid = ?`, allUserColumnsWithPlaceholders)
	_, err := db.Exec(query, u.Name, u.UID, u.GID, u.Gecos, u.Dir, u.Shell, u.BrokerID, u.Locked, u.UID)
	if err != nil {
		return fmt.Errorf("update user error: %w", err)
	}
	return nil
}

// DeleteUser removes the user from the database.
func (m *Manager) DeleteUser(uid uint32) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	query := `DELETE FROM users WHERE uid = ?`
	res, err := m.db.Exec(query, uid)
	if err != nil {
		return fmt.Errorf("failed to delete user: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return NewUIDNotFoundError(uid)
	}

	return nil
}

// UserWithGroups returns a user and their groups, including local groups, in a single transaction.
func (m *Manager) UserWithGroups(name string) (u UserRow, groups []GroupRow, localGroups []string, err error) {
	// Start a transaction
	tx, err := m.db.Begin()
	if err != nil {
		return UserRow{}, nil, nil, fmt.Errorf("failed to start transaction: %w", err)
	}

	// Ensure the transaction is committed or rolled back
	defer func() {
		err = commitOrRollBackTransaction(err, tx)
	}()

	u, err = userByName(tx, name)
	if err != nil {
		return UserRow{}, nil, nil, err
	}

	groups, err = userGroups(tx, u.UID)
	if err != nil {
		return UserRow{}, nil, nil, fmt.Errorf("failed to get groups: %w", err)
	}

	localGroups, err = userLocalGroups(tx, u.UID)
	if err != nil {
		return UserRow{}, nil, nil, fmt.Errorf("failed to get local groups: %w", err)
	}

	return u, groups, localGroups, nil
}
