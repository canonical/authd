package pam_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// PackagedControl returns the control of the authd module for the action in
// debian/pam-configs/authd.in, translating its actions with translations.
func PackagedControl(action Action, translations map[string]string) (map[string]string, error) {
	_, file, _, _ := runtime.Caller(0)
	profile := filepath.Join(filepath.Dir(file), "..", "..", "..", "debian", "pam-configs", "authd.in")
	data, err := os.ReadFile(profile)
	if err != nil {
		return nil, err
	}

	name := action.String()
	header := strings.ToUpper(name[:1]) + name[1:] + ":"
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if line != header || i+1 >= len(lines) {
			continue
		}

		values, err := ParseControl(strings.Split(strings.TrimSpace(lines[i+1]), "\t")[0])
		if err != nil {
			return nil, err
		}
		for k, v := range values {
			if t, ok := translations[v]; ok {
				values[k] = t
			}
		}
		return values, nil
	}

	return nil, fmt.Errorf("no %s entry in %s", name, profile)
}

// ParseControl parses a PAM control in the [value=action ...] syntax.
func ParseControl(control string) (map[string]string, error) {
	if !strings.HasPrefix(control, "[") || !strings.HasSuffix(control, "]") {
		return nil, fmt.Errorf("unsupported PAM control %q", control)
	}

	values := make(map[string]string)
	for _, f := range strings.Fields(control[1 : len(control)-1]) {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			return nil, fmt.Errorf("invalid PAM control value %q in %q", f, control)
		}
		values[k] = v
	}

	return values, nil
}
