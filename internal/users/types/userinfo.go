package types

import (
	"fmt"
	"regexp"

	"github.com/canonical/authd/internal/sliceutils"
)

// usernameRegexp is the regexp that a valid username must match.
// It follows the Debian/Ubuntu username policy as defined by shadow-utils/useradd rules.
var usernameRegexp = regexp.MustCompile(`^[a-z_][-a-z0-9_]*[$]?$`)

// ValidateUsername checks if the given username is valid.
// Valid usernames follow the Debian/Ubuntu naming convention: they start with a lowercase letter or
// underscore, followed by lowercase letters, digits, hyphens, or underscores, with an optional
// trailing dollar sign.
func ValidateUsername(name string) error {
	if !usernameRegexp.MatchString(name) {
		return fmt.Errorf("username %q is not valid: it must match %s", name, usernameRegexp)
	}
	return nil
}

// Equals checks that two users are equal.
func (u UserInfo) Equals(other UserInfo) bool {
	return u.Name == other.Name &&
		u.UID == other.UID &&
		u.Gecos == other.Gecos &&
		u.Dir == other.Dir &&
		u.Shell == other.Shell &&
		sliceutils.EqualContentFunc(u.Groups, other.Groups, GroupInfo.Equals)
}
