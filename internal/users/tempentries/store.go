package tempentries

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/canonical/authd/internal/systemd"
	"github.com/canonical/authd/internal/users/proc"
	"golang.org/x/sys/unix"
)

type storedUser struct {
	Version   int    `json:"version"`
	Name      string `json:"name"`
	LoginName string `json:"login_name"`
	UID       uint32 `json:"uid"`
}

type reservationOwner struct {
	process *proc.Process
	name    string
	token   int32
}

const (
	generationName       = systemd.StorePrefix + "generation"
	maxOwnerNameAttempts = 3
)

var (
	newDescriptorStore  = systemd.NewDescriptorStore
	generationGuardPath = "/run/authd/preauth-store"
)

// NewPersistentPreAuthUserRecords restores pidfds and sealed reservation
// metadata handed back by systemd.
func NewPersistentPreAuthUserRecords() (*PreAuthUserRecords, error) {
	store, err := newDescriptorStore()
	if err != nil {
		return nil, err
	}
	if store == nil {
		if _, err := os.Stat(generationGuardPath); err == nil {
			return nil, errors.New("reservation state is held by systemd; start authd through its service")
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		r := NewPreAuthUserRecords()
		r.requireStore = true
		return r, nil
	}
	return newStoredRecords(store, generationGuardPath)
}

// NewStoredPreAuthUserRecords allows tests to supply a descriptor store.
func NewStoredPreAuthUserRecords(store systemd.DescriptorStore) (*PreAuthUserRecords, error) {
	if store == nil {
		return nil, errors.New("missing descriptor store")
	}
	return newStoredRecords(store, "")
}

func newStoredRecords(store systemd.DescriptorStore, guardPath string) (r *PreAuthUserRecords, err error) {
	r = NewPreAuthUserRecords()
	r.store = store
	files := store.Load()
	defer func() {
		if err != nil {
			for _, group := range files {
				for _, file := range group {
					file.Close()
				}
			}
			r.Close()
		}
	}()
	if guardPath != "" {
		if err := r.checkGeneration(files, guardPath); err != nil {
			return r, err
		}
	}
	delete(files, generationName)
	for name, group := range files {
		if len(group) != 2 {
			return r, fmt.Errorf("incomplete reservation descriptor group %q", name)
		}
		var metadata *os.File
		var process *proc.Process
		for _, file := range group {
			if seals, err := unix.FcntlInt(file.Fd(), unix.F_GET_SEALS, 0); err == nil {
				if seals&(unix.F_SEAL_WRITE|unix.F_SEAL_GROW|unix.F_SEAL_SHRINK) !=
					unix.F_SEAL_WRITE|unix.F_SEAL_GROW|unix.F_SEAL_SHRINK {
					return r, errors.New("reservation metadata is not sealed")
				}
				metadata = file
			} else {
				process, err = proc.ProcessFromFile(file)
				if err != nil {
					return r, err
				}
			}
		}
		if metadata == nil || process == nil {
			return r, errors.New("reservation needs a sealed memfd and a pidfd")
		}
		var u storedUser
		err := json.NewDecoder(io.NewSectionReader(metadata, 0, 2048)).Decode(&u)
		metadata.Close()
		if err != nil {
			return r, err
		}
		if u.Version != 1 || u.UID == 0 || u.UID > math.MaxInt32 || u.UID == math.MaxUint16 ||
			u.LoginName == "" || len(u.LoginName) > MaxPreAuthUserNameLength ||
			!strings.HasPrefix(u.Name, UserPrefix+"-") {
			return r, errors.New("invalid reservation metadata")
		}
		if uid, exists := r.uidByLogin[u.LoginName]; exists && uid != u.UID {
			return r, errors.New("conflicting reservation login names")
		}
		existing, exists := r.users[u.UID]
		if exists && (existing.loginName != u.LoginName || existing.name != u.Name) {
			return r, errors.New("conflicting reservation IDs")
		}
		if !exists {
			existing = preAuthUser{name: u.Name, loginName: u.LoginName, uid: u.UID}
		}
		existing.owners = append(existing.owners, reservationOwner{process: process, name: name})
		r.users[u.UID] = existing
		r.uidByLogin[u.LoginName] = u.UID
		r.uidByName[u.Name] = u.UID
	}
	if r.ownerCount() > MaxPreAuthUsers {
		return r, errors.New("too many stored reservation owners")
	}
	if err := r.prune(); err != nil {
		return r, err
	}
	if err := r.startMonitor(); err != nil {
		return r, err
	}
	return r, nil
}

func sealedFile(data []byte) (*os.File, error) {
	fd, err := unix.MemfdCreate("authd-preauth", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "authd-preauth")
	if _, err := file.Write(data); err != nil {
		file.Close()
		return nil, err
	}
	if _, err := unix.FcntlInt(file.Fd(), unix.F_ADD_SEALS,
		unix.F_SEAL_WRITE|unix.F_SEAL_GROW|unix.F_SEAL_SHRINK|unix.F_SEAL_SEAL); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

func (r *PreAuthUserRecords) checkGeneration(files map[string][]*os.File, path string) error {
	guard, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil && len(guard) == 0 {
		return errors.New("empty reservation store generation guard")
	}
	group := files[generationName]
	var token []byte
	if len(group) == 0 {
		if len(guard) != 0 {
			return fmt.Errorf("reservation descriptor store was lost; reboot before restarting authd, or remove %s after confirming no pre-auth SSH sessions remain", path)
		}
		token = []byte(rand.Text())
		file, err := sealedFile(token)
		if err != nil {
			return err
		}
		defer file.Close()
		if err := r.store.Put(generationName, file); err != nil {
			return err
		}
	} else {
		if len(group) != 1 {
			return errors.New("invalid reservation store generation")
		}
		token, err = io.ReadAll(io.NewSectionReader(group[0], 0, 128))
		group[0].Close()
		if err != nil {
			return err
		}
		if len(token) == 0 || (len(guard) != 0 && string(guard) != string(token)) {
			return errors.New("reservation store generation mismatch")
		}
	}
	if len(guard) != 0 {
		return nil
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".preauth-store-*")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errors.New("systemd service must configure RuntimeDirectory=authd and RuntimeDirectoryPreserve=yes")
		}
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.Write(token); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func (r *PreAuthUserRecords) ownerCount() int {
	count := 0
	for _, u := range r.users {
		count += len(u.owners)
	}
	return count
}

func (r *PreAuthUserRecords) captureOwner(u preAuthUser, process *proc.Process) (reservationOwner, error) {
	if r.store == nil {
		return reservationOwner{}, errors.New("first-time SSH login requires systemd's descriptor store")
	}
	if r.ownerCount() >= MaxPreAuthUsers {
		return reservationOwner{}, errors.New("maximum number of pre-auth connection owners reached")
	}
	cloned, err := process.Clone()
	if err != nil {
		return reservationOwner{}, err
	}
	data, err := json.Marshal(storedUser{Version: 1, Name: u.name, LoginName: u.loginName, UID: u.uid})
	if err != nil {
		cloned.Close()
		return reservationOwner{}, err
	}
	metadata, err := sealedFile(data)
	if err != nil {
		cloned.Close()
		return reservationOwner{}, err
	}
	defer metadata.Close()
	for range maxOwnerNameAttempts {
		// Each owner adds this name twice to LISTEN_FDNAMES. Keep the environment
		// string below Linux's 128 KiB limit even at the maximum owner count.
		name := systemd.StorePrefix + rand.Text()[:10]
		ref := reservationOwner{process: cloned, name: name}
		if err := r.monitorOwner(u.uid, &ref); err != nil {
			cloned.Close()
			return reservationOwner{}, err
		}
		err := r.store.Put(name, metadata, cloned.File())
		if err == nil {
			return ref, nil
		}
		r.unmonitorOwner(ref)
		if !errors.Is(err, systemd.ErrDescriptorNameExists) {
			cloned.Close()
			return reservationOwner{}, err
		}
	}
	cloned.Close()
	return reservationOwner{}, fmt.Errorf("could not choose a unique descriptor name after %d attempts: %w",
		maxOwnerNameAttempts, systemd.ErrDescriptorNameExists)
}

// prune releases references after account creation or when their processes exit.
// This runs only during startup, before the monitor and request handlers start.
func (r *PreAuthUserRecords) prune() error {
	if r.store == nil {
		return nil
	}
	released := make(map[string]bool)
	var names []string
	for _, u := range r.users {
		if u.pins > 0 && !u.completed {
			continue
		}
		for _, owner := range u.owners {
			if !u.completed {
				alive, err := owner.process.Alive()
				if err != nil || alive {
					continue
				}
			}
			released[owner.name] = true
			names = append(names, owner.name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	if err := r.store.Remove(names...); err != nil {
		return err
	}
	for uid, u := range r.users {
		var live []reservationOwner
		for _, owner := range u.owners {
			if released[owner.name] {
				owner.process.Close()
			} else {
				live = append(live, owner)
			}
		}
		if len(live) == 0 {
			r.deletePreAuthUser(uid)
		} else {
			u.owners = live
			r.users[uid] = u
		}
	}
	return nil
}

// MarkCompleted releases a reservation after its permanent account exists.
func (r *PreAuthUserRecords) MarkCompleted(uid uint32) error {
	r.rwMu.Lock()
	defer r.rwMu.Unlock()
	if u, exists := r.users[uid]; exists {
		if r.store == nil {
			r.deletePreAuthUser(uid)
			return nil
		}
		u.completed = true
		r.users[uid] = u
		for _, owner := range u.owners {
			r.monitor.ready[owner.token] = watchedOwner{uid: uid, name: owner.name}
		}
	}
	r.wakeMonitor()
	return nil
}

// Close releases runtime copies without removing systemd's stored references.
func (r *PreAuthUserRecords) Close() error {
	r.closeOnce.Do(func() {
		r.rwMu.Lock()
		if r.monitor != nil {
			r.monitor.stopped = true
			r.wakeMonitor()
		}
		r.rwMu.Unlock()
		if r.monitor != nil {
			<-r.monitor.done
		}
		r.storeMu.Lock()
		defer r.storeMu.Unlock()
		r.rwMu.Lock()
		defer r.rwMu.Unlock()
		for _, u := range r.users {
			for _, owner := range u.owners {
				owner.process.Close()
			}
		}
		if r.monitor != nil {
			unix.Close(r.monitor.epoll)
			unix.Close(r.monitor.wake)
		}
		if r.store != nil {
			r.closeErr = r.store.Close()
		}
	})
	return r.closeErr
}
