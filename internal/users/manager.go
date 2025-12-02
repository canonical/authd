// Package users support all common action on the system for user handling.
package users

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/user"
	"slices"
	"strconv"
	"sync"
	"syscall"

	"github.com/canonical/authd/internal/decorate"
	"github.com/canonical/authd/internal/fileutils"
	"github.com/canonical/authd/internal/users/db"
	"github.com/canonical/authd/internal/users/localentries"
	userslocking "github.com/canonical/authd/internal/users/locking"
	"github.com/canonical/authd/internal/users/tempentries"
	"github.com/canonical/authd/internal/users/types"
	"github.com/canonical/authd/log"
)

// Config is the configuration for the user manager.
type Config struct {
	UIDMin uint32 `mapstructure:"uid_min" yaml:"uid_min"`
	UIDMax uint32 `mapstructure:"uid_max" yaml:"uid_max"`
	GIDMin uint32 `mapstructure:"gid_min" yaml:"gid_min"`
	GIDMax uint32 `mapstructure:"gid_max" yaml:"gid_max"`
}

// DefaultConfig is the default configuration for the user manager.
var DefaultConfig = Config{
	UIDMin: 10000,
	UIDMax: 60000,
	GIDMin: 10000,
	GIDMax: 60000,
}

// Manager is the manager for any user related operation.
type Manager struct {
	// userManagementMu must be used to protect all the operations in which we
	// do users registration to the DB, to ensure that concurrent goroutines may
	// not falsify the checks we are performing (such as the users existence).
	userManagementMu sync.Mutex

	db             *db.Manager
	config         Config
	preAuthRecords *tempentries.PreAuthUserRecords
	idGenerator    IDGeneratorIface
}

type options struct {
	idGenerator IDGeneratorIface
}

// Option is a function that allows changing some of the default behaviors of the manager.
type Option func(*options)

// WithIDGenerator makes the manager use a specific ID generator.
// This option is only useful in tests.
func WithIDGenerator(g IDGeneratorIface) Option {
	return func(o *options) {
		o.idGenerator = g
	}
}

// NewManager creates a new user manager.
func NewManager(config Config, dbDir string, args ...Option) (m *Manager, err error) {
	log.Debugf(context.Background(), "Creating user manager with config: %+v", config)

	opts := &options{}
	for _, arg := range args {
		arg(opts)
	}

	if opts.idGenerator == nil {
		// Check that the ID ranges are valid.
		if config.UIDMin >= config.UIDMax {
			return nil, fmt.Errorf("UID_MIN (%d) must be less than UID_MAX (%d)", config.UIDMin, config.UIDMax)
		}
		if config.GIDMin >= config.GIDMax {
			return nil, fmt.Errorf("GID_MIN (%d) must be less than GID_MAX (%d)", config.GIDMin, config.GIDMax)
		}
		// UIDs/GIDs larger than a signed int32 are known to cause issues in various programs,
		// so they should be avoided (see https://systemd.io/UIDS-GIDS/)
		if config.UIDMax > math.MaxInt32 {
			return nil, fmt.Errorf("UID_MAX (%d) must be less than or equal to %d", config.UIDMax, math.MaxInt32)
		}
		if config.GIDMax > math.MaxInt32 {
			return nil, fmt.Errorf("GID_MAX (%d) must be less than or equal to %d", config.GIDMax, math.MaxInt32)
		}

		// Check that the ID ranges are not overlapping with systemd dynamic service users.
		rangesOverlap := func(min1, max1, min2, max2 uint32) bool {
			return (min1 <= max2 && max1 >= min2) || (min2 <= max1 && max2 >= min1)
		}

		if rangesOverlap(config.UIDMin, config.UIDMax, systemdDynamicUIDMin, systemdDynamicUIDMax) {
			return nil, fmt.Errorf("UID range (%d-%d) overlaps with systemd dynamic service users range (%d-%d)", config.UIDMin, config.UIDMax, systemdDynamicUIDMin, systemdDynamicUIDMax)
		}
		if rangesOverlap(config.GIDMin, config.GIDMax, systemdDynamicUIDMin, systemdDynamicUIDMax) {
			return nil, fmt.Errorf("GID range (%d-%d) overlaps with systemd dynamic service users range (%d-%d)", config.GIDMin, config.GIDMax, systemdDynamicUIDMin, systemdDynamicUIDMax)
		}

		// Check that the number of possible UIDs is at least twice the number of possible pre-auth users.
		numUIDs := config.UIDMax - config.UIDMin + 1
		minNumUIDs := uint32(tempentries.MaxPreAuthUsers * 2)
		if numUIDs < minNumUIDs {
			return nil, fmt.Errorf("UID range configured via UID_MIN and UID_MAX is too small (%d), must be at least %d", numUIDs, minNumUIDs)
		}

		opts.idGenerator = &IDGenerator{
			UIDMin: config.UIDMin,
			UIDMax: config.UIDMax,
			GIDMin: config.GIDMin,
			GIDMax: config.GIDMax,
		}
	}

	m = &Manager{
		config:         config,
		preAuthRecords: tempentries.NewPreAuthUserRecords(),
		idGenerator:    opts.idGenerator,
	}

	m.db, err = db.New(dbDir)
	if err != nil {
		return nil, err
	}

	return m, nil
}

// Stop closes the underlying db.
func (m *Manager) Stop() error {
	return m.db.Close()
}

// UpdateUser updates the user information in the db.
func (m *Manager) UpdateUser(u types.UserInfo) (err error) {
	defer decorate.OnError(&err, "failed to update user %q", u.Name)

	log.Debugf(context.TODO(), "Updating user %q", u.Name)

	if u.Name == "" {
		return errors.New("empty username")
	}

	// Prepend the user private group
	u.Groups = append([]types.GroupInfo{{Name: u.Name, UGID: u.Name}}, u.Groups...)
	userPrivateGroup := &u.Groups[0]

	var oldUserInfo *types.UserInfo
	checkEqualUserExists := func() (exists bool, err error) {
		// Check if the user already exists in the database.
		oldUserInfo, err = m.getOldUserInfoFromDB(u.Name)
		if err != nil {
			return false, err
		}
		if oldUserInfo == nil {
			return false, nil
		}
		if !compareNewUserInfoWithUserInfoFromDB(u, *oldUserInfo) {
			log.Debugf(context.TODO(), "User %q is already in our database", u.Name)
			return false, nil
		}

		log.Debugf(context.TODO(), "User %q in database already matches current", u.Name)
		return true, nil
	}

	// Do a first check before locking, so that if the user is already there and
	// matches the DB entry, we can avoid any kind of locking (and so being
	// blocked by other pre-auth users that may try to login meanwhile).
	exists, err := checkEqualUserExists()
	if exists || err != nil {
		// The user already exists, so no update needed, or an error occurred.
		return err
	}

	m.userManagementMu.Lock()
	defer m.userManagementMu.Unlock()

	// Now that we're locked, check again if meanwhile some other request
	// created the same user, if not we can do all the kinds of locking since
	// we're sure that the user needs to be added or updated in the database.
	if exists, err := checkEqualUserExists(); exists || err != nil {
		return err
	}

	log.Debugf(context.TODO(), "User %q needs update", u.Name)

	lockedEntries, unlockEntries, err := localentries.WithUserDBLock()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlockEntries()) }()

	if oldUserInfo != nil {
		// The user already exists in the database, use the existing UID to avoid permission issues.
		u.UID = oldUserInfo.UID
	} else {
		preauthUID, cleanup, err := m.preAuthRecords.MaybeCompletePreauthUser(u.Name)
		if err != nil && !errors.Is(err, tempentries.NoDataFoundError{}) {
			return err
		}
		if preauthUID != 0 {
			u.UID = preauthUID
			defer cleanup()
		} else {
			unique, err := lockedEntries.IsUniqueUserName(u.Name)
			if err != nil {
				return err
			}
			if !unique {
				log.Warningf(context.Background(), "User %q already exists", u.Name)
				return fmt.Errorf("another system user exists with %q name", u.Name)
			}

			var cleanupUID func()
			u.UID, cleanupUID, err = m.idGenerator.GenerateUID(lockedEntries, m)
			if err != nil {
				return err
			}
			defer cleanupUID()
			log.Debugf(context.Background(), "Using new UID %d for user %q", u.UID, u.Name)
		}
	}

	// User private Group GID is the same of the user UID.
	userPrivateGroup.GID = &u.UID

	var groupRows []db.GroupRow
	var localGroups []string
	var newGroups []types.GroupInfo
	for _, g := range u.Groups {
		if g.Name == "" {
			return fmt.Errorf("empty group name for user %q", u.Name)
		}

		if g.UGID == "" {
			// An empty UGID means that the group is local, i.e. it's not stored in the database but expected to be
			// already present in /etc/group.
			localGroups = append(localGroups, g.Name)
			continue
		}

		// It's not a local group, so before storing it in the database, check if a group with the same name already
		// exists.
		if err := m.checkGroupNameConflict(g.Name, g.UGID); err != nil {
			return err
		}

		// Check if the group already exists in the database
		oldGroup, err := m.findGroup(g)
		if err != nil && !errors.Is(err, db.NoDataFoundError{}) {
			// Unexpected error
			return err
		}
		if !errors.Is(err, db.NoDataFoundError{}) {
			// The group already exists in the database, use the existing GID to avoid permission issues.
			g.GID = &oldGroup.GID
		}

		if g.GID == nil {
			// The group does not exist in the database, so we generate a unique GID for it.
			newGroups = append(newGroups, g)
			continue
		}

		groupRows = append(groupRows, db.NewGroupRow(g.Name, *g.GID, g.UGID))
	}

	if len(newGroups) > 0 {
		for _, g := range newGroups {
			unique, err := lockedEntries.IsUniqueGroupName(g.Name)
			if err != nil {
				return err
			}
			if !unique {
				log.Warningf(context.Background(), "Group %q already exists", g.Name)
				return fmt.Errorf("another system group exists with %q name", g.Name)
			}

			gid, cleanupGID, err := m.idGenerator.GenerateGID(lockedEntries, m)
			if err != nil {
				return err
			}
			defer cleanupGID()

			g.GID = &gid
			groupRows = append(groupRows, db.NewGroupRow(g.Name, *g.GID, g.UGID))
			log.Debugf(context.Background(), "Using new GID %d for group %q", gid, u.Name)
		}
	}

	var oldLocalGroups []string
	if oldUserInfo != nil {
		for _, g := range oldUserInfo.Groups {
			if g.UGID != "" {
				// A non-empty UGID means that it's an authd group
				continue
			}
			oldLocalGroups = append(oldLocalGroups, g.Name)
		}
	}

	userRow := db.NewUserRow(u.Name, u.UID, *userPrivateGroup.GID, u.Gecos, u.Dir, u.Shell)
	if err = m.db.UpdateUserEntry(userRow, groupRows, localGroups); err != nil {
		return err
	}

	// Update local groups.
	if err := localentries.UpdateGroups(lockedEntries, u.Name, localGroups, oldLocalGroups); err != nil {
		return err
	}

	if err = checkHomeDirOwner(userRow.Dir, userRow.UID, userRow.GID); err != nil {
		log.Warningf(context.Background(), "Failed to check home directory ownership: %v", err)
	}

	return nil
}

func (m *Manager) getOldUserInfoFromDB(name string) (oldUserInfo *types.UserInfo, err error) {
	oldUser, oldGroups, oldLocalGroups, err := m.db.UserWithGroups(name)
	if err != nil && !errors.Is(err, db.NoDataFoundError{}) {
		// Unexpected error
		return nil, err
	}
	if errors.Is(err, db.NoDataFoundError{}) {
		return nil, nil
	}

	return userInfoFromUserAndGroupRows(oldUser, oldGroups, oldLocalGroups), nil
}

func compareNewUserInfoWithUserInfoFromDB(newUserInfo, dbUserInfo types.UserInfo) bool {
	if len(dbUserInfo.Groups) != len(newUserInfo.Groups) {
		return false
	}

	// The new user UID may be set or un set, but despite that we're going to use
	// the one we saved, so compare against it.
	newUserInfo.UID = dbUserInfo.UID

	// We then need to normalize the new user groups in order to be able to
	// compare the two users, in fact we may receive from the broker user entries
	// with unset or different GIDs, so let's
	for idx, g := range newUserInfo.Groups {
		oldGroupIdx := slices.IndexFunc(dbUserInfo.Groups, func(dg types.GroupInfo) bool {
			if dg.UGID == "" {
				// Do not compare through UGID if the one of the existing group is
				// empty, because we didn't store the UGID in 0.3.7 and earlier.
				return dg.Name == g.Name
			}
			return dg.UGID == g.UGID
		})
		if oldGroupIdx < 0 {
			continue
		}
		newUserInfo.Groups[idx].GID = dbUserInfo.Groups[oldGroupIdx].GID
	}

	return dbUserInfo.Equals(newUserInfo)
}

// SetUserID updates the UID of the user with the given name to the specified UID.
func (m *Manager) SetUserID(name string, uid uint32) (warnings []string, err error) {
	log.Debugf(context.TODO(), "Updating UID for user %q to %d", name, uid)

	if name == "" {
		return nil, errors.New("empty username")
	}

	if uid > math.MaxInt32 {
		return nil, fmt.Errorf("UID %d is too large to convert to int32", uid)
	}

	// Call lckpwdf to avoid race conditions with other processes which add UIDs
	err = userslocking.WriteLock()
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, userslocking.WriteUnlock()) }()

	// Check if the user already has the given UID
	oldUser, err := m.db.UserByName(name)
	if err != nil {
		return nil, err
	}
	if oldUser.UID == uid {
		warning := fmt.Sprintf("User %q already has UID %d", name, uid)
		log.Info(context.Background(), warning)
		return []string{warning}, nil
	}

	// Check if another user already has the given UID
	_, err = user.LookupId(strconv.FormatUint(uint64(uid), 10))
	var userErr user.UnknownUserIdError
	if err != nil && !errors.As(err, &userErr) {
		// Unexpected error
		return nil, err
	}
	if err == nil {
		return nil, fmt.Errorf("UID %d already exists", uid)
	}

	err = m.db.SetUserID(name, uid)
	if err != nil {
		return nil, err
	}

	// Check if the home directory is currently owned by the user.
	homeUID, _, err := getHomeDirOwner(oldUser.Dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		warning := fmt.Sprintf("Could not get owner of home directory %q", oldUser.Dir)
		log.Warningf(context.Background(), "%s: %v", warning, err)
		return []string{warning}, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		// The home directory does not exist, so we don't need to change the owner.
		log.Debugf(context.Background(), "Home directory %q for user %q does not exist, skipping ownership change", oldUser.Dir, name)
		return nil, nil
	}

	if homeUID != oldUser.UID {
		warning := fmt.Sprintf("Not changing ownership of home directory %q, because it is not owned by UID %d (current owner: %d)", oldUser.Dir, oldUser.UID, homeUID)
		log.Warning(context.Background(), warning)
		return []string{warning}, nil
	}

	// Change the ownership of all files in the home directory from the old UID to the new UID.
	err = fileutils.ChownRecursiveFrom(oldUser.Dir, oldUser.UID, 0, int32(uid), -1)
	if err != nil {
		return nil, err
	}

	return nil, nil
}

// checkGroupNameConflict checks if a group with the given name already exists.
// If it does, it checks if it has the same UGID.
func (m *Manager) checkGroupNameConflict(name string, ugid string) error {
	// First check in our database.
	existingGroup, err := m.db.GroupByName(name)
	if err != nil && !errors.Is(err, db.NoDataFoundError{}) {
		// Unexpected error
		return err
	}

	if errors.Is(err, db.NoDataFoundError{}) {
		// The group does not exist in the database, the check in the system
		// can be delayed to the registration point.
		return nil
	}

	// A group with that name already exists in the database, check if it has the same UGID.
	// Ignore it if the UGID of the existing group is empty, because we didn't store the UGID in 0.3.7 and earlier.
	if existingGroup.UGID == "" {
		return nil
	}
	if existingGroup.UGID != ugid {
		log.Errorf(context.Background(), "Group %q already exists in the database with UGID %q (expected %q)", name, existingGroup.UGID, ugid)
		return errors.New("found a different group with the same name in the database")
	}

	// The group exists in the database and has the same UGID, so we can proceed.
	return nil
}

func (m *Manager) findGroup(group types.GroupInfo) (oldGroup db.GroupRow, err error) {
	// Search by UGID first to support renaming groups
	oldGroup, err = m.db.GroupByUGID(group.UGID)
	if err == nil {
		return oldGroup, nil
	}
	if !errors.Is(err, db.NoDataFoundError{}) {
		// Unexpected error
		return oldGroup, err
	}

	// The group was not found by UGID. Search by name, because we didn't store the UGID in 0.3.7 and earlier.
	return m.db.GroupByName(group.Name)
}

func getHomeDirOwner(home string) (uid uint32, gid uint32, err error) {
	fileInfo, err := os.Stat(home)
	if err != nil {
		return 0, 0, err
	}

	sys, ok := fileInfo.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, errors.New("failed to get file info")
	}

	return sys.Uid, sys.Gid, nil
}

// checkHomeDirOwner checks if the home directory of the user is owned by the user and the user's group.
// If not, it logs a warning.
func checkHomeDirOwner(home string, uid, gid uint32) error {
	oldUID, oldGID, err := getHomeDirOwner(home)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if errors.Is(err, os.ErrNotExist) {
		// The home directory does not exist, so we don't need to check the owner.
		return nil
	}

	// Check if the home directory is owned by the user.
	if oldUID != uid && oldGID != gid {
		log.Warningf(context.Background(), "Home directory %q is not owned by UID %d and GID %d. To fix this, run `sudo chown -R %d:%d %q`.", home, uid, gid, uid, gid, home)
		return nil
	}
	if oldUID != uid {
		log.Warningf(context.Background(), "Home directory %q is not owned by UID %d. To fix this, run `sudo chown -R --from=%d %d %q`.", home, oldUID, oldUID, uid, home)
	}
	if oldGID != gid {
		log.Warningf(context.Background(), "Home directory %q is not owned by GID %d. To fix this, run `sudo chown -R --from=:%d :%d %q`.", home, oldGID, oldGID, gid, home)
	}

	return nil
}

// BrokerForUser returns the broker ID for the given user.
func (m *Manager) BrokerForUser(username string) (string, error) {
	u, err := m.db.UserByName(username)
	if err != nil {
		return "", err
	}

	return u.BrokerID, nil
}

// UpdateBrokerForUser updates the broker ID for the given user.
func (m *Manager) UpdateBrokerForUser(username, brokerID string) error {
	if err := m.db.UpdateBrokerForUser(username, brokerID); err != nil {
		return err
	}

	return nil
}

// LockUser sets the "locked" field to true for the given user.
func (m *Manager) LockUser(username string) error {
	if err := m.db.UpdateLockedFieldForUser(username, true); err != nil {
		return err
	}

	return nil
}

// UnlockUser sets the "locked" field to false for the given user.
func (m *Manager) UnlockUser(username string) error {
	if err := m.db.UpdateLockedFieldForUser(username, false); err != nil {
		return err
	}

	return nil
}

// IsUserLocked returns true if the user with the given user name is locked, false otherwise.
func (m *Manager) IsUserLocked(username string) (bool, error) {
	u, err := m.db.UserByName(username)
	if err != nil {
		return false, err
	}

	return u.Locked, nil
}

// UserByName returns the user information for the given user name.
func (m *Manager) UserByName(username string) (types.UserEntry, error) {
	usr, err := m.db.UserByName(username)
	if err != nil {
		return types.UserEntry{}, err
	}
	return userEntryFromUserRow(usr), nil
}

// UserByID returns the user information for the given user ID.
func (m *Manager) UserByID(uid uint32) (types.UserEntry, error) {
	usr, err := m.db.UserByID(uid)
	if errors.Is(err, db.NoDataFoundError{}) {
		// Check if the user is a temporary user.
		return m.preAuthRecords.UserByID(uid)
	}
	if err != nil {
		return types.UserEntry{}, err
	}
	return userEntryFromUserRow(usr), nil
}

// AllUsers returns all users.
func (m *Manager) AllUsers() ([]types.UserEntry, error) {
	// We don't return temporary users here, because they are not interesting to the user and would clutter the output
	// of `getent passwd`. Other tools should check `getpwnam`/`getpwuid` to check for conflicts, like `useradd` does.
	usrs, err := m.db.AllUsers()
	if err != nil {
		return nil, err
	}

	var usrEntries []types.UserEntry
	for _, usr := range usrs {
		usrEntries = append(usrEntries, userEntryFromUserRow(usr))
	}
	return usrEntries, err
}

// UsedUIDs returns all user IDs, including the UIDs of temporary pre-auth users.
func (m *Manager) UsedUIDs() ([]uint32, error) {
	var uids []uint32

	usrEntries, err := m.AllUsers()
	if err != nil {
		return nil, err
	}
	for _, usr := range usrEntries {
		uids = append(uids, usr.UID)
	}

	// Add temporary users from the pre-auth records.
	tempUsers, err := m.preAuthRecords.AllUsers()
	if err != nil {
		return nil, fmt.Errorf("failed to get temporary users: %w", err)
	}
	for _, tempUser := range tempUsers {
		uids = append(uids, tempUser.UID)
	}

	return uids, nil
}

// GroupByName returns the group information for the given group name.
func (m *Manager) GroupByName(groupname string) (types.GroupEntry, error) {
	grp, err := m.db.GroupWithMembersByName(groupname)
	if err != nil {
		return types.GroupEntry{}, err
	}
	return groupEntryFromGroupWithMembers(grp), nil
}

// GroupByID returns the group information for the given group ID.
func (m *Manager) GroupByID(gid uint32) (types.GroupEntry, error) {
	grp, err := m.db.GroupWithMembersByID(gid)
	if errors.Is(err, db.NoDataFoundError{}) {
		// Check if the ID will be the private-group of a temporary user.
		return m.preAuthRecords.GroupByID(gid)
	}
	if err != nil {
		return types.GroupEntry{}, err
	}
	return groupEntryFromGroupWithMembers(grp), nil
}

// AllGroups returns all groups.
func (m *Manager) AllGroups() ([]types.GroupEntry, error) {
	// Same as in AllUsers, we don't return temporary groups here.
	grps, err := m.db.AllGroupsWithMembers()
	if err != nil {
		return nil, err
	}

	var grpEntries []types.GroupEntry
	for _, grp := range grps {
		grpEntries = append(grpEntries, groupEntryFromGroupWithMembers(grp))
	}
	return grpEntries, nil
}

// UsedGIDs returns all group IDs, including the GIDs of temporary pre-auth users.
func (m *Manager) UsedGIDs() ([]uint32, error) {
	var gids []uint32

	grpEntries, err := m.AllGroups()
	if err != nil {
		return nil, err
	}
	for _, g := range grpEntries {
		gids = append(gids, g.GID)
	}

	allUsers, err := m.AllUsers()
	if err != nil {
		return nil, err
	}
	for _, u := range allUsers {
		gids = append(gids, u.GID)
	}

	// Add temporary groups from the pre-auth records.
	tempUsers, err := m.preAuthRecords.AllUsers()
	if err != nil {
		return nil, fmt.Errorf("failed to get temporary groups: %w", err)
	}
	for _, tu := range tempUsers {
		gids = append(gids, tu.GID)
	}

	return gids, nil
}

// ShadowByName returns the shadow information for the given user name.
func (m *Manager) ShadowByName(username string) (types.ShadowEntry, error) {
	usr, err := m.db.UserByName(username)
	if err != nil {
		return types.ShadowEntry{}, err
	}
	return shadowEntryFromUserRow(usr), nil
}

// AllShadows returns all shadow entries.
func (m *Manager) AllShadows() ([]types.ShadowEntry, error) {
	usrs, err := m.db.AllUsers()
	if err != nil {
		return nil, err
	}

	var shadowEntries []types.ShadowEntry
	for _, usr := range usrs {
		shadowEntries = append(shadowEntries, shadowEntryFromUserRow(usr))
	}
	return shadowEntries, err
}

// RegisterUserPreAuth registers a temporary user with a unique UID in our NSS handler (in memory, not in the database).
//
// The temporary user record is removed when UpdateUser is called with the same username.
func (m *Manager) RegisterUserPreAuth(name string) (uid uint32, err error) {
	defer decorate.OnError(&err, "failed to register pre-auth user %q", name)

	// Do a first check without the lock, so that if the user is already there
	// we don't have to go through actual locking.
	if userRow, err := m.db.UserByName(name); err == nil {
		log.Debugf(context.Background(), "user %q already exists on the system", name)
		return userRow.UID, nil
	}

	m.userManagementMu.Lock()
	defer m.userManagementMu.Unlock()

	// Repeat the check once locked, so that we are really sure that the user
	// has not been registered meanwhile.
	if userRow, err := m.db.UserByName(name); err == nil {
		log.Debugf(context.Background(), "user %q already exists on the system", name)
		return userRow.UID, nil
	}

	user, err := m.preAuthRecords.UserByLogin(name)
	if err == nil {
		log.Debugf(context.Background(), "user %q already pre-authenticated", name)
		return user.UID, nil
	}
	if err != nil && !errors.Is(err, tempentries.NoDataFoundError{}) {
		return 0, err
	}

	lockedEntries, unlockEntries, err := localentries.WithUserDBLock()
	if err != nil {
		return 0, err
	}
	defer func() { err = errors.Join(err, unlockEntries()) }()

	unique, err := lockedEntries.IsUniqueUserName(name)
	if err != nil {
		return 0, err
	}
	if !unique {
		return 0, fmt.Errorf("another system user exists with %q name", name)
	}

	uid, cleanupUID, err := m.idGenerator.GenerateUID(lockedEntries, m)
	if err != nil {
		return 0, err
	}
	defer cleanupUID()

	if err := m.preAuthRecords.RegisterPreAuthUser(name, uid); err != nil {
		return 0, err
	}

	log.Debugf(context.Background(), "Using new UID %d for temporary user %q", uid, name)
	return uid, nil
}
