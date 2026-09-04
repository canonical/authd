package users

import (
	"github.com/canonical/authd/internal/users/db"
	"github.com/canonical/authd/internal/users/types"
)

func (m *Manager) DB() *db.Manager {
	return m.db
}

func (m *Manager) RealIDGenerator() *IDGenerator {
	//nolint:forcetypeassert  // We really want to panic if it's not true.
	return m.idGenerator.(*IDGenerator)
}

func (m *Manager) GetOldUserInfoFromDB(name string) (oldUserInfo *types.UserInfo, err error) {
	oldUserInfo, _, err = m.getOldUserInfoFromDB(name)
	return oldUserInfo, err
}

func CompareNewUserInfoWithUserInfoFromDB(newUserInfo, dbUserInfo types.UserInfo) bool {
	return len(diffNormalizedUserInfo(newUserInfo, dbUserInfo)) == 0
}

func DiffNewUserInfoWithUserInfoFromDB(newUserInfo, dbUserInfo types.UserInfo) []string {
	return diffNormalizedUserInfo(newUserInfo, dbUserInfo)
}

func (m *Manager) UsersWithPrimaryGroup(gid uint32) ([]string, error) {
	return m.usersWithPrimaryGroup(gid)
}

func (m *Manager) Z_ForTests_Crash() error { //nolint:revive // Test-only exports use the Z_ForTests_ prefix.
	m.temporaryAliases.mu.Lock()
	m.temporaryAliases.stopped = true
	for _, timer := range m.temporaryAliases.timers {
		timer.Stop()
	}
	clear(m.temporaryAliases.leases)
	clear(m.temporaryAliases.timers)
	m.temporaryAliases.mu.Unlock()
	return m.db.Close()
}

func UserInfoStoredAs(brokerUserInfo types.UserInfo, storedName string) types.UserInfo {
	return userInfoStoredAs(brokerUserInfo, storedName)
}

type TemporaryAliasCleanupForTests struct {
	Name            string
	UID             uint32
	NewName         string
	OldBrokerID     string
	OldProviderID   string
	OldFullUsername string
	NewBrokerID     string
	NewProviderID   string
	NewFullUsername string
	LocalGroups     []string
	CurrentGroups   []string
}

//nolint:revive // Test-only exports use the Z_ForTests_ prefix.
func (m *Manager) Z_ForTests_AddTemporaryAlias(leaseID, name string, uid uint32, brokerID, providerID, fullUsername string) error {
	return m.temporaryAliases.add(leaseID, name, uid, brokerID, providerID, fullUsername)
}

//nolint:revive // Test-only exports use the Z_ForTests_ prefix.
func (m *Manager) Z_ForTests_AddJournalCleanup(cleanup TemporaryAliasCleanupForTests) error {
	return m.aliasJournal.add(temporaryAliasCleanup(cleanup))
}

//nolint:revive // Test-only exports use the Z_ForTests_ prefix.
func (m *Manager) Z_ForTests_CleanupTemporaryAlias(name string) error {
	return m.cleanupTemporaryAlias(name)
}

//nolint:revive // Test-only exports use the Z_ForTests_ prefix.
func (m *Manager) Z_ForTests_ReconcileTemporaryAliasCleanups() error {
	return m.reconcileTemporaryAliasCleanups()
}

//nolint:revive // Test-only exports use the Z_ForTests_ prefix.
func (m *Manager) Z_ForTests_RemoveTemporaryAliasFromLocalGroups(name string) bool {
	return m.removeTemporaryAliasFromLocalGroups(temporaryUserAlias{name: name})
}

//nolint:revive // Test-only exports use the Z_ForTests_ prefix.
func Z_ForTests_RenameHomeDir(dir, oldName, newName string) string {
	return renameHomeDir(dir, oldName, newName)
}

//nolint:revive // Test-only exports use the Z_ForTests_ prefix.
func (m *Manager) Z_ForTests_StopTemporaryAliases() {
	m.temporaryAliases.stop()
}

const (
	SystemdDynamicUIDMin = systemdDynamicUIDMin
	SystemdDynamicUIDMax = systemdDynamicUIDMax
)
