// Package envutils provides utilities for manipulating string slices representing environment variables.
package envutils

import (
	"errors"
	"fmt"
	"strings"
)

// Getenv retrieves the value of an environment variable from a slice of strings.
func Getenv(env []string, key string) string {
	for _, kv := range env {
		if strings.HasPrefix(kv, key+"=") {
			return strings.TrimPrefix(kv, key+"=")
		}
	}
	return ""
}

// Setenv sets an environment variable in a slice of strings.
func Setenv(env []string, key, value string) ([]string, error) {
	if len(key) == 0 {
		return nil, errors.New("empty key")
	}
	if strings.ContainsAny(key, "="+"\x00") {
		return nil, fmt.Errorf("invalid key: %q", key)
	}
	if strings.ContainsRune(value, '\x00') {
		return nil, fmt.Errorf("invalid value: %q", value)
	}

	kv := fmt.Sprintf("%s=%s", key, value)

	// Check if the key is already set
	for i, kvPair := range env {
		if strings.HasPrefix(kvPair, key+"=") {
			// Key exists, update the value
			env[i] = kv
			return env, nil
		}
	}

	// Key is not set yet, append it
	env = append(env, kv)
	return env, nil
}
