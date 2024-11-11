package testutils

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
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

type goldenOptions struct {
	goldenPath string
}

// GoldenOption is a supported option reference to change the golden files comparison.
type GoldenOption func(*goldenOptions)

// WithGoldenPath overrides the default path for golden files used.
func WithGoldenPath(path string) GoldenOption {
	return func(o *goldenOptions) {
		if path != "" {
			o.goldenPath = path
		}
	}
}

// LoadWithUpdateFromGolden loads the element from a plaintext golden file.
// It will update the file if the update flag is used prior to loading it.
func LoadWithUpdateFromGolden(t *testing.T, data string, opts ...GoldenOption) string {
	t.Helper()

	o := goldenOptions{
		goldenPath: GoldenPath(t),
	}

	for _, opt := range opts {
		opt(&o)
	}

	if update {
		t.Logf("updating golden file %s", o.goldenPath)
		err := os.MkdirAll(filepath.Dir(o.goldenPath), 0750)
		require.NoError(t, err, "Cannot create directory for updating golden files")
		err = os.WriteFile(o.goldenPath, []byte(data), 0600)
		require.NoError(t, err, "Cannot write golden file")
	}

	want, err := os.ReadFile(o.goldenPath)
	require.NoError(t, err, "Cannot load golden file")

	return string(want)
}

// LoadWithUpdateFromGoldenYAML load the generic element from a YAML serialized golden file.
// It will update the file if the update flag is used prior to deserializing it.
func LoadWithUpdateFromGoldenYAML[E any](t *testing.T, got E, opts ...GoldenOption) E {
	t.Helper()

	t.Logf("Serializing object for golden file")
	data, err := yaml.Marshal(got)
	require.NoError(t, err, "Cannot serialize provided object")
	want := LoadWithUpdateFromGolden(t, string(data), opts...)

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

// CompareTreesWithFiltering allows comparing a goldPath directory to p. Those can be updated via the dedicated flag.
// It will filter dconf database and not commit it in the new golden directory.
func CompareTreesWithFiltering(t *testing.T, p, goldPath string, update bool) {
	t.Helper()

	// UpdateEnabled golden file
	if update {
		t.Logf("updating golden file %s", goldPath)
		require.NoError(t, os.RemoveAll(goldPath), "Cannot remove target golden directory")

		// check the source directory exists before trying to copy it
		info, err := os.Stat(p)
		if errors.Is(err, fs.ErrNotExist) {
			return
		}
		require.NoErrorf(t, err, "Error on checking %q", p)

		if !info.IsDir() {
			// copy file
			data, err := os.ReadFile(p)
			require.NoError(t, err, "Cannot read new generated file file %s", p)
			require.NoError(t, os.WriteFile(goldPath, data, info.Mode()), "Cannot write golden file")
		} else {
			err := addEmptyMarker(p)
			require.NoError(t, err, "Cannot add empty marker to directory %s", p)

			err = cp.Copy(p, goldPath)
			require.NoError(t, err, "Can’t update golden directory")
		}
	}

	var gotContent map[string]treeAttrs
	if _, err := os.Stat(p); err == nil {
		gotContent, err = treeContentAndAttrs(t, p, nil)
		if err != nil {
			t.Fatalf("No generated content: %v", err)
		}
	}

	var goldContent map[string]treeAttrs
	if _, err := os.Stat(goldPath); err == nil {
		goldContent, err = treeContentAndAttrs(t, goldPath, nil)
		if err != nil {
			t.Fatalf("No golden directory found: %v", err)
		}
	}

	// Maps are not ordered, so we need to compare the content and attributes of each file
	for key, value := range goldContent {
		require.Equal(t, value, gotContent[key], "Content or attributes are different for %s", key)
		delete(gotContent, key)
	}
	require.Empty(t, gotContent, "Some files are missing in the golden directory")

	// No more verification on p if it doesn’t exists
	if _, err := os.Stat(p); errors.Is(err, fs.ErrNotExist) {
		return
	}
}

// treeAttrs are the attributes to take into consideration when comparing each file.
type treeAttrs struct {
	content    string
	path       string
	executable bool
}

const fileForEmptyDir = ".empty"

// treeContentAndAttrs builds a recursive file list of dir with their content and other attributes.
// It can ignore files starting with ignoreHeaders.
func treeContentAndAttrs(t *testing.T, dir string, ignoreHeaders []byte) (map[string]treeAttrs, error) {
	t.Helper()

	r := make(map[string]treeAttrs)

	err := filepath.WalkDir(dir, func(path string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Ignore markers for empty directories
		if filepath.Base(path) == fileForEmptyDir {
			return nil
		}

		content := ""
		info, err := os.Stat(path)
		require.NoError(t, err, "Cannot stat %s", path)
		if !de.IsDir() {
			d, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			// ignore given header
			if ignoreHeaders != nil && bytes.HasPrefix(d, ignoreHeaders) {
				return nil
			}
			content = string(d)
		}
		trimmedPath := strings.TrimPrefix(path, dir)
		r[trimmedPath] = treeAttrs{content, strings.TrimPrefix(path, dir), info.Mode()&0111 != 0}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return r, nil
}

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
