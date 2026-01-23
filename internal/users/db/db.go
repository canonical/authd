// Package db handles transaction with an underlying database to store user and group information.
package db

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/canonical/authd/internal/consts"
	"github.com/canonical/authd/internal/fileutils"
	"github.com/canonical/authd/log"
	// sqlite3 driver.
	_ "github.com/mattn/go-sqlite3"
)

var (
	//go:embed sql/create_schema.sql
	createSchemaQuery string
	schemaVersion     = len(schemaMigrations)
)

// Manager is an abstraction to interact with the database.
type Manager struct {
	db   *sql.DB
	path string
	mu   sync.RWMutex
}

// queryable is an interface to execute SQL queries. Both sql.DB and sql.Tx implement this interface.
type queryable interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
	Query(query string, args ...any) (*sql.Rows, error)
}

// New creates a new database manager by creating or opening the underlying database.
func New(dbDir string) (*Manager, error) {
	dbPath := filepath.Join(dbDir, consts.DefaultDatabaseFileName)

	exists, err := fileutils.FileExists(dbPath)
	if err != nil {
		return nil, err
	}
	if !exists {
		// Create the database with permissions 0600.
		if err := fileutils.Touch(dbPath); err != nil {
			return nil, err
		}
	}

	if err := checkOwnerAndPermissions(dbPath); err != nil {
		return nil, err
	}

	// Use cache=shared to avoid the "database is locked" error as documented in the FAQ:
	// https://github.com/mattn/go-sqlite3?tab=readme-ov-file#faq
	dataSourceName := fmt.Sprintf("file:%s?cache=shared", dbPath)
	db, err := sql.Open("sqlite3", dataSourceName)
	if err != nil {
		return nil, err
	}

	// Set database connections to 1 to avoid the "database is locked" error as documented in the FAQ:
	// https://github.com/mattn/go-sqlite3?tab=readme-ov-file#faq
	db.SetMaxOpenConns(1)

	// Enable foreign key support (this needs to be done for each connection, so we can't do it in the schema).
	_, err = db.Exec("PRAGMA foreign_keys = ON;")
	if err != nil {
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	if !exists {
		log.Debugf(context.Background(), "Creating new SQLite database at %v", dbPath)
		if err := createSchema(db); err != nil {
			// Remove the database file if we failed to create the schema, to avoid that authd tries to use a broken
			// database on the next start.
			if removeErr := os.Remove(dbPath); removeErr != nil {
				log.Warningf(context.Background(), "Failed to remove database file after failed schema creation: %v", removeErr)
			}
			return nil, fmt.Errorf("failed to create schema: %w", err)
		}
	}

	m := &Manager{db: db, path: dbPath, mu: sync.RWMutex{}}
	err = m.maybeApplyMigrations()
	if err != nil {
		return nil, err
	}

	return m, nil
}

func createSchema(db *sql.DB) error {
	// Start a transaction to create the schema and set the schema version in a single transaction
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}

	// Ensure the transaction is committed or rolled back
	defer func() {
		err = commitOrRollBackTransaction(err, tx)
	}()

	if _, err := tx.Exec(createSchemaQuery); err != nil {
		return err
	}

	// Set the initial schema version
	query := `INSERT INTO schema_version (version) VALUES (?)`
	if _, err := tx.Exec(query, schemaVersion); err != nil {
		return fmt.Errorf("failed to set schema version: %w", err)
	}

	return nil
}

func getSchemaVersion(db *sql.DB) (int, error) {
	var version int
	query := "SELECT version FROM schema_version ORDER BY version DESC LIMIT 1"
	err := db.QueryRow(query).Scan(&version)
	if err != nil {
		return 0, fmt.Errorf("failed to get schema version: %w", err)
	}

	return version, nil
}

func setSchemaVersion(db *sql.DB, version int) error {
	query := `UPDATE schema_version SET version = ?`
	_, err := db.Exec(query, version)
	return err
}

// checkOwnerAndPermissions checks if the database file has secure owner and permissions.
func checkOwnerAndPermissions(path string) error {
	fileInfo, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("can't stat database file: %v", err)
	}

	// Fail if the file is not owned by root or the current user.
	stat, ok := fileInfo.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("can't get file information for %s", path)
	}
	if stat.Uid != 0 && int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("unexpected file owner for %s, should be root or %d but is %d", path, os.Getuid(), stat.Uid)
	}

	// Fail if the file is world-writable.
	perm := fileInfo.Mode().Perm()
	if perm&0002 != 0 {
		return fmt.Errorf("insecure file permissions for %s: %o", path, perm)
	}

	return nil
}

// Close closes the db and signal the monitoring goroutine to stop.
func (m *Manager) Close() error {
	log.Debugf(context.Background(), "Closing database")
	return m.db.Close()
}

// RemoveDB removes the database file.
func RemoveDB(dbDir string) error {
	return os.Remove(filepath.Join(dbDir, consts.DefaultDatabaseFileName))
}

// NewUIDNotFoundError returns a NoDataFoundError for the given user ID.
func NewUIDNotFoundError(uid uint32) NoDataFoundError {
	return NoDataFoundError{fmt.Sprintf("user with UID %d not found", uid)}
}

// NewGIDNotFoundError returns a NoDataFoundError for the given group ID.
func NewGIDNotFoundError(gid uint32) NoDataFoundError {
	return NoDataFoundError{fmt.Sprintf("group with GID %d not found", gid)}
}

// NewUserNotFoundError returns a NoDataFoundError for the given user name.
func NewUserNotFoundError(name string) NoDataFoundError {
	return NoDataFoundError{fmt.Sprintf("user %q not found", name)}
}

// NewGroupNotFoundError returns a NoDataFoundError for the given group name.
func NewGroupNotFoundError(name string) NoDataFoundError {
	return NoDataFoundError{fmt.Sprintf("group %q not found", name)}
}

// NoDataFoundError is returned when we didn’t find a matching entry.
type NoDataFoundError struct {
	msg string
}

// Error implements the error interface.
func (err NoDataFoundError) Error() string {
	return err.msg
}

// Is makes this error insensitive to the key and table names.
func (NoDataFoundError) Is(target error) bool { return target == NoDataFoundError{} }

func closeRows(rows *sql.Rows) {
	if err := rows.Close(); err != nil {
		log.Warningf(context.Background(), "failed to close rows: %v", err)
	}
}

func commitOrRollBackTransaction(err error, tx *sql.Tx) error {
	// If there's an error, roll back the transaction
	if err != nil {
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			err = errors.Join(err, fmt.Errorf("failed to rollback transaction: %w", rollbackErr))
		}
		return err
	}

	// Otherwise, commit the transaction
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}
