package tempentries

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/canonical/authd/internal/systemd"
	"github.com/canonical/authd/internal/users/proc"
	"github.com/stretchr/testify/require"
)

func TestReservationDescriptorNameCollisions(t *testing.T) {
	t.Parallel()
	duplicate := fmt.Errorf("existing reservation: %w", systemd.ErrDescriptorNameExists)
	failed := errors.New("descriptor store unavailable")
	tests := map[string]struct {
		failures     []error
		wantAttempts int
		wantErr      error
	}{
		"retry_collisions":          {failures: []error{duplicate, duplicate}, wantAttempts: 3},
		"limit_collision_retries":   {failures: []error{duplicate, duplicate, duplicate}, wantAttempts: 3, wantErr: systemd.ErrDescriptorNameExists},
		"do_not_retry_store_errors": {failures: []error{failed}, wantAttempts: 1, wantErr: failed},
		"stop_retrying_on_store_error": {
			failures: []error{duplicate, failed}, wantAttempts: 2, wantErr: failed,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			store := &failingPutStore{DescriptorStore: systemd.Z_ForTests_NewDescriptorStore(t)}
			owner, _ := testOwner(t)
			r, err := NewStoredPreAuthUserRecords(store)
			require.NoError(t, err)
			t.Cleanup(func() { _ = r.Close() })
			require.NoError(t, r.RegisterPreAuthUser("alice", 10000, owner))
			alice, err := r.UserByID(10000)
			require.NoError(t, err)
			store.failures = tc.failures
			store.names = nil

			err = r.RegisterPreAuthUser("bob", 10001, owner)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				_, err = r.UserByID(10001)
				require.ErrorIs(t, err, NoDataFoundError{}, "failed attempts must not expose a reservation")
			} else {
				require.NoError(t, err)
				bob, err := r.UserByID(10001)
				require.NoError(t, err)
				require.Equal(t, "bob", bob.Gecos)
			}
			require.Len(t, store.names, tc.wantAttempts)
			seen := make(map[string]bool)
			for _, name := range store.names {
				require.False(t, seen[name], "each retry must generate a new name")
				seen[name] = true
			}
			unchanged, err := r.UserByID(10000)
			require.NoError(t, err)
			require.Equal(t, alice, unchanged, "collisions must not alter existing reservations")
			wantOwners := 1
			if tc.wantErr == nil {
				wantOwners++
			}
			r.rwMu.RLock()
			monitored := len(r.monitor.owners)
			r.rwMu.RUnlock()
			require.Equal(t, wantOwners, monitored, "failed attempts must not leave monitored owners")
		})
	}
}

type failingPutStore struct {
	systemd.DescriptorStore
	failures []error
	names    []string
}

func (s *failingPutStore) Put(name string, files ...*os.File) error {
	s.names = append(s.names, name)
	if len(s.failures) > 0 {
		err := s.failures[0]
		s.failures = s.failures[1:]
		return err
	}
	return s.DescriptorStore.Put(name, files...)
}

func TestStoredReservationsSurviveRestart(t *testing.T) {
	t.Parallel()
	store := systemd.Z_ForTests_NewDescriptorStore(t)
	first, exitFirst := testOwner(t)
	second, exitSecond := testOwner(t)
	r, err := NewStoredPreAuthUserRecords(store)
	require.NoError(t, err)
	require.NoError(t, r.RegisterPreAuthUser("alice", 10000, first))
	uid, err := r.AddOwner("alice", second)
	require.NoError(t, err)
	require.Equal(t, uint32(10000), uid)
	_, err = r.AddOwner("alice", second)
	require.NoError(t, err)
	waitForOwners(t, r, uid, 2)
	user, err := r.UserByID(uid)
	require.NoError(t, err)
	require.NoError(t, r.Close())

	r, err = NewStoredPreAuthUserRecords(store)
	require.NoError(t, err)
	restored, err := r.UserByID(uid)
	require.NoError(t, err)
	require.Equal(t, user, restored)
	exitFirst()
	waitForOwners(t, r, uid, 1)
	ids, err := r.ReservedIDs()
	require.NoError(t, err)
	require.Contains(t, ids, uid, "the overlapping owner still holds the reservation")
	require.NoError(t, r.Close())

	r, err = NewStoredPreAuthUserRecords(store)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	ids, err = r.ReservedIDs()
	require.NoError(t, err)
	require.Contains(t, ids, uid)
	exitSecond()
	waitForOwners(t, r, uid, 0)
	ids, err = r.ReservedIDs()
	require.NoError(t, err)
	require.Empty(t, ids)
	require.NoError(t, r.RegisterPreAuthUser("bob", uid))
}

func TestCompletedReservationReleasedWithLiveOwners(t *testing.T) {
	t.Parallel()
	store := systemd.Z_ForTests_NewDescriptorStore(t)
	first, _ := testOwner(t)
	second, _ := testOwner(t)
	r, err := NewStoredPreAuthUserRecords(store)
	require.NoError(t, err)
	require.NoError(t, r.RegisterPreAuthUser("alice", 10000, first))
	_, err = r.AddOwner("alice", second)
	require.NoError(t, err)
	uid, release, cleanup, err := r.HoldUser("alice")
	require.NoError(t, err)
	require.NoError(t, cleanup())
	release()
	require.NoError(t, cleanup(), "repeated completion must be harmless")
	waitForOwners(t, r, uid, 0)
	ids, err := r.ReservedIDs()
	require.NoError(t, err)
	require.Empty(t, ids)
	_, err = r.UserByID(uid)
	require.ErrorIs(t, err, NoDataFoundError{})
	require.NoError(t, r.Close())

	r, err = NewStoredPreAuthUserRecords(store)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	ids, err = r.ReservedIDs()
	require.NoError(t, err)
	require.Empty(t, ids, "completed reservations must not return after a restart")
	require.NoError(t, r.RegisterPreAuthUser("bob", uid, second))
}

func TestCompletedReservationRemovalRetried(t *testing.T) {
	t.Parallel()
	store := systemd.Z_ForTests_NewDescriptorStore(t)
	observed := &observedStore{DescriptorStore: store}
	owner, _ := testOwner(t)
	r, err := NewStoredPreAuthUserRecords(observed)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	require.NoError(t, r.RegisterPreAuthUser("alice", 10000, owner))
	_, release, cleanup, err := r.HoldUser("alice")
	require.NoError(t, err)

	store.SetFailRemove(true)
	require.NoError(t, cleanup())
	release()
	require.Eventually(t, func() bool { return observed.attempts.Load() > 0 }, 3*time.Second, time.Millisecond)
	ids, err := r.ReservedIDs()
	require.NoError(t, err, "cleanup errors must not fail lookups")
	require.Contains(t, ids, uint32(10000), "failed removal must retain the reservation")
	store.SetFailRemove(false)
	waitForOwners(t, r, 10000, 0)
	ids, err = r.ReservedIDs()
	require.NoError(t, err)
	require.Empty(t, ids, "removal must be retried without waiting for the owner to exit")
}

func TestDeadPidfdReclaimedOnRestore(t *testing.T) {
	t.Parallel()
	store := systemd.Z_ForTests_NewDescriptorStore(t)
	owner, exit := testOwner(t)
	r, err := NewStoredPreAuthUserRecords(store)
	require.NoError(t, err)
	require.NoError(t, r.RegisterPreAuthUser("alice", 10000, owner))
	require.NoError(t, r.Close())
	exit()
	r, err = NewStoredPreAuthUserRecords(store)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	ids, err := r.ReservedIDs()
	require.NoError(t, err)
	require.Empty(t, ids)
	require.Error(t, r.RegisterPreAuthUser("bob", 10001, owner))
}

func TestStoreFailuresDoNotExposeOrReleaseIDs(t *testing.T) {
	t.Parallel()
	store := systemd.Z_ForTests_NewDescriptorStore(t)
	observed := &observedStore{DescriptorStore: store}
	first, exit := testOwner(t)
	second, _ := testOwner(t)
	r, err := NewStoredPreAuthUserRecords(observed)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	require.NoError(t, r.RegisterPreAuthUser("alice", 10000, first))
	store.FailPut = true
	require.Error(t, r.RegisterPreAuthUser("bob", 10001, second))
	_, err = r.UserByID(10001)
	require.ErrorIs(t, err, NoDataFoundError{})
	_, err = r.AddOwner("alice", second)
	require.Error(t, err)
	waitForOwners(t, r, 10000, 1)
	store.FailPut = false
	store.SetFailRemove(true)
	exit()
	require.Eventually(t, func() bool { return observed.attempts.Load() > 0 }, 3*time.Second, time.Millisecond)
	ids, err := r.ReservedIDs()
	require.NoError(t, err)
	require.Contains(t, ids, uint32(10000), "failed removal must not make IDs available")
	store.SetFailRemove(false)
	waitForOwners(t, r, 10000, 0)
	ids, err = r.ReservedIDs()
	require.NoError(t, err)
	require.Empty(t, ids)
}

func TestPendingAccountCreationKeepsPidfd(t *testing.T) {
	t.Parallel()
	store := systemd.Z_ForTests_NewDescriptorStore(t)
	owner, exit := testOwner(t)
	r, err := NewStoredPreAuthUserRecords(store)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	require.NoError(t, r.RegisterPreAuthUser("alice", 10000, owner))
	uid, release, _, err := r.HoldUser("alice")
	require.NoError(t, err)
	require.Equal(t, uint32(10000), uid)
	_, _, _, err = r.HoldUser("unknown")
	require.ErrorIs(t, err, NoDataFoundError{})
	exit()
	require.Eventually(t, func() bool {
		r.rwMu.RLock()
		defer r.rwMu.RUnlock()
		return len(r.monitor.ready) == 1
	}, 3*time.Second, time.Millisecond)
	ids, err := r.ReservedIDs()
	require.NoError(t, err)
	require.Contains(t, ids, uint32(10000))
	release()
	waitForOwners(t, r, 10000, 0)
	ids, err = r.ReservedIDs()
	require.NoError(t, err)
	require.Empty(t, ids)
}

func TestLookupsDoNotWaitForReservationCleanup(t *testing.T) {
	t.Parallel()
	store := &blockingRemovalStore{
		DescriptorStore: systemd.Z_ForTests_NewDescriptorStore(t),
		started:         make(chan struct{}),
		resume:          make(chan struct{}),
	}
	owner, exit := testOwner(t)
	liveOwner, _ := testOwner(t)
	r, err := NewStoredPreAuthUserRecords(store)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	var resume sync.Once
	unblock := func() { resume.Do(func() { close(store.resume) }) }
	t.Cleanup(unblock)
	require.NoError(t, r.RegisterPreAuthUser("alice", 10000, owner))
	require.NoError(t, r.RegisterPreAuthUser("bob", 10001, liveOwner))
	exit()
	select {
	case <-store.started:
	case <-time.After(3 * time.Second):
		t.Fatal("owner exit did not start background cleanup")
	}

	type lookupResults struct {
		ids     []uint32
		errors  []error
		missing error
	}
	done := make(chan lookupResults, 1)
	go func() {
		ids, err := r.ReservedIDs()
		result := lookupResults{ids: ids, errors: []error{err}}
		_, err = r.UserByID(10001)
		result.errors = append(result.errors, err)
		_, err = r.GroupByID(10001)
		result.errors = append(result.errors, err)
		_, result.missing = r.UserByID(10002)
		result.errors = append(result.errors, r.MarkCompleted(10001))
		done <- result
	}()
	select {
	case result := <-done:
		for _, err := range result.errors {
			require.NoError(t, err)
		}
		require.Contains(t, result.ids, uint32(10000), "IDs remain reserved until removal is confirmed")
		require.ErrorIs(t, result.missing, NoDataFoundError{})
	case <-time.After(3 * time.Second):
		t.Fatal("lookups blocked on descriptor-store cleanup")
	}
	unblock()
	waitForOwners(t, r, 10000, 0)
	waitForOwners(t, r, 10001, 0)
	require.NoError(t, r.Close())
	require.NoError(t, r.Close(), "closing the monitor twice must be harmless")
}

type blockingRemovalStore struct {
	systemd.DescriptorStore
	started     chan struct{}
	startedOnce sync.Once
	resume      chan struct{}
}

func (s *blockingRemovalStore) Remove(names ...string) error {
	s.startedOnce.Do(func() { close(s.started) })
	<-s.resume
	return s.DescriptorStore.Remove(names...)
}

type observedStore struct {
	systemd.DescriptorStore
	attempts atomic.Uint32
}

func (s *observedStore) Remove(names ...string) error {
	err := s.DescriptorStore.Remove(names...)
	s.attempts.Add(1)
	return err
}

func waitForOwners(t *testing.T, r *PreAuthUserRecords, uid uint32, count int) {
	t.Helper()
	require.Eventually(t, func() bool {
		r.rwMu.RLock()
		defer r.rwMu.RUnlock()
		return len(r.users[uid].owners) == count
	}, 3*time.Second, time.Millisecond)
}

func TestDescriptorStoreGeneration(t *testing.T) {
	t.Parallel()
	store := systemd.Z_ForTests_NewDescriptorStore(t)
	path := filepath.Join(t.TempDir(), "generation")
	r, err := newStoredRecords(store, path)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	r, err = newStoredRecords(store, path)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	require.NoError(t, store.Remove(generationName))
	_, err = newStoredRecords(store, path)
	require.ErrorContains(t, err, "descriptor store was lost",
		"an empty store must not look like a fresh boot while connections may still be alive")
	require.ErrorContains(t, err, path)
	require.NoError(t, os.Remove(path))
	r, err = newStoredRecords(store, path)
	require.NoError(t, err, "the guard may be removed once no pending SSH sessions remain")
	require.NoError(t, r.Close())
}

func TestPersistentRecordsCheckConfiguredGuard(t *testing.T) {
	oldStore, oldPath := newDescriptorStore, generationGuardPath
	t.Cleanup(func() {
		newDescriptorStore, generationGuardPath = oldStore, oldPath
	})
	newDescriptorStore = func() (systemd.DescriptorStore, error) { return nil, nil }
	generationGuardPath = filepath.Join(t.TempDir(), "guard")
	require.NoError(t, os.WriteFile(generationGuardPath, []byte("generation"), 0600))
	_, err := NewPersistentPreAuthUserRecords()
	require.ErrorContains(t, err, "start authd through its service")
}

func TestPersistentRecordsRejectUnreadableGuard(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a non-root caller needs to fail directory access")
	}
	oldStore, oldPath := newDescriptorStore, generationGuardPath
	t.Cleanup(func() {
		newDescriptorStore, generationGuardPath = oldStore, oldPath
	})
	newDescriptorStore = func() (systemd.DescriptorStore, error) { return nil, nil }
	dir := t.TempDir()
	generationGuardPath = filepath.Join(dir, "guard")
	require.NoError(t, os.Chmod(dir, 0))
	t.Cleanup(func() { require.NoError(t, os.Chmod(dir, 0o700)) }) //nolint:gosec // A directory needs execute permission for cleanup.

	records, err := NewPersistentPreAuthUserRecords()
	require.ErrorIs(t, err, os.ErrPermission)
	require.Nil(t, records)
}

func TestStoredReservationNamesFitExecEnvironment(t *testing.T) {
	t.Parallel()
	store := systemd.Z_ForTests_NewDescriptorStore(t)
	r, err := NewStoredPreAuthUserRecords(store)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	require.NoError(t, r.RegisterPreAuthUser("alice", 10000))
	r.rwMu.RLock()
	name := r.users[10000].owners[0].name
	r.rwMu.RUnlock()
	env := "LISTEN_FDNAMES=" + generationName + ":" + strings.Repeat(name+":"+name+":", MaxPreAuthUsers)
	require.Less(t, len(env)+1, 32*4096, "all owners must fit in one execve environment string on 4 KiB-page kernels")
}

func TestAddOwnerRejectsReapedProcess(t *testing.T) {
	t.Parallel()
	store := systemd.Z_ForTests_NewDescriptorStore(t)
	owner, exit := testOwner(t)
	r, err := NewStoredPreAuthUserRecords(store)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	require.NoError(t, r.RegisterPreAuthUser("alice", 10000, owner))
	exit()
	_, err = r.AddOwner("alice", owner)
	require.ErrorContains(t, err, "owner has exited")
}

func TestIncompleteDescriptorGroup(t *testing.T) {
	t.Parallel()
	store := systemd.Z_ForTests_NewDescriptorStore(t)
	metadata, err := sealedFile([]byte(`{"version":1,"uid":10000}`))
	require.NoError(t, err)
	defer metadata.Close()
	require.NoError(t, store.Put(systemd.StorePrefix+"incomplete", metadata))
	_, err = NewStoredPreAuthUserRecords(store)
	require.ErrorContains(t, err, "incomplete reservation")
}

func testOwner(t *testing.T) (*proc.Process, func()) {
	t.Helper()
	cmd := exec.Command("sleep", "600")
	require.NoError(t, cmd.Start())
	var once sync.Once
	exit := func() {
		once.Do(func() {
			require.NoError(t, cmd.Process.Kill())
			_ = cmd.Wait()
		})
	}
	t.Cleanup(exit)
	owner, err := proc.OpenProcess(cmd.Process.Pid)
	require.NoError(t, err)
	t.Cleanup(func() { _ = owner.Close() })
	return owner, exit
}
