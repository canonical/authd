package tempentries

import (
	"errors"
	"slices"

	"github.com/canonical/authd/internal/users/proc"
)

// AddOwner attaches a connection's process to an existing reservation before
// returning its UID to NSS. Repeated lookups from one process are idempotent.
func (r *PreAuthUserRecords) AddOwner(name string, owner *proc.Process) (uint32, error) {
	if err := checkOwner(owner); err != nil {
		return 0, err
	}
	r.storeMu.Lock()
	defer r.storeMu.Unlock()
	r.rwMu.Lock()
	defer r.rwMu.Unlock()
	if err := r.monitorError(); err != nil {
		return 0, err
	}

	uid, ok := r.uidByLogin[name]
	if !ok {
		return 0, NoDataFoundError{}
	}
	u := r.users[uid]
	pid, err := owner.PID()
	if err != nil {
		return 0, err
	}
	if pid <= 0 {
		return 0, errors.New("pre-auth process owner has exited")
	}
	for _, ref := range u.owners {
		ownerPID, err := ref.process.PID()
		if err != nil {
			return 0, err
		}
		if ownerPID > 0 && ownerPID == pid {
			return uid, nil
		}
	}
	ref, err := r.captureOwner(u, owner)
	if err != nil {
		return 0, err
	}
	u.owners = append(u.owners, ref)
	r.users[uid] = u
	return uid, nil
}

func checkOwner(owner *proc.Process) error {
	if owner == nil {
		return errors.New("missing peer pidfd")
	}
	alive, err := owner.Alive()
	if err != nil {
		return err
	}
	if !alive {
		return errors.New("pre-auth process owner has exited")
	}
	return nil
}

// HoldUser finds and pins a reservation during account creation, even if all
// SSH owners exit. Complete it only after saving the account, then release it.
func (r *PreAuthUserRecords) HoldUser(name string) (uid uint32, release func(), complete func() error, err error) {
	r.storeMu.Lock()
	defer r.storeMu.Unlock()
	r.rwMu.Lock()
	defer r.rwMu.Unlock()
	uid, ok := r.uidByLogin[name]
	if !ok {
		return 0, nil, nil, NoDataFoundError{}
	}
	u := r.users[uid]
	u.pins++
	r.users[uid] = u
	release = func() {
		r.rwMu.Lock()
		defer r.rwMu.Unlock()
		u, ok := r.users[uid]
		if !ok {
			return
		}
		u.pins--
		r.users[uid] = u
		r.wakeMonitor()
	}
	return uid, release, func() error { return r.MarkCompleted(uid) }, nil
}

// ReservedUsers returns the login name for each reserved UID.
func (r *PreAuthUserRecords) ReservedUsers() map[uint32]string {
	r.rwMu.RLock()
	defer r.rwMu.RUnlock()
	users := make(map[uint32]string, len(r.users))
	for uid, u := range r.users {
		users[uid] = u.loginName
	}
	return users
}

// ReservedIDs returns IDs held for pending account creation.
func (r *PreAuthUserRecords) ReservedIDs() ([]uint32, error) {
	r.rwMu.RLock()
	defer r.rwMu.RUnlock()
	ids := make([]uint32, 0, len(r.users))
	for uid := range r.users {
		ids = append(ids, uid)
	}
	return ids, nil
}

// CheckNotReserved prevents assigning a pending account's IDs elsewhere.
func (r *PreAuthUserRecords) CheckNotReserved(id uint32) error {
	ids, err := r.ReservedIDs()
	if err != nil {
		return err
	}
	if slices.Contains(ids, id) {
		return errors.New("ID is held by a pre-authentication connection")
	}
	return nil
}
