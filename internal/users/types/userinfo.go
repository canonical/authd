package types

import (
	"fmt"

	"github.com/canonical/authd/internal/sliceutils"
)

// Diff returns a human-readable description of the fields that differ between
// u (the "before" state) and other (the "after" state). The returned slice is
// empty when the two users are equal. Callers that only need a boolean result
// should use Equals.
func (u UserInfo) Diff(other UserInfo) []string {
	var diffs []string
	if u.Name != other.Name {
		diffs = append(diffs, fmt.Sprintf("name (%q → %q)", u.Name, other.Name))
	}
	if u.UID != other.UID {
		diffs = append(diffs, fmt.Sprintf("uid (%d → %d)", u.UID, other.UID))
	}
	if u.Gecos != other.Gecos {
		diffs = append(diffs, fmt.Sprintf("gecos (%q → %q)", u.Gecos, other.Gecos))
	}
	if u.Dir != other.Dir {
		diffs = append(diffs, fmt.Sprintf("dir (%q → %q)", u.Dir, other.Dir))
	}
	if u.Shell != other.Shell {
		diffs = append(diffs, fmt.Sprintf("shell (%q → %q)", u.Shell, other.Shell))
	}
	for _, g := range sliceutils.DifferenceFunc(other.Groups, u.Groups, GroupInfo.Equals) {
		diffs = append(diffs, fmt.Sprintf("group %q added", g.Name))
	}
	for _, g := range sliceutils.DifferenceFunc(u.Groups, other.Groups, GroupInfo.Equals) {
		diffs = append(diffs, fmt.Sprintf("group %q removed", g.Name))
	}
	return diffs
}

// Equals checks that two users are equal.
func (u UserInfo) Equals(other UserInfo) bool {
	if u.Name != other.Name ||
		u.UID != other.UID ||
		u.Gecos != other.Gecos ||
		u.Dir != other.Dir ||
		u.Shell != other.Shell {
		return false
	}

	return sliceutils.EqualContentFunc(u.Groups, other.Groups, GroupInfo.Equals)
}
