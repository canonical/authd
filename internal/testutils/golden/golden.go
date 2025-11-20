// Package golden provides utilities to compare and update golden files in tests.
package golden

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/otiai10/copy"
	"github.com/pmezard/go-difflib/difflib"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

var update bool

const (
	// UpdateGoldenFilesEnv is the environment variable used to indicate go test that
	// the golden files should be overwritten with the current test results.
	UpdateGoldenFilesEnv = `TESTS_UPDATE_GOLDEN`
)

func init() {
	if os.Getenv(UpdateGoldenFilesEnv) != "" {
		update = true
	}
}

type goldenOptions struct {
	path   string
	suffix string
}

// Option is a supported option reference to change the golden files comparison.
type Option func(*goldenOptions)

// WithPath overrides the default path for golden files used.
func WithPath(path string) Option {
	return func(o *goldenOptions) {
		if path != "" {
			o.path = path
		}
	}
}

// WithSuffix add a suffix to golden files used.
func WithSuffix(suffix string) Option {
	return func(o *goldenOptions) {
		if suffix != "" {
			o.suffix = suffix
		}
	}
}

func parseGoldenOptions(t *testing.T, options ...Option) goldenOptions {
	t.Helper()

	opts := goldenOptions{}
	for _, f := range options {
		f(&opts)
	}
	if !filepath.IsAbs(opts.path) {
		opts.path = filepath.Join(Path(t), opts.path)
	}

	opts.path += opts.suffix

	return opts
}

func updateGoldenFile(t *testing.T, path string, data []byte) {
	t.Helper()

	t.Logf("updating golden file %s", path)
	err := os.MkdirAll(filepath.Dir(path), 0750)
	require.NoError(t, err, "Cannot create directory for updating golden files")
	err = os.WriteFile(path, data, 0600)
	require.NoError(t, err, "Cannot write golden file")
}

// CheckOrUpdate compares the provided string with the content of the golden file. If the update environment
// variable is set, the golden file is updated with the provided string.
func CheckOrUpdate(t *testing.T, got string, options ...Option) {
	t.Helper()

	opts := parseGoldenOptions(t, options...)

	if update {
		updateGoldenFile(t, opts.path, []byte(got))
	}

	checkGoldenFileEqualsString(t, got, opts.path)
}

// CheckOrUpdateYAML compares the provided object with the content of the golden file. If the update environment
// variable is set, the golden file is updated with the provided object serialized as YAML.
func CheckOrUpdateYAML[E any](t *testing.T, got E, options ...Option) {
	t.Helper()

	data, err := yaml.Marshal(got)
	require.NoError(t, err, "Cannot serialize provided object")

	CheckOrUpdate(t, string(data), options...)
}

// LoadWithUpdate loads the element from a plaintext golden file.
// It will update the file if the update flag is used prior to loading it.
func LoadWithUpdate(t *testing.T, data string, options ...Option) string {
	t.Helper()

	opts := parseGoldenOptions(t, options...)

	if update {
		updateGoldenFile(t, opts.path, []byte(data))
	}

	want, err := os.ReadFile(opts.path)
	require.NoError(t, err, "Cannot load golden file")

	return string(want)
}

// LoadWithUpdateYAML load the generic element from a YAML serialized golden file.
// It will update the file if the update flag is used prior to deserializing it.
func LoadWithUpdateYAML[E any](t *testing.T, got E, options ...Option) E {
	t.Helper()

	t.Logf("Serializing object for golden file")
	data, err := yaml.Marshal(got)
	require.NoError(t, err, "Cannot serialize provided object")
	want := LoadWithUpdate(t, string(data), options...)

	var wantDeserialized E
	err = yaml.Unmarshal([]byte(want), &wantDeserialized)
	require.NoError(t, err, "Cannot create expanded policy objects from golden file")

	return wantDeserialized
}

// CheckValidGoldenFileName checks if the provided name is a valid golden file name.
func CheckValidGoldenFileName(t *testing.T, name string) {
	t.Helper()

	// A valid golden file contains only alphanumeric characters, underscores, dashes, and dots.
	require.Regexp(t, `^[\w\-.]+$`, name,
		"Invalid golden file name %q. Only alphanumeric characters, underscores, dashes, and dots are allowed", name)
}

// Path returns the golden path for the provided test.
func Path(t *testing.T) string {
	t.Helper()

	for _, part := range strings.Split(t.Name(), "/") {
		CheckValidGoldenFileName(t, part)
	}

	return filepath.Join(Dir(t), t.Name())
}

// Dir returns the golden directory for the provided test.
func Dir(t *testing.T) string {
	t.Helper()

	cwd, err := os.Getwd()
	require.NoError(t, err, "Cannot get current working directory")

	return filepath.Join(cwd, "testdata", "golden")
}

// runDelta pipes the unified diff through the `delta` command for word-level diff and coloring.
func runDelta(diff string) (string, error) {
	cmd := exec.Command("delta", "--diff-so-fancy", "--hunk-header-style", "omit")
	cmd.Stdin = strings.NewReader(diff)

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("failed to run delta: %w", err)
	}
	return out.String(), nil
}

// checkFileContent compares the content of the actual and golden files and reports any differences.
func checkFileContent(t *testing.T, actual, expected, actualPath, expectedPath string) {
	t.Helper()

	if actual == expected {
		return
	}

	diff := difflib.UnifiedDiff{
		A:        difflib.SplitLines(expected),
		B:        difflib.SplitLines(actual),
		FromFile: "Expected (golden)",
		ToFile:   "Actual",
		Context:  3,
	}
	diffStr, err := difflib.GetUnifiedDiffString(diff)
	require.NoError(t, err, "Cannot get unified diff")

	// Check if the `delta` command is available and use it to colorize the diff.
	_, err = exec.LookPath("delta")
	if err == nil {
		diffStr, err = runDelta(diffStr)
		require.NoError(t, err, "Cannot run delta")
	} else {
		diffStr = "\nDiff:\n" + diffStr
	}

	msg := fmt.Sprintf("Golden file: %s", expectedPath)
	if actualPath != "Actual" {
		msg += fmt.Sprintf("\nFile: %s", actualPath)
	}

	require.Failf(t, strings.Join([]string{
		"Golden file content mismatch",
		"\nExpected (golden):",
		strings.Repeat("-", 50),
		strings.TrimSuffix(expected, "\n"),
		strings.Repeat("-", 50),
		"\nActual: ",
		strings.Repeat("-", 50),
		strings.TrimSuffix(actual, "\n"),
		strings.Repeat("-", 50),
		diffStr,
	}, "\n"), msg)
}

func checkGoldenFileEqualsFile(t *testing.T, path, goldenPath string) {
	t.Helper()

	fileContent, err := os.ReadFile(path)
	require.NoError(t, err, "Cannot read file %s", path)
	goldenContent, err := os.ReadFile(goldenPath)
	require.NoError(t, err, "Cannot read golden file %s", goldenPath)

	checkFileContent(t, string(fileContent), string(goldenContent), path, goldenPath)
}

func checkGoldenFileEqualsString(t *testing.T, got, goldenPath string) {
	t.Helper()

	goldenContent, err := os.ReadFile(goldenPath)
	require.NoError(t, err, "Cannot read golden file %s", goldenPath)

	checkFileContent(t, got, string(goldenContent), "Actual", goldenPath)
}

// CheckOrUpdateFileTree allows comparing a goldPath directory to p. Those can be updated via the dedicated flag.
func CheckOrUpdateFileTree(t *testing.T, path string, options ...Option) {
	t.Helper()

	opts := parseGoldenOptions(t, options...)

	if update {
		t.Logf("updating golden path %s", opts.path)
		err := os.RemoveAll(opts.path)
		require.NoError(t, err, "Cannot remove golden path %s", opts.path)

		// check the source directory exists before trying to copy it
		info, err := os.Stat(path)
		if errors.Is(err, fs.ErrNotExist) {
			return
		}
		require.NoErrorf(t, err, "Error on checking %q", path)

		if !info.IsDir() {
			// copy file
			data, err := os.ReadFile(path)
			require.NoError(t, err, "Cannot read file %s", path)
			err = os.WriteFile(opts.path, data, info.Mode())
			require.NoError(t, err, "Cannot write golden file")
		} else {
			err := addEmptyMarker(path)
			require.NoError(t, err, "Cannot add empty marker to directory %s", path)

			err = copy.Copy(path, opts.path)
			require.NoError(t, err, "Can’t update golden directory")
		}
	}

	// Compare the content and attributes of the files in the directories.
	err := filepath.WalkDir(path, func(p string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(path, p)
		require.NoError(t, err, "Cannot get relative path for %s", p)
		goldenFilePath := filepath.Join(opts.path, relPath)

		if de.IsDir() {
			return nil
		}

		goldenFile, err := os.Stat(goldenFilePath)
		if errors.Is(err, fs.ErrNotExist) {
			require.Failf(t, "Unexpected file %s", p)
		}
		require.NoError(t, err, "Cannot get golden file %s", goldenFilePath)

		file, err := os.Stat(p)
		require.NoError(t, err, "Cannot get file %s", p)

		// Compare executable bit
		a := strconv.FormatInt(int64(goldenFile.Mode().Perm()&0o111), 8)
		b := strconv.FormatInt(int64(file.Mode().Perm()&0o111), 8)
		require.Equal(t, a, b, "Executable bit does not match.\nFile: %s\nGolden file: %s", p, goldenFilePath)

		// Compare content
		checkGoldenFileEqualsFile(t, p, goldenFilePath)

		return nil
	})
	require.NoError(t, err, "Cannot walk through directory %s", path)

	// Check if there are files in the golden directory that are not in the source directory.
	err = filepath.WalkDir(opts.path, func(p string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Ignore the ".empty" file
		if de.Name() == fileForEmptyDir {
			return nil
		}

		relPath, err := filepath.Rel(opts.path, p)
		require.NoError(t, err, "Cannot get relative path for %s", p)
		filePath := filepath.Join(path, relPath)

		if de.IsDir() {
			return nil
		}

		_, err = os.Stat(filePath)
		require.NoError(t, err, "Missing expected file %s", filePath)

		return nil
	})
	require.NoError(t, err, "Cannot walk through directory %s", opts.path)
}

const fileForEmptyDir = ".empty"

// addEmptyMarker adds to any empty directory, fileForEmptyDir to it.
// That allows git to commit it.
func addEmptyMarker(p string) error {
	err := filepath.WalkDir(p, func(path string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !de.IsDir() {
			return nil
		}

		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			f, err := os.Create(filepath.Join(path, fileForEmptyDir))
			if err != nil {
				return err
			}
			err = f.Close()
			if err != nil {
				return err
			}
		}
		return nil
	})

	return err
}

// UpdateEnabled returns true if the update flag was set, false otherwise.
func UpdateEnabled() bool {
	return update
}
