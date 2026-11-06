package systemd

import (
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/canonical/authd/internal/testsdetection"
	"golang.org/x/sys/unix"
)

// TestDescriptorStore holds descriptors outside a simulated daemon instance.
type TestDescriptorStore struct {
	mu         sync.Mutex
	files      map[string][]*os.File
	FailPut    bool
	FailRemove bool
}

// Z_ForTests_NewDescriptorStore supplies the same ownership semantics as
// systemd, without changing the host's service manager.
//
//nolint:revive // Test helper naming convention.
func Z_ForTests_NewDescriptorStore(tb testing.TB) *TestDescriptorStore {
	tb.Helper()
	testsdetection.MustBeTesting()
	s := &TestDescriptorStore{files: make(map[string][]*os.File)}
	tb.Cleanup(func() {
		for _, files := range s.files {
			for _, file := range files {
				file.Close()
			}
		}
	})
	return s
}

func duplicate(file *os.File) (*os.File, error) {
	fd, err := unix.FcntlInt(file.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), file.Name()), nil
}

// Load duplicates systemd's references for a new daemon instance.
func (s *TestDescriptorStore) Load() map[string][]*os.File {
	testsdetection.MustBeTesting()
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make(map[string][]*os.File)
	for name, files := range s.files {
		for _, file := range files {
			cloned, err := duplicate(file)
			if err != nil {
				panic(err)
			}
			result[name] = append(result[name], cloned)
		}
	}
	return result
}

// Put keeps copies after the sender closes its descriptors.
func (s *TestDescriptorStore) Put(name string, files ...*os.File) error {
	testsdetection.MustBeTesting()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.FailPut {
		return errors.New("descriptor store failed")
	}
	if _, exists := s.files[name]; exists {
		return ErrDescriptorNameExists
	}
	var copies []*os.File
	for _, file := range files {
		cloned, err := duplicate(file)
		if err != nil {
			for _, file := range copies {
				file.Close()
			}
			return err
		}
		copies = append(copies, cloned)
	}
	s.files[name] = copies
	return nil
}

// Remove releases the service manager's copies.
func (s *TestDescriptorStore) Remove(names ...string) error {
	testsdetection.MustBeTesting()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.FailRemove {
		return errors.New("descriptor store removal failed")
	}
	for _, name := range names {
		for _, file := range s.files[name] {
			file.Close()
		}
		delete(s.files, name)
	}
	return nil
}

// Close represents daemon exit, not service-manager exit.
func (s *TestDescriptorStore) Close() error {
	testsdetection.MustBeTesting()
	return nil
}
