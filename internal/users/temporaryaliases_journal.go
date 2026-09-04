package users

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/canonical/authd/internal/fileutils"
)

const temporaryAliasJournalName = "temporary-user-aliases.json"

type temporaryAliasCleanup struct {
	Name            string   `json:"name"`
	UID             uint32   `json:"uid"`
	NewName         string   `json:"new_name"`
	OldBrokerID     string   `json:"old_broker_id"`
	OldProviderID   string   `json:"old_provider_id"`
	OldFullUsername string   `json:"old_full_username"`
	NewBrokerID     string   `json:"new_broker_id"`
	NewProviderID   string   `json:"new_provider_id"`
	NewFullUsername string   `json:"new_full_username"`
	LocalGroups     []string `json:"local_groups"`
	CurrentGroups   []string `json:"current_groups"`
}

type temporaryAliasJournal struct {
	mu      sync.Mutex
	path    string
	records map[string]temporaryAliasCleanup
}

func newTemporaryAliasJournal(dbDir string) (*temporaryAliasJournal, error) {
	journal := &temporaryAliasJournal{
		path:    filepath.Join(dbDir, temporaryAliasJournalName),
		records: make(map[string]temporaryAliasCleanup),
	}

	data, err := os.ReadFile(journal.path)
	if errors.Is(err, os.ErrNotExist) {
		return journal, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not read temporary user alias journal: %w", err)
	}
	if err := json.Unmarshal(data, &journal.records); err != nil {
		return nil, fmt.Errorf("could not parse temporary user alias journal: %w", err)
	}
	return journal, nil
}

func (j *temporaryAliasJournal) add(cleanup temporaryAliasCleanup) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	previous, existed := j.records[cleanup.Name]
	j.records[cleanup.Name] = cleanup
	if err := j.saveLocked(); err != nil {
		if existed {
			j.records[cleanup.Name] = previous
		} else {
			delete(j.records, cleanup.Name)
		}
		return err
	}
	return nil
}

func (j *temporaryAliasJournal) get(name string) (temporaryAliasCleanup, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	cleanup, ok := j.records[name]
	return cleanup, ok
}

func (j *temporaryAliasJournal) all() []temporaryAliasCleanup {
	j.mu.Lock()
	defer j.mu.Unlock()
	cleanups := make([]temporaryAliasCleanup, 0, len(j.records))
	for _, cleanup := range j.records {
		cleanups = append(cleanups, cleanup)
	}
	return cleanups
}

func (j *temporaryAliasJournal) remove(name string) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	cleanup, ok := j.records[name]
	if !ok {
		return nil
	}
	delete(j.records, name)
	if err := j.saveLocked(); err != nil {
		j.records[name] = cleanup
		return err
	}
	return nil
}

func (j *temporaryAliasJournal) saveLocked() error {
	if len(j.records) == 0 {
		if err := os.Remove(j.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("could not remove temporary user alias journal: %w", err)
		}
		return syncDir(filepath.Dir(j.path))
	}

	data, err := json.Marshal(j.records)
	if err != nil {
		return fmt.Errorf("could not serialize temporary user alias journal: %w", err)
	}

	file, err := os.CreateTemp(filepath.Dir(j.path), ".temporary-user-aliases-*")
	if err != nil {
		return fmt.Errorf("could not create temporary user alias journal: %w", err)
	}
	tempPath := file.Name()
	removeTemp := true
	defer func() {
		_ = file.Close()
		if removeTemp {
			_ = os.Remove(tempPath)
		}
	}()
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("could not write temporary user alias journal: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("could not sync temporary user alias journal: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("could not close temporary user alias journal: %w", err)
	}
	if err := fileutils.Lrename(tempPath, j.path); err != nil {
		return fmt.Errorf("could not replace temporary user alias journal: %w", err)
	}
	removeTemp = false

	return syncDir(filepath.Dir(j.path))
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("could not open temporary user alias journal directory: %w", err)
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("could not sync temporary user alias journal directory: %w", err)
	}
	return nil
}
