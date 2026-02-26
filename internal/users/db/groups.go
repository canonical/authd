package db

import (
	"database/sql"
	"errors"
	"fmt"
)

// GroupRow represents a group in the database.
type GroupRow struct {
	Name string
	GID  uint32
	UGID string
}

// GroupWithMembers is a GroupRow with a list of users that are members of the group.
type GroupWithMembers struct {
	GroupRow `yaml:",inline"`
	Users    []string
}

type userToGroupRow struct {
	UID uint32
	GID uint32
}

// NewGroupRow creates a new GroupRow.
func NewGroupRow(name string, gid uint32, ugid string) GroupRow {
	return GroupRow{
		Name: name,
		GID:  gid,
		UGID: ugid,
	}
}

// GroupByID returns the group with the given group ID or a NoDataFoundError if no group was found.
func (m *Manager) GroupByID(gid uint32) (GroupRow, error) {
	return groupByID(m.db, gid)
}

func groupByID(db queryable, gid uint32) (GroupRow, error) {
	query := `SELECT name, gid, ugid FROM groups WHERE gid = ?`
	row := db.QueryRow(query, gid)

	var g GroupRow
	err := row.Scan(&g.Name, &g.GID, &g.UGID)
	if errors.Is(err, sql.ErrNoRows) {
		return GroupRow{}, NewGIDNotFoundError(gid)
	}
	if err != nil {
		return GroupRow{}, fmt.Errorf("query error: %w", err)
	}

	return g, nil
}

// GroupWithMembersByID returns the group with the given group ID with a list of users that are members of the group.
func (m *Manager) GroupWithMembersByID(gid uint32) (_ GroupWithMembers, err error) {
	// Start a transaction to receive the group row and its members in a single transaction
	tx, err := m.db.Begin()
	if err != nil {
		return GroupWithMembers{}, fmt.Errorf("failed to start transaction: %w", err)
	}

	// Ensure the transaction is committed or rolled back
	defer func() {
		err = commitOrRollBackTransaction(err, tx)
	}()

	group, err := groupByID(tx, gid)
	if err != nil {
		return GroupWithMembers{}, err
	}

	users, err := getGroupMembers(tx, gid)
	if err != nil {
		return GroupWithMembers{}, err
	}

	return GroupWithMembers{GroupRow: group, Users: users}, nil
}

// GroupByName returns the group with the given name or a NoDataFoundError if no group was found.
func (m *Manager) GroupByName(name string) (GroupRow, error) {
	return groupByName(m.db, name)
}

func groupByName(db queryable, name string) (GroupRow, error) {
	query := `SELECT name, gid, ugid FROM groups WHERE name = ?`
	row := db.QueryRow(query, name)

	var g GroupRow
	err := row.Scan(&g.Name, &g.GID, &g.UGID)
	if errors.Is(err, sql.ErrNoRows) {
		return GroupRow{}, NewGroupNotFoundError(name)
	}
	if err != nil {
		return GroupRow{}, fmt.Errorf("query error: %w", err)
	}

	return g, nil
}

// GroupWithMembersByName returns the group with the given name with a list of users that are members of the group.
func (m *Manager) GroupWithMembersByName(name string) (_ GroupWithMembers, err error) {
	// Start a transaction to receive the group row and its members in a single transaction
	tx, err := m.db.Begin()
	if err != nil {
		return GroupWithMembers{}, fmt.Errorf("failed to start transaction: %w", err)
	}

	// Ensure the transaction is committed or rolled back
	defer func() {
		err = commitOrRollBackTransaction(err, tx)
	}()

	group, err := groupByName(tx, name)
	if err != nil {
		return GroupWithMembers{}, err
	}

	users, err := getGroupMembers(tx, group.GID)
	if err != nil {
		return GroupWithMembers{}, err
	}

	return GroupWithMembers{GroupRow: group, Users: users}, nil
}

// GroupByUGID returns the group with the given UGID or a NoDataFoundError if no group was found.
func (m *Manager) GroupByUGID(ugid string) (GroupRow, error) {
	return groupByUGID(m.db, ugid)
}

func groupByUGID(db queryable, ugid string) (GroupRow, error) {
	query := `SELECT name, gid, ugid FROM groups WHERE ugid = ?`
	row := db.QueryRow(query, ugid)

	var g GroupRow
	err := row.Scan(&g.Name, &g.GID, &g.UGID)
	if errors.Is(err, sql.ErrNoRows) {
		return GroupRow{}, NewGroupNotFoundError(ugid)
	}
	if err != nil {
		return GroupRow{}, fmt.Errorf("query error: %w", err)
	}

	return g, nil
}

// AllGroupsWithMembers returns all groups with their members.
func (m *Manager) AllGroupsWithMembers() (_ []GroupWithMembers, err error) {
	// Start a transaction to receive all groups and their members in a single transaction
	tx, err := m.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("failed to start transaction: %w", err)
	}

	// Ensure the transaction is committed or rolled back
	defer func() {
		err = commitOrRollBackTransaction(err, tx)
	}()

	groups, err := allGroups(tx)
	if err != nil {
		return nil, err
	}

	var res []GroupWithMembers
	for _, g := range groups {
		users, err := getGroupMembers(tx, g.GID)
		if err != nil {
			return nil, err
		}

		res = append(res, GroupWithMembers{GroupRow: g, Users: users})
	}

	return res, nil
}

// allGroups returns all groups from the database.
func allGroups(db queryable) ([]GroupRow, error) {
	query := `SELECT name, gid, ugid FROM groups`
	rows, err := db.Query(query)
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

	return groups, nil
}

// insertOrUpdateGroupByID inserts or, if a group with the same name, GID or UGID already exists, updates the group in the database.
func insertOrUpdateGroupByID(db queryable, g GroupRow) error {
	exists, err := groupExists(db, g)
	if err != nil {
		return fmt.Errorf("failed to check if group exists: %w", err)
	}

	if !exists {
		return insertGroup(db, g)
	}

	return updateGroupByID(db, g)
}

// groupExists checks if a group with the same name, GID or UGID already exists in the database.
func groupExists(db queryable, g GroupRow) (bool, error) {
	query := `
		SELECT 1 FROM groups 
		WHERE name = ? OR gid = ? OR ugid = ? 
		LIMIT 1`
	row := db.QueryRow(query, g.Name, g.GID, g.UGID)

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

// insertGroup inserts a group into the database.
func insertGroup(db queryable, g GroupRow) error {
	_, err := db.Exec(`INSERT INTO groups (name, gid, ugid) VALUES (?, ?, ?)`, g.Name, g.GID, g.UGID)
	if err != nil {
		return fmt.Errorf("insert group error: %w", err)
	}

	return nil
}

// updateGroupByID updates the group with the same GID in the database.
func updateGroupByID(db queryable, g GroupRow) error {
	_, err := db.Exec(`UPDATE groups SET name = ?, gid = ?, ugid = ? WHERE gid = ?`, g.Name, g.GID, g.UGID, g.GID)
	if err != nil {
		return fmt.Errorf("update group error: %w", err)
	}

	return nil
}
