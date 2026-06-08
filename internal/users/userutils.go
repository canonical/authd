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

func checkValidShellPath(shell string) (err error) {
	// Do the same checks as systemd-homed in shell_is_ok:
	// https://github.com/systemd/systemd/blob/ba67af7efb7b743ba1974ef9ceb53fba0e3f9e21/src/home/homectl.c#L2812
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
