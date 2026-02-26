package types

// Equals checks that two groups are equal.
func (u GroupInfo) Equals(other GroupInfo) bool {
	if u.Name != other.Name ||
		u.UGID != other.UGID {
		return false
	}

	if u.GID == nil || other.GID == nil {
		return u.GID == other.GID
	}

	return *u.GID == *other.GID
}
