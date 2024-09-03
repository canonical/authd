// Package fileutils provides utility functions for file operations.
package fileutils

import (
	"errors"
	"io"
	"os"
)

// FileExists checks if a file exists at the given path.
func FileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	return !errors.Is(err, os.ErrNotExist), nil
}

// IsDirEmpty checks if the specified directory is empty.
func IsDirEmpty(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	_, err = f.Readdirnames(1)
	if errors.Is(err, io.EOF) {
		return true, nil
	}
	return false, err
}

// Touch creates an empty file at the given path, if it doesn't already exist.
func Touch(path string) error {
	file, err := os.OpenFile(path, os.O_RDONLY|os.O_CREATE, 0o600)
	if err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	err = file.Close()
	if err != nil {
		return err
	}
	return nil
}
