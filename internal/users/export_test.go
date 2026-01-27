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
	return compareNewUserInfoWithUserInfoFromDB(newUserInfo, dbUserInfo)
}

func GetHomeDirOwner(home string) (uid uint32, gid uint32, err error) {
	return getHomeDirOwner(home)
}

const (
	SystemdDynamicUIDMin = systemdDynamicUIDMin
	SystemdDynamicUIDMax = systemdDynamicUIDMax
)
