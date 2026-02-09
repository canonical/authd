// Package tempentries provides a temporary pre-authentication records.
package tempentries

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sync"

	"github.com/canonical/authd/internal/users/db"
	"github.com/canonical/authd/internal/users/types"
	"github.com/canonical/authd/log"
)

const (
	// MaxPreAuthUsers is the maximum number of pre-auth users that can be registered. If this limit is reached,
	// RegisterPreAuthUser will return an error and disable login for new users via SSH until authd is restarted.
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
}

// PreAuthUserRecords is a structure holding in memory all the temporary users
// that have done a pre-auth request.
type PreAuthUserRecords struct {
	rwMu       sync.RWMutex
	users      map[uint32]preAuthUser
	uidByName  map[string]uint32
	uidByLogin map[string]uint32
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
// Returns the generated UID if the user was found in the pre-auth list or an, and a cleanup function that should be called to
// remove the temporary user once the user is added to the database.
func (r *PreAuthUserRecords) MaybeCompletePreauthUser(name string) (uid uint32, cleanup func(), err error) {
	r.rwMu.Lock()
	defer r.rwMu.Unlock()

	// Check if there is already a pre-auth user for that name
	uid, ok := r.uidByLogin[name]
	if !ok {
		return 0, nil, NoDataFoundError{}
	}

	return uid, func() {
		r.rwMu.Lock()
		defer r.rwMu.Unlock()

		r.deletePreAuthUser(uid)
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
	r.rwMu.RLock()
	defer r.rwMu.RUnlock()

	if len(r.users) == 0 {
		return nil, nil
	}

	users := make([]types.UserEntry, 0, len(r.users))
	for _, user := range r.users {
		users = append(users, preAuthUserEntry(user))
	}

	return users, nil
}

// RegisterPreAuthUser registers a temporary user with a unique UID in our NSS
// handler (in memory, not in the database).
//
// The temporary user record is removed when [MaybeCompletePreauthUser] is called with the
// same username.
//
// This method is called when a user logs in for the first time via SSH, in
// which case sshd checks if the user exists on the system (before
// authentication), and denies the login if the user does not exist.
// We pretend that the user exists by creating this temporary user record,
// which is converted into a permanent user record when [RegisterUser] is called
// after the user authenticated successfully.
func (r *PreAuthUserRecords) RegisterPreAuthUser(loginName string, uid uint32) (err error) {
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

	// To mitigate DoS attacks, we limit the pre-auth users to [MaxPreAuthUsers].
	if len(r.users) >= MaxPreAuthUsers {
		return errors.New("maximum number of pre-auth users reached, login for new users via SSH is disabled until authd is restarted")
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

	return nil
}

// UserByID returns the user information for the given user ID.
func (r *PreAuthUserRecords) userByID(uid uint32) (types.UserEntry, error) {
	r.rwMu.RLock()
	defer r.rwMu.RUnlock()

	return r.userByIDWithoutLock(uid)
}

func (r *PreAuthUserRecords) userByIDWithoutLock(uid uint32) (types.UserEntry, error) {
	user, ok := r.users[uid]
	if !ok {
		return types.UserEntry{}, db.NewUIDNotFoundError(uid)
	}

	return preAuthUserEntry(user), nil
}

func (r *PreAuthUserRecords) userByLogin(loginName string) (types.UserEntry, error) {
	r.rwMu.RLock()
	defer r.rwMu.RUnlock()

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
		return errors.New("maximum number of pre-auth users reached, login for new users via SSH is disabled until authd is restarted")
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
