package users

import (
	"errors"
	"path"
	"strings"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

func checkValidPasswdField(value string) (err error) {
	if value == "" {
		return errors.New("value cannot be empty")
	}

	if !utf8.ValidString(value) {
		return errors.New("value must be valid UTF-8")
	}

	if strings.ContainsRune(value, ':') {
		return errors.New("value cannot contain ':' character")
	}

	for _, r := range value {
		if r < 32 || r == 127 {
			return errors.New("value cannot contain control characters")
		}
	}

	return nil
}

func checkValidShell(shell string) (err error) {
	if err = checkValidPasswdField(shell); err != nil {
		return err
	}

	if !path.IsAbs(shell) {
		return errors.New("shell must be an absolute path")
	}

	if shell != path.Clean(shell) {
		return errors.New("shell path must be normalized")
	}

	// PATH_MAX is counted with the terminating null byte
	if unix.PathMax-1 < len(shell) {
		return errors.New("shell path is too long")
	}

	return nil
}
