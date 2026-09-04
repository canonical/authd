package users

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTemporaryAliasJournal(t *testing.T) {
	t.Parallel()

	dbDir := t.TempDir()
	journal, err := newTemporaryAliasJournal(dbDir)
	require.NoError(t, err)

	cleanup := temporaryAliasCleanup{
		Name:            "user",
		UID:             1234,
		NewName:         "user@example.com",
		OldBrokerID:     "broker",
		OldProviderID:   "provider",
		OldFullUsername: "user@example.com",
		NewBrokerID:     "broker",
		NewProviderID:   "provider",
		NewFullUsername: "user@example.com",
		LocalGroups:     []string{"group"},
		CurrentGroups:   []string{"group"},
	}
	require.NoError(t, journal.add(cleanup))

	retrieved, ok := journal.get(cleanup.Name)
	require.True(t, ok)
	require.Equal(t, cleanup, retrieved)

	_, ok = journal.get("nonexistent")
	require.False(t, ok)

	reloaded, err := newTemporaryAliasJournal(dbDir)
	require.NoError(t, err)
	require.Equal(t, []temporaryAliasCleanup{cleanup}, reloaded.all())

	require.NoError(t, reloaded.remove("nonexistent"))

	require.NoError(t, reloaded.remove(cleanup.Name))
	require.NoFileExists(t, reloaded.path)
}

func TestTemporaryAliasJournalUpdateAndGet(t *testing.T) {
	t.Parallel()

	dbDir := t.TempDir()
	journal, err := newTemporaryAliasJournal(dbDir)
	require.NoError(t, err)

	cleanup1 := temporaryAliasCleanup{
		Name:            "user",
		UID:             1001,
		NewName:         "user1@example.com",
		OldFullUsername: "user1@example.com",
	}
	require.NoError(t, journal.add(cleanup1))

	cleanup2 := temporaryAliasCleanup{
		Name:            "user",
		UID:             1002,
		NewName:         "user2@example.com",
		OldFullUsername: "user2@example.com",
	}
	require.NoError(t, journal.add(cleanup2))

	got, ok := journal.get("user")
	require.True(t, ok)
	require.Equal(t, cleanup2, got)
	require.Equal(t, []temporaryAliasCleanup{cleanup2}, journal.all())
}

func TestTemporaryAliasJournalCorruptedFile(t *testing.T) {
	t.Parallel()

	dbDir := t.TempDir()
	journalPath := filepath.Join(dbDir, temporaryAliasJournalName)
	require.NoError(t, os.WriteFile(journalPath, []byte("invalid json content"), 0o600))

	_, err := newTemporaryAliasJournal(dbDir)
	require.ErrorContains(t, err, "could not parse temporary user alias journal")
}

func TestTemporaryAliasJournalUnreadableFile(t *testing.T) {
	t.Parallel()

	dbDir := t.TempDir()
	journalPath := filepath.Join(dbDir, temporaryAliasJournalName)
	require.NoError(t, os.Mkdir(journalPath, 0o700))

	_, err := newTemporaryAliasJournal(dbDir)
	require.ErrorContains(t, err, "could not read temporary user alias journal")
}

func TestTemporaryAliasJournalSaveErrorRollback(t *testing.T) {
	dbDir := t.TempDir()
	journal, err := newTemporaryAliasJournal(dbDir)
	require.NoError(t, err)

	cleanup1 := temporaryAliasCleanup{
		Name: "user1",
		UID:  1001,
	}
	require.NoError(t, journal.add(cleanup1))

	// Make directory read-only so saving fails.
	require.NoError(t, os.Chmod(dbDir, 0o500))       //nolint:gosec // G302 - test-only permission change
	t.Cleanup(func() { _ = os.Chmod(dbDir, 0o700) }) //nolint:gosec // G302 - test-only permission change

	cleanupNew := temporaryAliasCleanup{
		Name: "user2",
		UID:  1002,
	}
	err = journal.add(cleanupNew)
	require.Error(t, err)
	_, ok := journal.get("user2")
	require.False(t, ok, "non-existing record should not be kept on save error")

	cleanup1Updated := temporaryAliasCleanup{
		Name: "user1",
		UID:  9999,
	}
	err = journal.add(cleanup1Updated)
	require.Error(t, err)
	got, ok := journal.get("user1")
	require.True(t, ok)
	require.Equal(t, cleanup1, got, "previous record should be restored on save error")

	err = journal.remove("user1")
	require.Error(t, err)
	got, ok = journal.get("user1")
	require.True(t, ok)
	require.Equal(t, cleanup1, got, "removed record should be restored on save error")

	require.NoError(t, os.Chmod(dbDir, 0o700)) //nolint:gosec // G302 - test-only permission change
	require.NoError(t, journal.remove("user1"))
	require.Empty(t, journal.all())
}

func TestSyncDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, syncDir(dir))

	err := syncDir(filepath.Join(dir, "nonexistent"))
	require.ErrorContains(t, err, "could not open temporary user alias journal directory")
}
