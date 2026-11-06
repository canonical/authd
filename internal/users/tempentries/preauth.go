// Package tempentries provides a temporary pre-authentication records.
package tempentries

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/canonical/authd/internal/systemd"
	"github.com/canonical/authd/internal/users/db"
	"github.com/canonical/authd/internal/users/proc"
	"github.com/canonical/authd/internal/users/types"
	"github.com/canonical/authd/log"
)

const (
	// MaxPreAuthUsers is the maximum number of pre-auth users that can be registered. If this limit is reached,
	// RegisterPreAuthUser will return an error until an existing reservation is released.
	//
	// This value must be significantly smaller (less than half) than the number of UIDs which can be generated (as
	// defined by UID_MIN and UID_MAX in the config file), otherwise finding a unique UID by trial and error can take
	// too long.
	MaxPreAuthUsers = 4096

	// MaxPreAuthUserNameLength is the maximum length of the pre-auth user name.
	MaxPreAuthUserNameLength = 256

	// UserPrefix is the prefix used as login name by the pre-auth temporary users.
	UserPrefix = "authd-pre-auth-user"
)

// NoDataFoundError is the error returned when no entry is found in the database.
type NoDataFoundError = db.NoDataFoundError

type preAuthUser struct {
	// name is the generated random name of the pre-auth user (which is returned by UserByID).
	name string
	// loginName is the name of the user who the pre-auth user record is created for.
	loginName string
	uid       uint32
	owners    []reservationOwner
	completed bool
	pins      int
}

// PreAuthUserRecords tracks temporary users that have done a pre-auth request.
type PreAuthUserRecords struct {
	rwMu         sync.RWMutex
	users        map[uint32]preAuthUser
	uidByName    map[string]uint32
	uidByLogin   map[string]uint32
	store        systemd.DescriptorStore
	requireStore bool
}

// NewPreAuthUserRecords creates a new instance of [preAuthUserRecords] that
// can be used to track the temporary preauth user entries.
func NewPreAuthUserRecords() *PreAuthUserRecords {
	return &PreAuthUserRecords{
		users:      make(map[uint32]preAuthUser),
		uidByName:  make(map[string]uint32),
		uidByLogin: make(map[string]uint32),
	}
}

// MaybeCompletePreauthUser tries to complete a pre-auth user registration.
//
// Returns the reserved UID and a function to release the reservation after
// the account is saved.
func (r *PreAuthUserRecords) MaybeCompletePreauthUser(name string) (uid uint32, cleanup func() error, err error) {
	r.rwMu.Lock()
	defer r.rwMu.Unlock()

	if err := r.prune(); err != nil {
		return 0, nil, err
	}
	// Check if there is already a pre-auth user for that name
	uid, ok := r.uidByLogin[name]
	if !ok {
		return 0, nil, NoDataFoundError{}
	}

	return uid, func() error {
		return r.MarkCompleted(uid)
	}, nil
}

// UserByID returns the user information for the given user ID.
func (r *PreAuthUserRecords) UserByID(uid uint32) (types.UserEntry, error) {
	return r.userByID(uid)
}

// UserByLogin returns the user information for the given user name.
func (r *PreAuthUserRecords) UserByLogin(name string) (types.UserEntry, error) {
	return r.userByLogin(name)
}

// GroupByID returns the private-group information for the given user ID.
func (r *PreAuthUserRecords) GroupByID(gid uint32) (types.GroupEntry, error) {
	user, err := r.userByID(gid)
	if err != nil {
		return types.GroupEntry{}, err
	}

	return types.GroupEntry{
		Name:   user.Name,
		GID:    user.GID,
		Users:  []string{user.Name},
		Passwd: "x",
	}, nil
}

// AllUsers returns all pre-auth users as a slice of UserEntry.
func (r *PreAuthUserRecords) AllUsers() ([]types.UserEntry, error) {
	r.rwMu.Lock()
	defer r.rwMu.Unlock()

	if err := r.prune(); err != nil {
		return nil, err
	}

	if len(r.users) == 0 {
		return nil, nil
	}

	users := make([]types.UserEntry, 0, len(r.users))
	for _, user := range r.users {
		if user.completed {
			continue
		}
		users = append(users, preAuthUserEntry(user))
	}

	return users, nil
}

// RegisterPreAuthUser reserves a unique UID in our NSS handler. If a store is
// configured, the reservation is saved before this method returns.
//
// The temporary user record is removed by the cleanup function returned by
// [MaybeCompletePreauthUser], after the account is saved in the database.
//
// This method is called when a user logs in for the first time via SSH, in
// which case sshd checks if the user exists on the system (before
// authentication), and denies the login if the user does not exist.
// We pretend that the user exists by creating this temporary user record,
// which is converted into a permanent user record after successful authentication.
func (r *PreAuthUserRecords) RegisterPreAuthUser(loginName string, uid uint32, owners ...*proc.Process) (err error) {
	if loginName == "" {
		return fmt.Errorf("empty username")
	}

	// To mitigate DoS attacks, we limit the length of the name to
	// MaxPreAuthUserNameLength] characters.
	if len(loginName) > MaxPreAuthUserNameLength {
		return fmt.Errorf("username is too long (maximum %d characters): %q",
			MaxPreAuthUserNameLength, loginName)
	}

	r.rwMu.Lock()
	defer r.rwMu.Unlock()

	if r.requireStore && r.store == nil {
		return errors.New("first-time SSH login requires systemd's descriptor store")
	}
	if len(owners) > 1 {
		return errors.New("expected one initial pre-auth process owner")
	}
	if err := r.prune(); err != nil {
		return err
	}
	// To mitigate DoS attacks, we limit the pre-auth users to [MaxPreAuthUsers].
	if len(r.users) >= MaxPreAuthUsers {
		return errors.New("maximum number of pre-auth users reached, waiting for existing connections to exit")
	}

	if _, ok := r.uidByLogin[loginName]; ok {
		panic(fmt.Sprintf("We should never be here, User %q is already tracked", loginName))
	}

	if user, ok := r.users[uid]; ok {
		panic(fmt.Sprintf("We should never be here, ID %d is already tracked for %q",
			uid, user.loginName))
	}

	if err := r.addPreAuthUser(uid, loginName); err != nil {
		return fmt.Errorf("could not add pre-auth user record: %w", err)
	}

	if r.store != nil && len(owners) == 0 {
		owner, err := proc.OpenProcess(os.Getpid())
		if err != nil {
			r.deletePreAuthUser(uid)
			return err
		}
		defer owner.Close()
		owners = []*proc.Process{owner}
	}
	user := r.users[uid]
	for _, owner := range owners {
		ref, err := r.captureOwner(user, owner)
		if err != nil {
			r.deletePreAuthUser(uid)
			return err
		}
		user.owners = append(user.owners, ref)
	}
	r.users[uid] = user

	return nil
}

// UserByID returns the user information for the given user ID.
func (r *PreAuthUserRecords) userByID(uid uint32) (types.UserEntry, error) {
	r.rwMu.Lock()
	defer r.rwMu.Unlock()

	if err := r.prune(); err != nil {
		return types.UserEntry{}, err
	}

	return r.userByIDWithoutLock(uid)
}

func (r *PreAuthUserRecords) userByIDWithoutLock(uid uint32) (types.UserEntry, error) {
	user, ok := r.users[uid]
	if !ok || user.completed {
		return types.UserEntry{}, db.NewUIDNotFoundError(uid)
	}

	return preAuthUserEntry(user), nil
}

func (r *PreAuthUserRecords) userByLogin(loginName string) (types.UserEntry, error) {
	r.rwMu.Lock()
	defer r.rwMu.Unlock()

	if err := r.prune(); err != nil {
		return types.UserEntry{}, err
	}

	uid, ok := r.uidByLogin[loginName]
	if !ok {
		return types.UserEntry{}, db.NewUserNotFoundError(loginName)
	}

	return r.userByIDWithoutLock(uid)
}

func preAuthUserEntry(user preAuthUser) types.UserEntry {
	return types.UserEntry{
		Name: user.name,
		UID:  user.uid,
		// The UID is also the GID of the user private group (see https://wiki.debian.org/UserPrivateGroups#UPGs)
		GID:   user.uid,
		Gecos: user.loginName,
		Dir:   "/nonexistent",
		Shell: "/usr/sbin/nologin",
	}
}

// addPreAuthUser adds a temporary user with a random name and the given UID. We use a random name here to avoid
// creating user records with attacker-controlled names.
func (r *PreAuthUserRecords) addPreAuthUser(uid uint32, loginName string) (err error) {
	if len(r.users) >= MaxPreAuthUsers {
		return errors.New("maximum number of pre-auth users reached, waiting for existing connections to exit")
	}

	var name string
	for {
		// Generate a 64 character (32 bytes in hex) random ID which we store in the
		// name field of the temporary user record to be able to identify it.
		bytes := make([]byte, 32)
		if _, err := rand.Read(bytes); err != nil {
			return fmt.Errorf("failed to generate random name: %w", err)
		}
		name = fmt.Sprintf("%s-%x", UserPrefix, bytes)

		if _, ok := r.uidByName[name]; ok {
			log.Debugf(context.Background(), "Generated user %q was not unique", name)
			continue
		}

		break
	}

	log.Debugf(context.Background(),
		"Added temporary record for user %q with UID %d as %q", loginName, uid, name)

	user := preAuthUser{name: name, uid: uid, loginName: loginName}
	r.users[uid] = user
	r.uidByName[name] = uid
	r.uidByLogin[loginName] = uid

	return nil
}

// deletePreAuthUser deletes the temporary user with the given UID.
//
// This must be called with the mutex locked.
func (r *PreAuthUserRecords) deletePreAuthUser(uid uint32) {
	user, ok := r.users[uid]
	if !ok {
		// We ignore the case that the pre-auth user does not exist, because it can happen that the same user is
		// registered multiple times (because multiple SSH sessions are opened for the same user) and the cleanup
		// function is called multiple times.
		return
	}
	delete(r.users, uid)
	delete(r.uidByName, user.name)
	delete(r.uidByLogin, user.loginName)
	log.Debugf(context.Background(), "Removed temporary record for user %q with UID %d", user.name, uid)
}
