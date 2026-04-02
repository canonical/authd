package testutils

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	goCoverDir     string
	goCoverDirOnce sync.Once
)

// fqdnToPath allows to return the fqdn path for this file relative to go.mod.
func fqdnToPath(t *testing.T, path string) string {
	t.Helper()

	srcPath, err := filepath.Abs(path)
	require.NoError(t, err, "Setup: can't calculate absolute path")

	d := srcPath
	for d != "/" {
		f, err := os.Open(filepath.Clean(filepath.Join(d, "go.mod")))
		if err != nil {
			d = filepath.Dir(d)
			continue
		}
		defer func() { assert.NoError(t, f.Close(), "Setup: can’t close go.mod") }()

		r := bufio.NewReader(f)
		l, err := r.ReadString('\n')
		require.NoError(t, err, "can't read go.mod first line")
		if !strings.HasPrefix(l, "module ") {
			t.Fatal(`Setup: failed to find "module" line in go.mod`)
		}

		prefix := strings.TrimSpace(strings.TrimPrefix(l, "module "))
		relpath := strings.TrimPrefix(srcPath, d)
		return filepath.Join(prefix, relpath)
	}

	t.Fatal("failed to find go.mod")
	return ""
}

// CoverDirEnv returns the cover dir env variable to run a go binary, if coverage is enabled.
func CoverDirEnv() string {
	if CoverDirForTests() == "" {
		return ""
	}
	return fmt.Sprintf("GOCOVERDIR=%s", CoverDirForTests())
}

// AppendCovEnv returns the env needed to enable coverage when running a go binary,
// if coverage is enabled.
func AppendCovEnv(env []string) []string {
	coverDir := CoverDirEnv()
	if coverDir == "" {
		return env
	}
	return append(env, coverDir)
}

// CoverDirForTests parses the test arguments and return the cover profile directory,
// if coverage is enabled.
func CoverDirForTests() string {
	goCoverDirOnce.Do(func() {
		if testing.CoverMode() == "" {
			return
		}

		for _, arg := range os.Args {
			if !strings.HasPrefix(arg, "-test.gocoverdir=") {
				continue
			}
			goCoverDir = strings.TrimPrefix(arg, "-test.gocoverdir=")
		}
	})

	return goCoverDir
}

// writeGoCoverageLine writes given line in go coverage format to w.
func writeGoCoverageLine(t *testing.T, w io.Writer, file string, lineNum, lineLength int, covered string) {
	t.Helper()

	_, err := fmt.Fprintf(w, "%s:%d.1,%d.%d 1 %s\n", file, lineNum, lineNum, lineLength, covered)
	require.NoErrorf(t, err, "Teardown: can't write a write to golang compatible cover file : %v", err)
}
