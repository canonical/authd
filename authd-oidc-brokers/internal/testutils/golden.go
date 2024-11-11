package testutils

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	cp "github.com/otiai10/copy"
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

// GoldenOptions are options for functions that work with golden files.
type GoldenOptions struct {
	Path string
}

func updateGoldenFile(t *testing.T, path string, data []byte) {
	t.Logf("updating golden file %s", path)
	err := os.MkdirAll(filepath.Dir(path), 0750)
	require.NoError(t, err, "Cannot create directory for updating golden files")
	err = os.WriteFile(path, data, 0600)
	require.NoError(t, err, "Cannot write golden file")
}

// CheckOrUpdateGolden compares the provided string with the content of the golden file. If the update environment
// variable is set, the golden file is updated with the provided string.
func CheckOrUpdateGolden(t *testing.T, got string, opts *GoldenOptions) {
	t.Helper()

	if opts == nil {
		opts = &GoldenOptions{}
	}
	if opts.Path == "" {
		opts.Path = GoldenPath(t)
	}

	want := LoadWithUpdateFromGolden(t, got, opts)
	require.Equal(t, want, got, "Output does not match golden file %s", opts.Path)
}

// CheckOrUpdateGoldenYAML compares the provided object with the content of the golden file. If the update environment
// variable is set, the golden file is updated with the provided object serialized as YAML.
func CheckOrUpdateGoldenYAML[E any](t *testing.T, got E, opts *GoldenOptions) {
	t.Helper()

	data, err := yaml.Marshal(got)
	require.NoError(t, err, "Cannot serialize provided object")

	CheckOrUpdateGolden(t, string(data), opts)
}

// LoadWithUpdateFromGolden loads the element from a plaintext golden file.
// It will update the file if the update flag is used prior to loading it.
func LoadWithUpdateFromGolden(t *testing.T, data string, opts *GoldenOptions) string {
	t.Helper()

	if opts == nil {
		opts = &GoldenOptions{}
	}
	if opts.Path == "" {
		opts.Path = GoldenPath(t)
	}

	if update {
		updateGoldenFile(t, opts.Path, []byte(data))
	}

	want, err := os.ReadFile(opts.Path)
	require.NoError(t, err, "Cannot load golden file")

	return string(want)
}

// LoadWithUpdateFromGoldenYAML load the generic element from a YAML serialized golden file.
// It will update the file if the update flag is used prior to deserializing it.
func LoadWithUpdateFromGoldenYAML[E any](t *testing.T, got E, opts *GoldenOptions) E {
	t.Helper()

	t.Logf("Serializing object for golden file")
	data, err := yaml.Marshal(got)
	require.NoError(t, err, "Cannot serialize provided object")
	want := LoadWithUpdateFromGolden(t, string(data), opts)

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

// TestFamilyPath returns the path of the dir for storing fixtures and other files related to the test.
func TestFamilyPath(t *testing.T) string {
	t.Helper()

	// Ensures that only the name of the parent test is used.
	super, _, _ := strings.Cut(t.Name(), "/")

	return filepath.Join("testdata", super)
}

// GoldenPath returns the golden path for the provided test.
func GoldenPath(t *testing.T) string {
	t.Helper()

	path := filepath.Join(TestFamilyPath(t), "golden")
	_, subtestName, found := strings.Cut(t.Name(), "/")
	if found {
		CheckValidGoldenFileName(t, subtestName)
		path = filepath.Join(path, subtestName)
	}

	return path
}

// CheckOrUpdateGoldenFileTree allows comparing a goldPath directory to p. Those can be updated via the dedicated flag.
func CheckOrUpdateGoldenFileTree(t *testing.T, path, goldenPath string) {
	t.Helper()

	if update {
		t.Logf("updating golden path %s", goldenPath)
		err := os.RemoveAll(goldenPath)
		require.NoError(t, err, "Cannot remove golden path %s", goldenPath)

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
			err = os.WriteFile(goldenPath, data, info.Mode())
			require.NoError(t, err, "Cannot write golden file")
		} else {
			err := addEmptyMarker(path)
			require.NoError(t, err, "Cannot add empty marker to directory %s", path)

			err = cp.Copy(path, goldenPath)
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
		goldenFilePath := filepath.Join(goldenPath, relPath)

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
		fileContent, err := os.ReadFile(p)
		require.NoError(t, err, "Cannot read file %s", p)
		goldenContent, err := os.ReadFile(goldenFilePath)
		require.NoError(t, err, "Cannot read golden file %s", goldenFilePath)
		require.Equal(t, string(fileContent), string(goldenContent), "Content does not match.\nFile: %s\nGolden file: %s", p, goldenFilePath)

		return nil
	})
	require.NoError(t, err, "Cannot walk through directory %s", path)

	// Check if there are files in the golden directory that are not in the source directory.
	err = filepath.WalkDir(goldenPath, func(p string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Ignore the ".empty" file
		if de.Name() == fileForEmptyDir {
			return nil
		}

		relPath, err := filepath.Rel(goldenPath, p)
		require.NoError(t, err, "Cannot get relative path for %s", p)
		filePath := filepath.Join(path, relPath)

		if de.IsDir() {
			return nil
		}

		_, err = os.Stat(filePath)
		require.NoError(t, err, "Missing expected file %s", filePath)

		return nil
	})
	require.NoError(t, err, "Cannot walk through directory %s", goldenPath)
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
			f.Close()
		}
		return nil
	})

	return err
}

// UpdateEnabled returns true if the update flag was set, false otherwise.
func UpdateEnabled() bool {
	return update
}
