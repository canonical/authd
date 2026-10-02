package types

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// ValidateUserName checks that a username can be stored by authd and written to the system files
// it manages. authd accepts names that the identity provider returns, which are usually email
// addresses, so this only rejects what would break /etc/group, /etc/passwd or the tools that read
// them.
func ValidateUserName(name string) error {
	if name == "" {
		return errors.New("username cannot be empty")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("username %q is not allowed", name)
	}

	// ':' separates the fields of /etc/passwd and /etc/group, and ',' separates the members of a
	// group. A username containing either one would be read back as several fields or members.
	for _, c := range []rune{':', ','} {
		if strings.ContainsRune(name, c) {
			return fmt.Errorf("username %q cannot contain %q character", name, c)
		}
	}

	// The username is used to build paths such as the home directory, so a '/' would escape into
	// another directory.
	if strings.ContainsRune(name, '/') {
		return fmt.Errorf("username %q cannot contain %q character", name, '/')
	}

	for _, r := range name {
		// A newline would add a line to the generated group file, and the other control characters
		// are not printable, so an administrator could not tell two names apart.
		if unicode.IsControl(r) {
			return fmt.Errorf("username %q cannot contain control characters", name)
		}
		if unicode.IsSpace(r) {
			return fmt.Errorf("username %q cannot contain whitespace", name)
		}
	}

	// A leading '-' makes the name look like an option to the command line tools that manage
	// users, and both '+' and '-' introduce NIS entries in /etc/passwd and /etc/group.
	if strings.HasPrefix(name, "-") || strings.HasPrefix(name, "+") {
		return fmt.Errorf("username %q cannot start with %q", name, name[:1])
	}

	return nil
}
