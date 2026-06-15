package users

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"unicode"
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

	if strings.ContainsFunc(value, unicode.IsControl) {
		return errors.New("value cannot contain control characters")
	}

	return nil
}

// checkValidPasswdPath checks if the provided path is valid using the same rules as systemd-homed's shell_is_ok function:
//
// 1. The path must be an absolute path.
// 2. The path must be normalized (i.e., it must not contain any redundant components like "." or "..").
// 3. The path must not exceed the maximum allowed length (PATH_MAX - 1).
func checkValidPasswdPath(argPath string) (err error) {
	if err = checkValidPasswdField(argPath); err != nil {
		return err
	}

	if !path.IsAbs(argPath) {
		return errors.New("must be absolute an absolute path")
	}

	if argPath != path.Clean(argPath) {
		return errors.New("path must be normalized")
	}

	// PATH_MAX is counted with the terminating null byte
	if unix.PathMax-1 < len(argPath) {
		return errors.New("path is too long")
	}

	return nil
}

func checkValidShell(shell string) (err error) {
	// Check if the shell exists and is executable
	stat, err := os.Stat(shell)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("shell '%s' does not exist", shell)
	}

	if stat.IsDir() || stat.Mode()&0111 == 0 {
		return fmt.Errorf("shell '%s' is not an executable file", shell)
	}

	// Check if the shell is in the list of allowed shells in /etc/shells
	f, err := os.Open("/etc/shells")
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line == shell {
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	return fmt.Errorf("shell '%s' is not allowed in /etc/shells", shell)
}
