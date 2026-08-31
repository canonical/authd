// Package users support all common action on the system for user handling.
package users

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/canonical/authd/internal/decorate"
	"github.com/canonical/authd/internal/fileutils"
	"github.com/canonical/authd/internal/users/db"
	"github.com/canonical/authd/internal/users/localentries"
	userslocking "github.com/canonical/authd/internal/users/locking"
	"github.com/canonical/authd/internal/users/proc"
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

	UseShortUsernames bool `mapstructure:"use_short_usernames" yaml:"use_short_usernames"`
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

	db               *db.Manager
	config           Config
	preAuthRecords   *tempentries.PreAuthUserRecords
	temporaryAliases *temporaryUserAliases
	aliasJournal     *temporaryAliasJournal
	idGenerator      IDGeneratorIface
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
	m.aliasJournal, err = newTemporaryAliasJournal(dbDir)
	if err != nil {
		_ = m.db.Close()
		return nil, err
	}
	m.temporaryAliases = newTemporaryUserAliases(m.removeTemporaryAliasFromLocalGroups)
	if err := m.reconcileTemporaryAliasCleanups(); err != nil {
		_ = m.db.Close()
		return nil, err
	}

	return m, nil
}

// Stop closes the underlying db.
func (m *Manager) Stop() error {
	m.temporaryAliases.stop()
	return m.db.Close()
}

// UpdateUser updates the user information in the db.
func (m *Manager) UpdateUser(brokerUserInfo types.UserInfo) (err error) {
	defer decorate.OnError(&err, "failed to update user %q", brokerUserInfo.Name)

	log.Debugf(context.TODO(), "Updating user %q", brokerUserInfo.Name)

	if brokerUserInfo.Name == "" {
		return errors.New("empty username")
	}
	if brokerUserInfo.ProviderID != "" && brokerUserInfo.BrokerID == "" {
		return fmt.Errorf("provider ID for user %q is not scoped by a broker ID", brokerUserInfo.Name)
	}

	// Brokers always report the fully qualified username, which is the identity everything below is
	// resolved against. The name authd stores the user under is derived from it, and is decided
	// again on every check pass because it depends on what the database holds at that moment.
	fullUsername := brokerUserInfo.Name

	var u types.UserInfo
	var userPrivateGroup *types.GroupInfo
	var oldUserInfo *types.UserInfo
	var oldUserRow db.UserRow
	var pendingDiffs []string
	var lookupName string
	checkUserNeedsUpdate := func(lockedEntries *localentries.UserDBLocked) (needsUpdate bool, err error) {
		// Resolve the row this user is already stored as, whatever name it currently carries, then
		// decide the name to store them under and rewrite the broker-provided info to match.
		matchedRow, matched, err := m.resolveStoredUser(fullUsername, brokerUserInfo.BrokerID, brokerUserInfo.ProviderID)
		if err != nil {
			return false, err
		}

		storedName, err := m.nameToStoreUserUnder(fullUsername, matchedRow, matched, lockedEntries)
		if err != nil {
			return false, err
		}
		if alias, exists := m.temporaryAliases.lookup(storedName); exists &&
			(!matched || !temporaryAliasMatchesUser(alias, matchedRow)) {
			return false, fmt.Errorf("username %q is temporarily reserved by another user", storedName)
		}
		if cleanup, exists := m.aliasJournal.get(storedName); exists &&
			(!matched || cleanup.UID != matchedRow.UID) {
			return false, fmt.Errorf("username %q is pending cleanup for another user", storedName)
		}

		u = userInfoStoredAs(brokerUserInfo, storedName)
		// Prepend the user private group. Its UGID is the fully qualified username, so that the
		// group keeps its identity — and therefore its GID — even when authd stores the user under
		// a shortened name.
		u.Groups = append([]types.GroupInfo{{Name: storedName, UGID: fullUsername}}, u.Groups...)
		userPrivateGroup = &u.Groups[0]

		// The matched row is this very user, so it is the row to compare against and to rename,
		// whatever name it is currently stored under.
		lookupName = storedName
		if matched {
			lookupName = matchedRow.Name
		}

		// Check if the user already exists in the database.
		oldUserInfo, oldUserRow, err = m.getOldUserInfoFromDB(lookupName)
		if err != nil {
			return false, err
		}
		if oldUserInfo == nil {
			// A brand new user authenticated by a broker must come with a stable provider
			// identifier so we can reliably re-identify them across username changes at the IdP.
			if u.BrokerID != "" && u.ProviderID == "" {
				return false, fmt.Errorf("broker %q did not provide a provider ID for new user %q; the broker may need to be updated to a version that identifies users by a stable provider ID", u.BrokerID, u.Name)
			}
			return true, nil
		}
		if oldUserInfo.BrokerID != "" && u.BrokerID != "" && oldUserInfo.BrokerID != u.BrokerID {
			// The broker ID scopes the stored provider ID and must not change once set: a user
			// is bound to the broker they first authenticated with.
			return false, fmt.Errorf("user %q is already bound to broker %q and cannot authenticate with broker %q",
				u.Name, oldUserInfo.BrokerID, u.BrokerID)
		}
		if oldUserInfo.BrokerID != "" {
			// The broker ID scopes the stored provider ID and should not change after it is set.
			u.BrokerID = oldUserInfo.BrokerID
		}
		if oldUserInfo.BrokerID == "" && u.BrokerID != "" {
			// First login after migration: persist broker ID in DB.
			return true, nil
		}
		if oldUserInfo.ProviderID != "" && u.ProviderID == "" {
			// Preserve already-recorded stable identity when brokers don't provide it (v2).
			u.ProviderID = oldUserInfo.ProviderID
		}
		if oldUserInfo.ProviderID == "" && u.ProviderID != "" {
			// First login after migration: persist provider ID in DB.
			return true, nil
		}
		if lookupName != u.Name {
			// Username changed (provider-ID matched rename): always trigger an update.
			return true, nil
		}
		if oldUserRow.FullUsername != fullUsername {
			// Only the domain of the fully qualified username changed, so the stored name stays
			// the same and the diff below would not notice. The stored full username is what maps
			// the user back to the name the brokers know them by, so it has to be refreshed.
			return true, nil
		}
		pendingDiffs = diffNormalizedUserInfo(u, *oldUserInfo)
		if len(pendingDiffs) == 0 {
			log.Debugf(context.TODO(), "User %q in database is up to date with current user info", u.Name)
			return false, nil
		}
		log.Debugf(context.TODO(), "User %q exists in database but needs update", u.Name)
		return true, nil
	}

	// Do a first check before locking, so that if the user is already there and
	// matches the DB entry, we can avoid any kind of locking (and so being
	// blocked by other pre-auth users that may try to login meanwhile).
	needsUpdate, err := checkUserNeedsUpdate(nil)
	if err != nil {
		return err
	}
	if !needsUpdate {
		return m.retireCanonicalAlias(oldUserRow.UID, u.Name)
	}

	m.userManagementMu.Lock()
	defer m.userManagementMu.Unlock()

	lockedEntries, unlockEntries, err := localentries.WithUserDBLock()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlockEntries()) }()

	// Now that we're locked, check again if meanwhile some other request
	// created the same user. The system account lock also makes the final short-name decision
	// atomic with the user and group uniqueness checks below.
	if needsUpdate, err := checkUserNeedsUpdate(lockedEntries); err != nil {
		return err
	} else if !needsUpdate {
		return m.retireCanonicalAlias(oldUserRow.UID, u.Name)
	}

	if oldUserInfo == nil {
		log.Debugf(context.TODO(), "User %q needs update: new user", u.Name)
	} else {
		log.Debugf(context.TODO(), "User %q needs update: %s", u.Name, strings.Join(pendingDiffs, ", "))
	}

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

	var groupRows []db.GroupRow
	var localGroups []string
	var newGroups []types.GroupInfo
	for i := range u.Groups {
		g := &u.Groups[i]
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
		//
		// The user private group is exempt when the user is already stored under this very name:
		// the group found under it is then their own. Its UGID is the fully qualified username,
		// which changes when only the domain changes at the IdP, and that has to re-identify the
		// existing group rather than be reported as a conflicting one.
		if g != userPrivateGroup || oldUserInfo == nil || oldUserInfo.Name != u.Name {
			if err := m.checkGroupNameConflict(g.Name, g.UGID); err != nil {
				return err
			}
		}

		// Check if the group already exists in the database
		oldGroup, err := m.findGroup(*g)
		if err != nil && !errors.Is(err, db.NoDataFoundError{}) {
			// Unexpected error
			return err
		}
		if !errors.Is(err, db.NoDataFoundError{}) {
			// The group already exists in the database, use the existing GID to avoid permission issues.
			g.GID = &oldGroup.GID
		}

		if g.GID == nil {
			// The group does not exist in the database.
			if g == userPrivateGroup {
				// On first login the user private group doesn't exist yet, so we default to GID = UID.
				// Subsequent logins will find the existing group above and preserve any custom GID.
				g.GID = &u.UID
			} else {
				// Else, we add it to the list of new groups to create, since we need to generate a GID for it.
				newGroups = append(newGroups, *g)
				continue
			}
		}

		groupRows = append(groupRows, db.NewGroupRow(g.Name, *g.GID, g.UGID))
	}

	if len(newGroups) > 0 {
		for _, g := range newGroups {
			unique, err := lockedEntries.IsUniqueGroupName(g.Name)
			if err != nil {
				return err
			}
			// If a system group with that name already exists, we log a warning and skip the creation of this group.
			if !unique {
				log.Warningf(context.Background(), "Group '%[1]s' already exists on the system, skipping creation. To have the user added to this local group, add them to the IdP group 'linux-%[1]s'.", g.Name)
				continue
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

	userRow := db.NewUserRow(u.Name, u.UID, *userPrivateGroup.GID, u.Gecos, u.Dir, u.Shell, u.BrokerID, u.ProviderID, fullUsername)

	if err := m.temporaryAliases.protectNameForUID(u.UID, u.Name); err != nil {
		return err
	}
	aliases := m.temporaryAliases.forUID(u.UID)
	cleanupGroups := slices.Clone(oldLocalGroups)
	for _, group := range localGroups {
		if !slices.Contains(cleanupGroups, group) {
			cleanupGroups = append(cleanupGroups, group)
		}
	}
	for _, alias := range aliases {
		if len(cleanupGroups) == 0 {
			continue
		}
		// Journal the cleanup before the database update, so that a crash or a failed cleanup
		// can be reconciled at startup. The record carries the exact pre- and post-update
		// identities, and recovery only replays the cleanup when the committed row matches one
		// of them, so an uncommitted update is never applied.
		if err := m.aliasJournal.add(temporaryAliasCleanup{
			Name:            alias.name,
			UID:             u.UID,
			NewName:         u.Name,
			OldBrokerID:     oldUserRow.BrokerID,
			OldProviderID:   oldUserRow.ProviderID,
			OldFullUsername: oldUserRow.FullUsername,
			NewBrokerID:     alias.brokerID,
			NewProviderID:   alias.providerID,
			NewFullUsername: alias.fullUsername,
			LocalGroups:     cleanupGroups,
			CurrentGroups:   slices.Clone(localGroups),
		}); err != nil {
			return err
		}
	}
	if err = m.db.UpdateUserEntry(userRow, groupRows, localGroups); err != nil {
		return err
	}

	// Update local groups.
	if err := localentries.UpdateGroups(lockedEntries, u.Name, localGroups, oldLocalGroups); err != nil {
		return err
	}

	// Temporary aliases must carry the user's current local memberships while PAM still refers to
	// the old name. They never keep groups removed by the broker.
	for _, alias := range aliases {
		if alias.name == u.Name {
			continue
		}
		if err := localentries.UpdateGroups(lockedEntries, alias.name, localGroups, oldLocalGroups); err != nil {
			return fmt.Errorf("failed to update temporary alias %q in local groups: %w", alias.name, err)
		}
	}

	// Direct callers do not prepare a temporary alias. Keep their rename behavior unchanged.
	if oldUserInfo != nil && lookupName != u.Name &&
		!slices.ContainsFunc(aliases, func(alias temporaryUserAlias) bool { return alias.name == lookupName }) &&
		len(oldLocalGroups) > 0 {
		if err := localentries.UpdateGroups(lockedEntries, lookupName, nil, oldLocalGroups); err != nil {
			return fmt.Errorf("failed to remove old username %q from local groups: %w", lookupName, err)
		}
	}
	if err := m.retireCanonicalAlias(u.UID, u.Name); err != nil {
		return err
	}

	if err = checkHomeDirOwner(userRow.Dir, userRow.UID, userRow.GID); err != nil {
		log.Warningf(context.Background(), "Failed to check home directory ownership: %v", err)
	}

	return nil
}

func (m *Manager) getOldUserInfoFromDB(name string) (oldUserInfo *types.UserInfo, oldUserRow db.UserRow, err error) {
	oldUser, oldGroups, oldLocalGroups, err := m.db.UserWithGroups(name)
	if err != nil && !errors.Is(err, db.NoDataFoundError{}) {
		// Unexpected error
		return nil, db.UserRow{}, err
	}
	if errors.Is(err, db.NoDataFoundError{}) {
		return nil, db.UserRow{}, nil
	}

	return userInfoFromUserAndGroupRows(oldUser, oldGroups, oldLocalGroups), oldUser, nil
}

// diffNormalizedUserInfo normalizes newUserInfo for comparison against dbUserInfo
// (overriding UID and group GIDs to match existing DB values, since those are
// assigned by authd and not by the broker) and returns the diff between the two.
// An empty slice means the users are equal.
func diffNormalizedUserInfo(newUserInfo, dbUserInfo types.UserInfo) []string {
	// The new user UID may be set or unset, but we're going to use the one we
	// saved, so normalize it before comparing.
	newUserInfo.UID = dbUserInfo.UID

	// Normalize group GIDs: the broker may send different or absent GIDs, so
	// match each new group to its existing DB counterpart (by UGID, falling
	// back to name for legacy records where UGID was not stored).
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

	return dbUserInfo.Diff(newUserInfo)
}

// SetUserIDResp is the response type of SetUserID.
type SetUserIDResp struct {
	IDChanged           bool
	HomeDirOwnerChanged bool
	Warnings            []string
}

// SetUserID updates the UID of the user with the given name to the specified UID.
func (m *Manager) SetUserID(name string, uid uint32) (resp *SetUserIDResp, err error) {
	log.Debugf(context.TODO(), "Updating UID for user %q to %d", name, uid)
	resp = &SetUserIDResp{}

	if name == "" {
		return nil, errors.New("empty username")
	}

	if uid > math.MaxInt32 {
		return nil, fmt.Errorf("UID %d is too large to convert to int32", uid)
	}

	m.userManagementMu.Lock()
	defer m.userManagementMu.Unlock()

	// Call lckpwdf to avoid race conditions with other processes which add UIDs
	err = userslocking.WriteLock()
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, userslocking.WriteUnlock()) }()

	// Check if the user exists
	oldUser, err := m.db.UserByName(name)
	if err != nil {
		return nil, err
	}
	// Check if the user already has the given UID
	if oldUser.UID == uid {
		warning := fmt.Sprintf("User '%s' already has UID %d.", name, uid)
		log.Info(context.Background(), warning)
		resp.Warnings = append(resp.Warnings, warning)
		return resp, nil
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

	// Check if the user has active processes
	err = proc.CheckUserBusy(name, oldUser.UID)
	if err != nil {
		return nil, err
	}

	err = m.db.SetUserID(name, uid)
	if err != nil {
		return nil, err
	}
	resp.IDChanged = true

	// Check if the home directory is currently owned by the user.
	homeUID, _, err := getHomeDirOwner(oldUser.Dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		warning := fmt.Sprintf("Warning: Could not get owner of home directory '%s', not updating ownership.", oldUser.Dir)
		log.Warningf(context.Background(), "%s: %v", warning, err)
		resp.Warnings = append(resp.Warnings, warning)
		return resp, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		// The home directory does not exist, so we don't need to change the owner.
		log.Debugf(context.Background(), "Home directory %q for user %q does not exist, skipping ownership change", oldUser.Dir, name)
		return resp, nil
	}

	if homeUID != oldUser.UID {
		warning := fmt.Sprintf("Warning: Not updating ownership of home directory '%s' because it is not owned by UID %d (current owner: %d).", oldUser.Dir, oldUser.UID, homeUID)
		log.Warning(context.Background(), warning)
		resp.Warnings = append(resp.Warnings, warning)
		return resp, nil
	}

	// Change the ownership of all files in the home directory from the old UID to the new UID.
	log.Debugf(context.Background(), "Changing ownership of home directory %q from UID %d to UID %d", oldUser.Dir, oldUser.UID, uid)
	err = fileutils.ChownRecursiveFrom(
		oldUser.Dir,
		&fileutils.ChownUIDArgs{FromUID: oldUser.UID, ToUID: uid},
		nil,
	)
	if err != nil {
		return resp, err
	}
	resp.HomeDirOwnerChanged = true

	return resp, nil
}

// SetGroupIDResp is the response type of SetGroupID.
type SetGroupIDResp struct {
	IDChanged           bool
	HomeDirOwnerChanged bool
	Warnings            []string
}

// SetGroupID updates the GID of the group with the given name to the specified GID.
func (m *Manager) SetGroupID(name string, gid uint32) (resp *SetGroupIDResp, err error) {
	log.Debugf(context.TODO(), "Updating GID for group %q to %d", name, gid)
	resp = &SetGroupIDResp{}

	if name == "" {
		return nil, errors.New("empty group name")
	}

	if gid > math.MaxInt32 {
		return nil, fmt.Errorf("GID %d is too large to convert to int32", gid)
	}

	m.userManagementMu.Lock()
	defer m.userManagementMu.Unlock()

	// Call lckpwdf to avoid race conditions with other processes which add GIDs
	err = userslocking.WriteLock()
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, userslocking.WriteUnlock()) }()

	// Check if the group already has the given GID
	oldGroup, err := m.db.GroupByName(name)
	if err != nil {
		return nil, err
	}
	if oldGroup.GID == gid {
		warning := fmt.Sprintf("Group '%s' already has GID %d.", name, gid)
		log.Info(context.Background(), warning)
		resp.Warnings = append(resp.Warnings, warning)
		return resp, nil
	}

	// Check if another group already has the given GID
	_, err = user.LookupGroupId(strconv.FormatUint(uint64(gid), 10))
	var userErr user.UnknownGroupIdError
	if err != nil && !errors.As(err, &userErr) {
		// Unexpected error
		return nil, err
	}
	if err == nil {
		return nil, fmt.Errorf("GID %d already exists", gid)
	}

	userRows, err := m.db.SetGroupID(name, gid)
	if err != nil {
		return nil, err
	}
	resp.IDChanged = true

	for _, userRow := range userRows {
		changed, warning, updateErr := m.updateUserHomeDirOwnership(userRow, oldGroup.GID, gid)
		if updateErr != nil {
			err = errors.Join(err, updateErr)
		}
		if warning != "" {
			resp.Warnings = append(resp.Warnings, warning)
		}
		resp.HomeDirOwnerChanged = changed
	}

	return resp, err
}

func (m *Manager) updateUserHomeDirOwnership(userRow db.UserRow, oldGID uint32, newGID uint32) (changed bool, warning string, err error) {
	// Check if the home directory is currently owned by the group
	_, homeGID, err := getHomeDirOwner(userRow.Dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		warning := fmt.Sprintf("Warning: Could not get owner of home directory '%s', not updating ownership.", userRow.Dir)
		log.Warningf(context.Background(), "%s: %v", warning, err)
		return false, warning, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		// The home directory does not exist, so we don't need to change the owner.
		log.Debugf(context.Background(), "Not updating ownership of home directory %q for user %q because it does not exist", userRow.Dir, userRow.Name)
		return false, "", nil
	}

	if homeGID != oldGID {
		warning := fmt.Sprintf("Warning: Not updating ownership of home directory '%s' because it is not owned by GID %d (current owner: %d).", userRow.Dir, oldGID, homeGID)
		log.Warning(context.Background(), warning)
		return false, warning, nil
	}

	// Change the ownership of all files in the home directory from the old GID to the new GID.
	log.Debugf(context.Background(), "Changing ownership of home directory %q from GID %d to GID %d", userRow.Dir, oldGID, newGID)
	err = fileutils.ChownRecursiveFrom(
		userRow.Dir,
		nil,
		&fileutils.ChownGIDArgs{FromGID: oldGID, ToGID: newGID},
	)
	if err != nil {
		return false, "", err
	}

	return true, "", nil
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

// SetShell sets the shell for the given user.
func (m *Manager) SetShell(username, shell string) (warnings []string, err error) {
	if username == "" {
		return nil, errors.New("empty username")
	}

	m.userManagementMu.Lock()
	defer m.userManagementMu.Unlock()

	// Check if the user exists
	_, err = m.db.UserByName(username)
	if err != nil {
		return nil, err
	}

	err = checkValidPasswdPath(shell)
	if err != nil {
		return nil, fmt.Errorf("invalid shell: %w", err)
	}

	err = checkValidShell(shell)
	if err != nil {
		// We allow root to set an invalid shell but print a warning
		warnings = append(warnings, fmt.Sprintf("Warning: %s", err.Error()))
	}

	if err = m.db.SetShell(username, shell); err != nil {
		return warnings, err
	}

	return warnings, nil
}

// SetHomeDirResp is the response type of SetHomeDir.
type SetHomeDirResp struct {
	HomeDirChanged bool
	HomeDirMoved   bool
	Warnings       []string
}

// SetHomeDir updates the home directory of the user with the given name to the
// specified path. If the user's current home directory exists, its contents are
// moved to the new location; the move is performed with rename(2), so the new
// path must reside on the same filesystem as the current one.
func (m *Manager) SetHomeDir(name, home string) (resp *SetHomeDirResp, err error) {
	log.Debugf(context.TODO(), "Updating home directory for user %q to %q", name, home)
	resp = &SetHomeDirResp{}

	if name == "" {
		return nil, errors.New("empty username")
	}

	if err = checkValidPasswdPath(home); err != nil {
		return nil, fmt.Errorf("invalid homedir: %w", err)
	}

	m.userManagementMu.Lock()
	defer m.userManagementMu.Unlock()

	// Check if the user exists.
	oldUser, err := m.db.UserByName(name)
	if err != nil {
		return nil, err
	}

	// Check if the user already has the given home directory.
	if oldUser.Dir == home {
		warning := fmt.Sprintf("User '%s' already has home directory '%s'.", name, home)
		log.Info(context.Background(), warning)
		resp.Warnings = append(resp.Warnings, warning)
		return resp, nil
	}

	// Check if the user has active processes.
	if err = proc.CheckUserBusy(name, oldUser.UID); err != nil {
		return nil, err
	}

	// Refuse to overwrite an existing path at the destination.
	if _, err = os.Lstat(home); err == nil {
		return nil, fmt.Errorf("new home directory '%s' already exists", home)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("could not check new home directory '%s': %w", home, err)
	}

	// If the current home directory does not exist, only update the database
	// record without creating the new directory (this mirrors `usermod -m`).
	if _, err = os.Lstat(oldUser.Dir); errors.Is(err, os.ErrNotExist) {
		log.Debugf(context.Background(), "Home directory %q for user %q does not exist, only updating the database record", oldUser.Dir, name)
		if err = m.db.SetHomeDir(name, home); err != nil {
			return nil, err
		}
		resp.HomeDirChanged = true
		warning := fmt.Sprintf("Warning: Current home directory '%s' does not exist, not creating the new one.", oldUser.Dir)
		log.Warning(context.Background(), warning)
		resp.Warnings = append(resp.Warnings, warning)
		return resp, nil
	} else if err != nil {
		return nil, fmt.Errorf("could not check current home directory '%s': %w", oldUser.Dir, err)
	}

	// Move the home directory to the new location. Do this before updating the
	// database so that a failure leaves the user record untouched.
	log.Debugf(context.Background(), "Moving home directory of user %q from %q to %q", name, oldUser.Dir, home)
	if err = fileutils.Lrename(oldUser.Dir, home); err != nil {
		if errors.Is(err, syscall.EXDEV) {
			return nil, fmt.Errorf("cannot move home directory across filesystems (EXDEV); move %q manually (e.g. via a temporary path), then re-run this command to update the database to %q (which must not already exist)", oldUser.Dir, home)
		}
		return nil, fmt.Errorf("failed to move home directory from '%s' to '%s': %w", oldUser.Dir, home, err)
	}

	if err = m.db.SetHomeDir(name, home); err != nil {
		// Best-effort rollback of the move to keep the database and the
		// filesystem consistent.
		log.Warningf(context.Background(), "Could not update record for user %q in the database. Rolling back the home directory to its original location %q.", name, oldUser.Dir)
		if rerr := fileutils.Lrename(home, oldUser.Dir); rerr != nil {
			log.Warningf(context.Background(), "Failed to move home directory back to %q: %v. Try moving it manually.", oldUser.Dir, rerr)
		}
		return nil, err
	}
	resp.HomeDirChanged = true
	resp.HomeDirMoved = true

	return resp, nil
}

// BrokerForUser returns the broker ID for the given user.
func (m *Manager) BrokerForUser(username string) (string, error) {
	u, err := m.db.UserByName(username)
	if err != nil {
		return "", err
	}

	return u.BrokerID, nil
}

// BrokerAndProviderIDForUser returns the broker ID and the stable provider identifier recorded
// for the user in a single database lookup. Both values are empty if not recorded (pre-migration
// user, v2 broker, or local user).
func (m *Manager) BrokerAndProviderIDForUser(username string) (brokerID, providerID string, err error) {
	u, err := m.db.UserByName(username)
	if err != nil {
		return "", "", err
	}

	return u.BrokerID, u.ProviderID, nil
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

// DeleteUser removes the user with the given name from the database.
// If removeHome is true, the user's home directory is also removed.
func (m *Manager) DeleteUser(username string, removeHome bool) (err error) {
	m.userManagementMu.Lock()
	defer m.userManagementMu.Unlock()

	userRow, err := m.db.UserByName(username)
	if err != nil {
		return err
	}

	lockedEntries, unlockEntries, err := localentries.WithUserDBLock()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlockEntries()) }()

	// Remove the user from any local groups they are a member of.
	_, _, localGroups, err := m.db.UserWithGroups(username)
	if err != nil {
		return err
	}
	if err := localentries.UpdateGroups(lockedEntries, username, nil, localGroups); err != nil {
		return err
	}

	if err := m.db.DeleteUser(userRow.UID); err != nil {
		return err
	}

	// Delete the user's primary group only if no remaining user has it as primary.
	primaryUserNames, err := m.usersWithPrimaryGroup(userRow.GID)
	if err != nil {
		return fmt.Errorf("failed to check for users with primary group for user %q: %w", username, err)
	}
	if len(primaryUserNames) == 0 {
		if err := m.db.DeleteGroup(userRow.GID); err != nil {
			return fmt.Errorf("failed to delete primary group for user %q: %w", username, err)
		}
	}

	if removeHome && userRow.Dir != "" {
		if err := os.RemoveAll(userRow.Dir); err != nil {
			return fmt.Errorf("failed to remove home directory %q for user %q: %w", userRow.Dir, username, err)
		}
	}

	return nil
}

// usersWithPrimaryGroup returns the names of users for which the given GID is
// their primary group. It returns an empty slice when no such users exist.
func (m *Manager) usersWithPrimaryGroup(gid uint32) ([]string, error) {
	primaryUsers, err := m.db.UsersWithPrimaryGroup(gid)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(primaryUsers))
	for _, u := range primaryUsers {
		names = append(names, u.Name)
	}
	return names, nil
}

// DeleteGroup removes the group with the given name from the database.
func (m *Manager) DeleteGroup(groupname string) error {
	m.userManagementMu.Lock()
	defer m.userManagementMu.Unlock()

	groupRow, err := m.db.GroupByName(groupname)
	if err != nil {
		return err
	}

	primaryUserNames, err := m.usersWithPrimaryGroup(groupRow.GID)
	if err != nil {
		return fmt.Errorf("failed to check for users with primary group %q: %w", groupname, err)
	}
	if len(primaryUserNames) > 0 {
		return GroupIsPrimaryError{GroupName: groupname, Users: primaryUserNames}
	}

	return m.db.DeleteGroup(groupRow.GID)
}

// IsUserLocked returns true if the user with the given user name is locked, false otherwise.
func (m *Manager) IsUserLocked(username string) (bool, error) {
	u, err := m.db.UserByName(username)
	if err != nil {
		return false, err
	}

	return u.Locked, nil
}

// IsAuthenticatedUserLocked reports whether the user a broker has just authenticated is locked.
//
// It resolves the user the same way an update would, so that the lock is honored whatever name the
// row currently carries: a user renamed at the IdP, or stored under a shortened name, is still the
// same person and must stay locked out. An unknown user is not locked, since there is no record
// saying otherwise.
func (m *Manager) IsAuthenticatedUserLocked(fullUsername, brokerID, providerID string) (bool, error) {
	row, matched, err := m.resolveStoredUser(fullUsername, brokerID, providerID)
	if err != nil || !matched {
		return false, err
	}

	return row.Locked, nil
}

// UserByName returns the user information for the given user name.
func (m *Manager) UserByName(username string) (types.UserEntry, error) {
	usr, err := m.db.UserByName(username)
	if errors.Is(err, db.NoDataFoundError{}) {
		if aliasUser, ok, aliasErr := m.userByTemporaryAlias(username); aliasErr != nil {
			return types.UserEntry{}, aliasErr
		} else if ok {
			usr, err = aliasUser, nil
		}
	}
	if err != nil {
		return types.UserEntry{}, err
	}
	entry := userEntryFromUserRow(usr)
	if entry.Name != username {
		entry.Name = username
	}
	return entry, nil
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
	aliases, err := m.temporaryAliasNamesByUser()
	if err != nil {
		return types.GroupEntry{}, err
	}
	return groupEntryWithTemporaryAliases(grp, aliases), nil
}

// StoredGroupName resolves the name authd stores a group under from the name a caller knows it by.
//
// A user private group is named after its owner, so it is renamed along with them when authd stores
// them under a shortened name, and must stay reachable by both names just like the user is. Every
// other group is named by the identity provider and has a single name. A name matching no group is
// returned unchanged, so that the caller still reports it as not found.
func (m *Manager) StoredGroupName(groupname string) (string, error) {
	_, err := m.db.GroupByName(groupname)
	if err == nil {
		return groupname, nil
	}
	if !errors.Is(err, db.NoDataFoundError{}) {
		return "", err
	}

	if owner, ok, err := m.userByTemporaryAlias(groupname); err != nil {
		return "", err
	} else if ok {
		privateGroup, err := m.db.GroupByID(owner.GID)
		if err != nil {
			return "", err
		}
		return privateGroup.Name, nil
	}

	owner, err := m.db.UserByFullUsername(groupname)
	if errors.Is(err, db.NoDataFoundError{}) {
		return groupname, nil
	}
	if err != nil {
		return "", err
	}

	// The private group is the owner's primary group, so their GID names it.
	privateGroup, err := m.db.GroupByID(owner.GID)
	if errors.Is(err, db.NoDataFoundError{}) {
		return groupname, nil
	}
	if err != nil {
		return "", err
	}

	return privateGroup.Name, nil
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
	aliases, err := m.temporaryAliasNamesByUser()
	if err != nil {
		return types.GroupEntry{}, err
	}
	return groupEntryWithTemporaryAliases(grp, aliases), nil
}

// AllGroups returns all groups.
func (m *Manager) AllGroups() ([]types.GroupEntry, error) {
	// Same as in AllUsers, we don't return temporary groups here.
	grps, err := m.db.AllGroupsWithMembers()
	if err != nil {
		return nil, err
	}

	aliases, err := m.temporaryAliasNamesByUser()
	if err != nil {
		return nil, err
	}
	var grpEntries []types.GroupEntry
	for _, grp := range grps {
		grpEntries = append(grpEntries, groupEntryWithTemporaryAliases(grp, aliases))
	}
	return grpEntries, nil
}

func (m *Manager) temporaryAliasNamesByUser() (map[string][]string, error) {
	aliasesByUser := make(map[string][]string)
	for _, alias := range m.temporaryAliases.all() {
		user, ok, err := m.userByTemporaryAlias(alias.name)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if !slices.Contains(aliasesByUser[user.Name], alias.name) {
			aliasesByUser[user.Name] = append(aliasesByUser[user.Name], alias.name)
		}
	}
	return aliasesByUser, nil
}

func groupEntryWithTemporaryAliases(group db.GroupWithMembers, aliasesByUser map[string][]string) types.GroupEntry {
	entry := groupEntryFromGroupWithMembers(group)
	for _, user := range slices.Clone(entry.Users) {
		for _, alias := range aliasesByUser[user] {
			if !slices.Contains(entry.Users, alias) {
				entry.Users = append(entry.Users, alias)
			}
		}
	}
	return entry
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
	if errors.Is(err, db.NoDataFoundError{}) {
		if aliasUser, ok, aliasErr := m.userByTemporaryAlias(username); aliasErr != nil {
			return types.ShadowEntry{}, aliasErr
		} else if ok {
			usr, err = aliasUser, nil
		}
	}
	if err != nil {
		return types.ShadowEntry{}, err
	}
	entry := shadowEntryFromUserRow(usr)
	entry.Name = username
	return entry, nil
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

// ShortUsernameAllowed returns true if short usernames are allowed by the configuration, false otherwise.
func (m *Manager) ShortUsernameAllowed() bool {
	return m.config.UseShortUsernames
}

// PrepareUserAliases keeps names that an update may invalidate resolvable until PAM has finished
// its account checks: pam_unix re-resolves PAM_USER through NSS during pam_acct_mgmt, after the
// update was already applied. See the temporaryaliases.go file header for the full rationale. It
// returns the aliases that were prepared.
func (m *Manager) PrepareUserAliases(leaseID string, brokerUserInfo types.UserInfo) ([]string, error) {
	if brokerUserInfo.Name == "" {
		return nil, errors.New("empty username")
	}
	if brokerUserInfo.ProviderID != "" && brokerUserInfo.BrokerID == "" {
		return nil, fmt.Errorf("provider ID for user %q is not scoped by a broker ID", brokerUserInfo.Name)
	}

	matchedRow, matched, err := m.resolveStoredUser(
		brokerUserInfo.Name, brokerUserInfo.BrokerID, brokerUserInfo.ProviderID)
	if err != nil || !matched {
		return nil, err
	}

	nameToStore := brokerUserInfo.Name
	if m.config.UseShortUsernames {
		if shortName, _, found := strings.Cut(brokerUserInfo.Name, "@"); found {
			nameToStore = shortName
		}
	}

	var aliases []string
	if matchedRow.Name != nameToStore {
		aliases = append(aliases, matchedRow.Name)
	}
	if matchedRow.FullUsername != "" && matchedRow.FullUsername != brokerUserInfo.Name &&
		!slices.Contains(aliases, matchedRow.FullUsername) {
		aliases = append(aliases, matchedRow.FullUsername)
	}
	for _, alias := range aliases {
		aliasBrokerID := matchedRow.BrokerID
		if aliasBrokerID == "" {
			aliasBrokerID = brokerUserInfo.BrokerID
		}
		aliasProviderID := matchedRow.ProviderID
		if aliasProviderID == "" {
			aliasProviderID = brokerUserInfo.ProviderID
		}
		if err := m.temporaryAliases.add(leaseID, alias, matchedRow.UID,
			aliasBrokerID, aliasProviderID, brokerUserInfo.Name); err != nil {
			m.temporaryAliases.remove(leaseID)
			return nil, err
		}
	}
	return aliases, nil
}

// RetainUserAlias keeps an alias used to start another PAM transaction alive for that transaction.
func (m *Manager) RetainUserAlias(updateID, name string) (bool, error) {
	return m.temporaryAliases.retain(updateID, name)
}

// HasUserAlias reports whether a PAM transaction owns a temporary user alias.
func (m *Manager) HasUserAlias(leaseID string) bool {
	return m.temporaryAliases.has(leaseID)
}

// CancelUserAlias removes the temporary alias owned by a failed user update.
func (m *Manager) CancelUserAlias(leaseID string) {
	m.temporaryAliases.remove(leaseID)
}

// ReleaseUserAlias lets the temporary alias expire shortly after PAM account checks finish.
func (m *Manager) ReleaseUserAlias(leaseID string) bool {
	return m.temporaryAliases.complete(leaseID)
}

// UserEntryStoredAs rewrites a broker-provided entry for a user authd does not know yet to the name
// it would store them under. The pre-authentication path needs it so that the entry it hands out
// agrees with the one created after a successful login, down to the home directory.
func (m *Manager) UserEntryStoredAs(entry types.UserEntry) (result types.UserEntry, err error) {
	lockedEntries, unlockEntries, err := localentries.WithUserDBLock()
	if err != nil {
		return types.UserEntry{}, err
	}
	defer func() { err = errors.Join(err, unlockEntries()) }()

	storedName, err := m.nameToStoreUserUnder(entry.Name, db.UserRow{}, false, lockedEntries)
	if err != nil {
		return types.UserEntry{}, err
	}
	if storedName == entry.Name {
		return entry, nil
	}

	entry.Dir = renameHomeDir(entry.Dir, entry.Name, storedName)
	entry.Name = storedName

	return entry, nil
}

// NamesForLogin resolves the name authd stores the user under, together with the fully qualified
// name the brokers expect, from the name the user is logging in with. Both forms are accepted, so
// they are both looked up. Returns a NoDataFoundError if the user is not known yet.
func (m *Manager) NamesForLogin(loginName string) (storedName, fullUsername string, err error) {
	storedName, fullUsername, _, err = m.namesForLogin(loginName, "")
	return storedName, fullUsername, err
}

// NamesForLoginAndRetainAlias resolves loginName and atomically retains it when it is a temporary
// alias. This prevents the alias from expiring while a broker session is being created.
func (m *Manager) NamesForLoginAndRetainAlias(loginName, leaseID string) (storedName, fullUsername string, aliasRetained bool, err error) {
	return m.namesForLogin(loginName, leaseID)
}

func (m *Manager) namesForLogin(loginName, leaseID string) (storedName, fullUsername string, aliasRetained bool, err error) {
	userRow, err := m.db.UserByName(loginName)
	if errors.Is(err, db.NoDataFoundError{}) {
		if leaseID == "" {
			if aliasUser, ok, aliasErr := m.userByTemporaryAlias(loginName); aliasErr != nil {
				return "", "", false, aliasErr
			} else if ok {
				userRow, err = aliasUser, nil
			}
		} else if _, retained, retainErr := m.temporaryAliases.lookupAndRetain(leaseID, loginName); retainErr != nil {
			return "", "", false, retainErr
		} else if retained {
			aliasRetained = true
			if aliasUser, ok, aliasErr := m.userByTemporaryAlias(loginName); aliasErr != nil {
				m.temporaryAliases.remove(leaseID)
				return "", "", false, aliasErr
			} else if ok {
				userRow, err = aliasUser, nil
			} else {
				m.temporaryAliases.remove(leaseID)
				aliasRetained = false
			}
		}
	}
	if errors.Is(err, db.NoDataFoundError{}) {
		userRow, err = m.db.UserByFullUsername(loginName)
	}
	if err != nil {
		return "", "", false, err
	}

	fullUsername = userRow.FullUsername
	if fullUsername == "" {
		// Rows created before the full_username column existed (or by brokers which never provided
		// one) store the full username as the name.
		fullUsername = userRow.Name
	}

	return userRow.Name, fullUsername, aliasRetained, nil
}

func (m *Manager) userByTemporaryAlias(name string) (db.UserRow, bool, error) {
	alias, ok := m.temporaryAliases.lookup(name)
	if !ok {
		return db.UserRow{}, false, nil
	}

	current, err := m.db.UserByName(name)
	if err == nil {
		if !temporaryAliasMatchesUser(alias, current) {
			return db.UserRow{}, false, nil
		}
		return current, true, nil
	}
	if !errors.Is(err, db.NoDataFoundError{}) {
		return db.UserRow{}, false, err
	}

	user, err := m.db.UserByID(alias.uid)
	if errors.Is(err, db.NoDataFoundError{}) {
		return db.UserRow{}, false, nil
	}
	if err != nil {
		return db.UserRow{}, false, err
	}
	if !temporaryAliasMatchesUser(alias, user) {
		return db.UserRow{}, false, nil
	}

	return user, true, nil
}

func temporaryAliasMatchesUser(alias temporaryUserAlias, user db.UserRow) bool {
	if alias.providerID != "" {
		return user.BrokerID == alias.brokerID && user.ProviderID == alias.providerID
	}
	return user.FullUsername == alias.fullUsername
}

func (m *Manager) retireCanonicalAlias(uid uint32, name string) error {
	if err := m.aliasJournal.remove(name); err != nil {
		return err
	}
	m.temporaryAliases.discardNameForUID(uid, name)
	return nil
}

func (m *Manager) removeTemporaryAliasFromLocalGroups(alias temporaryUserAlias) bool {
	if err := m.cleanupTemporaryAlias(alias.name); err != nil {
		log.Errorf(context.Background(), "Could not clean up temporary user alias %q: %v", alias.name, err)
		return false
	}
	return true
}

func (m *Manager) reconcileTemporaryAliasCleanups() error {
	for _, cleanup := range m.aliasJournal.all() {
		if err := m.cleanupTemporaryAlias(cleanup.Name); err != nil {
			return fmt.Errorf("could not recover temporary user alias %q: %w", cleanup.Name, err)
		}
	}
	return nil
}

func (m *Manager) cleanupTemporaryAlias(name string) error {
	m.userManagementMu.Lock()
	defer m.userManagementMu.Unlock()

	cleanup, ok := m.aliasJournal.get(name)
	if !ok {
		return nil
	}
	current, currentErr := m.db.UserByID(cleanup.UID)
	if currentErr == nil && current.Name == cleanup.Name &&
		userRowMatchesIdentity(current, cleanup.OldBrokerID, cleanup.OldProviderID, cleanup.OldFullUsername) {
		return m.aliasJournal.remove(cleanup.Name)
	}
	if currentErr != nil && !errors.Is(currentErr, db.NoDataFoundError{}) {
		return currentErr
	}

	lockedEntries, unlockEntries, err := localentries.WithUserDBLock()
	if err != nil {
		return err
	}

	var updateErr error
	if currentErr == nil && current.Name == cleanup.NewName &&
		userRowMatchesIdentity(current, cleanup.NewBrokerID, cleanup.NewProviderID, cleanup.NewFullUsername) {
		updateErr = localentries.UpdateGroups(
			lockedEntries, current.Name, cleanup.CurrentGroups, cleanup.LocalGroups)
	}
	if err := localentries.UpdateGroups(lockedEntries, cleanup.Name, nil, cleanup.LocalGroups); err != nil {
		updateErr = errors.Join(updateErr, err)
	}
	unlockErr := unlockEntries()
	if err := errors.Join(updateErr, unlockErr); err != nil {
		return err
	}
	return m.aliasJournal.remove(cleanup.Name)
}

func userRowMatchesIdentity(user db.UserRow, brokerID, providerID, fullUsername string) bool {
	return user.BrokerID == brokerID && user.ProviderID == providerID && user.FullUsername == fullUsername
}

// UserByFullUsername returns the user information for the user stored under the given full username.
func (m *Manager) UserByFullUsername(fullUsername string) (types.UserEntry, error) {
	userRow, err := m.db.UserByFullUsername(fullUsername)
	if err != nil {
		return types.UserEntry{}, err
	}
	return userEntryFromUserRow(userRow), nil
}

// resolveStoredUser finds the row a user is already stored as, whatever name it currently carries.
// It is the single place that decides what "the same user" means, so that every caller agrees.
//
// Two things identify a user across a rename. The broker-scoped provider ID (sub/oid) is the stable
// identity and takes precedence: it survives a username change at the IdP. The fully qualified
// username is the fallback, and the only proof available for rows written before the brokers
// reported a provider ID or by v2 brokers, which never report one.
func (m *Manager) resolveStoredUser(fullUsername, brokerID, providerID string) (row db.UserRow, matched bool, err error) {
	if brokerID != "" && providerID != "" {
		providerIDMatch, err := m.db.UserByProviderID(brokerID, providerID)
		if err != nil && !errors.Is(err, db.NoDataFoundError{}) {
			return db.UserRow{}, false, fmt.Errorf("failed to look up user by provider ID: %w", err)
		}
		if err == nil {
			if providerIDMatch.FullUsername != fullUsername {
				log.Noticef(context.TODO(), "User identified by broker ID %q and provider ID %q: username changed from %q to %q",
					brokerID, providerID, providerIDMatch.FullUsername, fullUsername)
			}
			return providerIDMatch, true, nil
		}
	}

	fullUsernameMatch, err := m.db.UserByFullUsername(fullUsername)
	if err != nil && !errors.Is(err, db.NoDataFoundError{}) {
		return db.UserRow{}, false, fmt.Errorf("failed to look up user by full username: %w", err)
	}
	if err == nil {
		return fullUsernameMatch, true, nil
	}

	// An older authd version can add a row after the full_username migration has already run. Such
	// a row keeps the fully qualified username in name and leaves full_username empty. Accept only
	// that exact legacy form: a non-empty, different full_username means the stored name is merely
	// a local alias and must not identify the broker user.
	nameMatch, err := m.db.UserByName(fullUsername)
	if err != nil && !errors.Is(err, db.NoDataFoundError{}) {
		return db.UserRow{}, false, fmt.Errorf("failed to look up user by stored name: %w", err)
	}
	if err == nil && nameMatch.FullUsername == "" {
		return nameMatch, true, nil
	}

	return db.UserRow{}, false, nil
}

// nameToStoreUserUnder returns the name authd should store the given user under, which is the
// shortened form of their fully qualified username when the configuration asks for it.
//
// Distinct fully qualified usernames can shorten to the same name. The first user to claim it keeps
// it, and any other user falls back to their fully qualified name: they are both still able to log
// in, under different-looking names. Refusing the second user instead would make an account
// unusable for good, and which of the two lost would depend on who happened to log in first.
func (m *Manager) nameToStoreUserUnder(fullUsername string, matchedRow db.UserRow, matched bool, lockedEntries *localentries.UserDBLocked) (string, error) {
	if !m.config.UseShortUsernames {
		return fullUsername, nil
	}

	shortName, _, found := strings.Cut(fullUsername, "@")
	if !found {
		// The name carries no domain, so it is already as short as it gets.
		return fullUsername, nil
	}

	if matched && matchedRow.Name == shortName {
		// The user already holds the short name, so there is nothing to check.
		return shortName, nil
	}

	holder, err := m.db.UserByName(shortName)
	if err != nil && !errors.Is(err, db.NoDataFoundError{}) {
		return "", err
	}
	if err == nil {
		holderName := holder.FullUsername
		if holderName == "" {
			holderName = holder.Name
		}
		log.Warningf(context.TODO(), "Username %q is already used by %q, so %q keeps their fully qualified name",
			shortName, holderName, fullUsername)
		return fullUsername, nil
	}

	// The first, unlocked check only decides whether an update may be needed. The final check runs
	// with the system account database locked, so a local user or private group cannot claim the
	// short name between this decision and the database update.
	if lockedEntries == nil {
		return shortName, nil
	}

	unique, err := lockedEntries.IsUniqueUserName(shortName)
	if err != nil {
		return "", err
	}
	if !unique {
		log.Warningf(context.TODO(), "Username %q is already used by a system user, so %q keeps their fully qualified name",
			shortName, fullUsername)
		return fullUsername, nil
	}

	unique, err = lockedEntries.IsUniqueGroupName(shortName)
	if err != nil {
		return "", err
	}
	if !unique {
		log.Warningf(context.TODO(), "Group name %q is already used by a system group, so %q keeps their fully qualified name",
			shortName, fullUsername)
		return fullUsername, nil
	}

	return shortName, nil
}

// userInfoStoredAs returns the broker-provided user information rewritten to be stored under the
// given name. The name is the key authd stores the user under, so every name-derived field has to
// follow it. Group UGIDs are left untouched: they are stable identities, not names.
//
// The result never shares memory with the input, because the caller owns the information the broker
// reported and must keep seeing it unchanged.
func userInfoStoredAs(brokerUserInfo types.UserInfo, storedName string) types.UserInfo {
	u := brokerUserInfo
	u.Groups = slices.Clone(brokerUserInfo.Groups)

	if storedName == brokerUserInfo.Name {
		return u
	}

	// The broker may report a self-named group, which has to follow the user name.
	for i := range u.Groups {
		if u.Groups[i].Name == brokerUserInfo.Name {
			u.Groups[i].Name = storedName
		}
	}
	u.Dir = renameHomeDir(u.Dir, brokerUserInfo.Name, storedName)
	u.Name = storedName

	return u
}

// renameHomeDir returns dir with a trailing oldName element replaced by newName. Only the last
// element is considered: the username is what the home directory is named after, and it may well
// appear in the path of the directory holding it, which is not ours to rewrite.
func renameHomeDir(dir, oldName, newName string) string {
	if dir == "" || filepath.Base(dir) != oldName {
		return dir
	}

	return filepath.Join(filepath.Dir(dir), newName)
}
