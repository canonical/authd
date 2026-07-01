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
	return m.getOldUserInfoFromDB(name)
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

const (
	SystemdDynamicUIDMin = systemdDynamicUIDMin
	SystemdDynamicUIDMax = systemdDynamicUIDMax
)
