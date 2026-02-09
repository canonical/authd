package types

import "github.com/canonical/authd/internal/sliceutils"

// Equals checks that two users are equal.
func (u UserInfo) Equals(other UserInfo) bool {
	return u.Name == other.Name &&
		u.UID == other.UID &&
		u.Gecos == other.Gecos &&
		u.Dir == other.Dir &&
		u.Shell == other.Shell &&
		sliceutils.EqualContentFunc(u.Groups, other.Groups, GroupInfo.Equals)
}
