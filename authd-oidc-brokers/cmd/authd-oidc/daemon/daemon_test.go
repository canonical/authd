package daemon_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/canonical/authd/authd-oidc-brokers/cmd/authd-oidc/daemon"
	"github.com/canonical/authd/authd-oidc-brokers/internal/consts"
	"github.com/canonical/authd/authd-oidc-brokers/internal/testutils"
	"github.com/canonical/authd/log"
	"github.com/stretchr/testify/require"
)

var issuerURL string

func TestHelp(t *testing.T) {
	a := daemon.NewForTests(t, nil, issuerURL, "--help")

	getStdout := captureStdout(t)

	err := a.Run()
	require.NoErrorf(t, err, "Run should not return an error with argument --help. Stdout: %v", getStdout())
}

func TestCompletion(t *testing.T) {
	a := daemon.NewForTests(t, nil, issuerURL, "completion", "bash")

	getStdout := captureStdout(t)

	err := a.Run()
	require.NoError(t, err, "Completion should not start the daemon. Stdout: %v", getStdout())
}

func TestVersion(t *testing.T) {
	a := daemon.NewForTests(t, nil, issuerURL, "version")

	getStdout := captureStdout(t)

	err := a.Run()
	require.NoError(t, err, "Run should not return an error")

	out := getStdout()

	fields := strings.Fields(out)
	require.Len(t, fields, 2, "wrong number of fields in version: %s", out)

	require.Equal(t, t.Name(), fields[0], "Wrong executable name")
	require.Equal(t, consts.Version, fields[1], "Wrong version")
}

func TestNoUsageError(t *testing.T) {
	a := daemon.NewForTests(t, nil, issuerURL, "completion", "bash")

	getStdout := captureStdout(t)
	err := a.Run()

	require.NoError(t, err, "Run should not return an error, stdout: %v", getStdout())
	isUsageError := a.UsageError()
	require.False(t, isUsageError, "No usage error is reported as such")
}

func TestUsageError(t *testing.T) {
	a := daemon.NewForTests(t, nil, issuerURL, "doesnotexist")

	err := a.Run()
	require.Error(t, err, "Run should return an error, stdout: %v")
	isUsageError := a.UsageError()
	require.True(t, isUsageError, "Usage error is reported as such")
}

func TestCanQuitWhenExecute(t *testing.T) {
	a, wait := startDaemon(t, nil)
	defer wait()

	a.Quit()
}

func TestCanQuitTwice(t *testing.T) {
	a, wait := startDaemon(t, nil)

	a.Quit()
	wait()

	require.NotPanics(t, a.Quit)
}

func TestAppCanQuitWithoutExecute(t *testing.T) {
	t.Skipf("This test is skipped because it is flaky. There is no way to guarantee Quit has been called before run.")

	a := daemon.NewForTests(t, nil, issuerURL)

	requireGoroutineStarted(t, a.Quit)
	err := a.Run()
	require.Error(t, err, "Should return an error")

	require.Containsf(t, err.Error(), "grpc: the server has been stopped", "Unexpected error message")
}

func TestAppRunFailsOnComponentsCreationAndQuit(t *testing.T) {
	const (
		// DataDir errors
		dirIsFile = iota
		noParentDir
	)

	tests := map[string]struct {
		dataDirBehavior int
		configBehavior  int
	}{
		"Error_on_existing_data_dir_being_a_file":    {dataDirBehavior: dirIsFile},
		"Error_on_data_dir_missing_parent_directory": {dataDirBehavior: noParentDir},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tmpDir := t.TempDir()
			dataDir := filepath.Join(tmpDir, "data")

			switch tc.dataDirBehavior {
			case dirIsFile:
				err := os.WriteFile(dataDir, []byte("file"), 0600)
				require.NoError(t, err, "Setup: could not create cache file for tests")
			case noParentDir:
				dataDir = filepath.Join(dataDir, "doesnotexist", "data")
			}

			config := daemon.DaemonConfig{
				Verbosity: 0,
				Paths: daemon.SystemPaths{
					DataDir: dataDir,
				},
			}

			a := daemon.NewForTests(t, &config, issuerURL)
			err := a.Run()
			require.Error(t, err, "Run should return an error")
		})
	}
}

func TestAppRunFailsOnInsecureBrokerConfigPerms(t *testing.T) {
	t.Setenv("SNAP_DATA", "")
	tests := map[string]struct {
		mainConfPerm   os.FileMode
		dropInFileName string
		dropInFilePerm os.FileMode
		dropInDirPerm  os.FileMode
		wantError      string
	}{
		"Error_on_wrong_permission_on_broker_conf": {
			mainConfPerm: 0644,
			wantError:    "has permissions",
		},
		"Error_on_wrong_permission_on_drop_in_config_file": {
			dropInFileName: "extra.yaml",
			dropInFilePerm: 0644,
			wantError:      "has permissions",
		},
		"Error_on_writable_drop_in_directory": {
			dropInDirPerm: 0777,
			wantError:     "must not be writable by group or others",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tmpDir := t.TempDir()
			brokerConf := filepath.Join(tmpDir, "broker.yaml")
			config := daemon.DaemonConfig{
				Paths: daemon.SystemPaths{
					BrokerConf: brokerConf,
				},
			}

			a := daemon.NewForTests(t, &config, issuerURL)

			if tc.mainConfPerm != 0 {
				err := os.Chmod(brokerConf, tc.mainConfPerm)
				require.NoError(t, err, "Setup: could not change permission on broker config file for tests")
			}

			if tc.dropInFileName != "" || tc.dropInDirPerm != 0 {
				dropInDir := brokerConf + ".d"
				err := os.MkdirAll(dropInDir, 0700)
				require.NoError(t, err, "Setup: could not create drop-in directory for tests")
				//nolint:gosec // The drop-in directory is expected to have 0755 permissions
				err = os.Chmod(dropInDir, 0755)
				require.NoError(t, err, "Setup: could not set permissions on drop-in directory for tests")
				if tc.dropInDirPerm != 0 {
					require.NoError(t, os.Chmod(dropInDir, tc.dropInDirPerm))
				}
				if tc.dropInFileName != "" {
					dropInFile := filepath.Join(dropInDir, tc.dropInFileName)
					err = os.WriteFile(dropInFile, []byte("[users]\nallowed_users = OWNER\n"), tc.dropInFilePerm)
					require.NoError(t, err, "Setup: could not create drop-in config file for tests")
					err = os.Chmod(dropInFile, tc.dropInFilePerm)
					require.NoError(t, err, "Setup: could not set permissions on drop-in config file for tests")
				}
			}

			err := a.Run()
			require.ErrorContains(t, err, tc.wantError)
			for path, mode := range map[string]os.FileMode{
				brokerConf: tc.mainConfPerm,
				filepath.Join(brokerConf+".d", tc.dropInFileName): tc.dropInFilePerm,
				brokerConf + ".d": tc.dropInDirPerm,
			} {
				if mode == 0 {
					continue
				}
				info, err := os.Stat(path)
				require.NoError(t, err)
				require.Equal(t, mode, info.Mode().Perm(), "strict validation must not change permissions")
			}
		})
	}
}

func TestAppRunSelectsBrokerConfigValidationPolicy(t *testing.T) {
	tests := map[string]struct {
		snap            bool
		hasLegacyMarker bool
		wantLegacy      bool
	}{
		"snap_without_marker_uses_strict_validation": {snap: true},
		"snap_with_legacy_marker_allows_legacy_validation": {
			snap:            true,
			hasLegacyMarker: true,
			wantLegacy:      true,
		},
		"non_snap_uses_strict_validation": {},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			snapData := t.TempDir()
			if tc.snap {
				t.Setenv("SNAP_DATA", snapData)
			} else {
				t.Setenv("SNAP_DATA", "")
			}
			if tc.hasLegacyMarker {
				marker := filepath.Join(snapData, ".allow-legacy-config-pre-0.5.0")
				require.NoError(t, os.WriteFile(marker, nil, 0600))
			}

			tmpDir := t.TempDir()
			brokerConf := filepath.Join(tmpDir, "broker.conf")
			config := daemon.DaemonConfig{
				Paths: daemon.SystemPaths{
					BrokerConf: brokerConf,
					DataDir:    filepath.Join(tmpDir, "data"),
				},
			}
			a := daemon.NewForTests(t, &config, issuerURL)

			content, err := os.ReadFile(brokerConf)
			require.NoError(t, err)
			content = append(content, []byte("\n[future]\nkey = value\n")...)
			require.NoError(t, os.WriteFile(brokerConf, content, 0600))

			if !tc.wantLegacy {
				err := a.Run()
				require.ErrorContains(t, err, `unknown section "future"`)
				return
			}

			runErr := make(chan error, 1)
			go func() {
				runErr <- a.Run()
			}()
			a.WaitReady()
			a.Quit()
			require.NoError(t, <-runErr)
			_, err = os.Lstat(filepath.Join(snapData, ".allow-legacy-config-pre-0.5.0"))
			require.NoError(t, err, "legacy config warnings must not remove the compatibility marker")
		})
	}
}

func TestLegacyInstallationPromotesAfterCleanStartAndUsesStrictValidation(t *testing.T) {
	snapData := t.TempDir()
	t.Setenv("SNAP_DATA", snapData)
	legacyMarkerPath := filepath.Join(snapData, ".allow-legacy-config-pre-0.5.0")
	require.NoError(t, os.WriteFile(legacyMarkerPath, nil, 0600))

	var notices []string
	log.SetHandler(func(_ context.Context, level log.Level, format string, args ...interface{}) {
		if level == log.NoticeLevel {
			notices = append(notices, fmt.Sprintf(format, args...))
		}
	})
	t.Cleanup(func() { log.SetHandler(nil) })

	tmpDir := t.TempDir()
	config := daemon.DaemonConfig{
		Paths: daemon.SystemPaths{
			BrokerConf: filepath.Join(tmpDir, "broker.conf"),
			DataDir:    filepath.Join(tmpDir, "data"),
		},
	}

	app := daemon.NewForTests(t, &config, issuerURL)
	runErr := make(chan error, 1)
	go func() {
		runErr <- app.Run()
	}()
	app.WaitReady()
	app.Quit()
	require.NoError(t, <-runErr)
	require.Contains(t, strings.Join(notices, "\n"), "Strict configuration validation is now enabled")

	_, err := os.Lstat(legacyMarkerPath)
	require.ErrorIs(t, err, os.ErrNotExist, "a clean legacy start removes the compatibility marker")

	strictApp := daemon.NewForTests(t, &config, issuerURL)
	content, err := os.ReadFile(config.Paths.BrokerConf)
	require.NoError(t, err)
	content = append(content, []byte("\n[future]\nkey = value\n")...)
	require.NoError(t, os.WriteFile(config.Paths.BrokerConf, content, 0600))

	err = strictApp.Run()
	require.ErrorContains(t, err, `unknown section "future"`)
}

func TestLegacyInstallationWithOwnerOnlyDropInDirectoryAllowsBroadFileModes(t *testing.T) {
	snapData := t.TempDir()
	t.Setenv("SNAP_DATA", snapData)
	legacyMarkerPath := filepath.Join(snapData, ".allow-legacy-config-pre-0.5.0")
	require.NoError(t, os.WriteFile(legacyMarkerPath, nil, 0600))

	tmpDir := t.TempDir()
	brokerConf := filepath.Join(tmpDir, "broker.conf")
	config := daemon.DaemonConfig{
		Paths: daemon.SystemPaths{
			BrokerConf: brokerConf,
			DataDir:    filepath.Join(tmpDir, "data"),
		},
	}
	app := daemon.NewForTests(t, &config, issuerURL)

	dropInDir := brokerConf + ".d"
	require.NoError(t, os.Mkdir(dropInDir, 0700))
	dropInFile := filepath.Join(dropInDir, "extra.conf")
	require.NoError(t, os.WriteFile(dropInFile, []byte("[users]\nallowed_users = OWNER\n"), 0600))
	//nolint:gosec // A 0.4.1 owner-only directory protects drop-ins with broader file modes.
	require.NoError(t, os.Chmod(dropInFile, 0777))

	var warnings []string
	log.SetHandler(func(_ context.Context, level log.Level, format string, args ...interface{}) {
		if level == log.WarnLevel {
			warnings = append(warnings, fmt.Sprintf(format, args...))
		}
	})
	t.Cleanup(func() { log.SetHandler(nil) })

	runErr := make(chan error, 1)
	go func() {
		runErr <- app.Run()
	}()
	app.WaitReady()
	app.Quit()
	require.NoError(t, <-runErr)

	require.Empty(t, warnings)
	_, err := os.Lstat(legacyMarkerPath)
	require.ErrorIs(t, err, os.ErrNotExist, "owner-only drop-in directories must allow promotion")

	strictApp := daemon.NewForTests(t, &config, issuerURL)
	strictRunErr := make(chan error, 1)
	go func() {
		strictRunErr <- strictApp.Run()
	}()
	strictApp.WaitReady()
	strictApp.Quit()
	require.NoError(t, <-strictRunErr)
	require.Empty(t, warnings, "strict validation must accept drop-ins protected by an owner-only directory")
}

func TestAppRunDoesNotPromoteWithoutSnapData(t *testing.T) {
	t.Setenv("SNAP_DATA", "")

	var notices []string
	log.SetHandler(func(_ context.Context, level log.Level, format string, args ...interface{}) {
		if level == log.NoticeLevel {
			notices = append(notices, fmt.Sprintf(format, args...))
		}
	})
	t.Cleanup(func() { log.SetHandler(nil) })

	tmpDir := t.TempDir()
	config := daemon.DaemonConfig{
		Paths: daemon.SystemPaths{
			BrokerConf: filepath.Join(tmpDir, "broker.conf"),
			DataDir:    filepath.Join(tmpDir, "data"),
		},
	}
	app := daemon.NewForTests(t, &config, issuerURL)

	runErr := make(chan error, 1)
	go func() {
		runErr <- app.Run()
	}()
	app.WaitReady()
	app.Quit()
	require.NoError(t, <-runErr)
	require.Empty(t, notices)

	for _, dir := range []string{tmpDir, config.Paths.DataDir} {
		_, err := os.Lstat(filepath.Join(dir, ".allow-legacy-config-pre-0.5.0"))
		require.ErrorIs(t, err, os.ErrNotExist)
	}
}

func TestLegacyMarkerRemovalFailureDoesNotFailStartup(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("read-only directory permissions do not block root")
	}

	snapData := t.TempDir()
	t.Setenv("SNAP_DATA", snapData)
	markerPath := filepath.Join(snapData, ".allow-legacy-config-pre-0.5.0")
	require.NoError(t, os.WriteFile(markerPath, nil, 0600))
	//nolint:gosec // Exercise failure to remove the marker from a read-only SNAP_DATA directory.
	require.NoError(t, os.Chmod(snapData, 0500))
	t.Cleanup(func() {
		//nolint:gosec // Restore permissions so TempDir cleanup can proceed.
		require.NoError(t, os.Chmod(snapData, 0700))
	})

	tmpDir := t.TempDir()
	config := daemon.DaemonConfig{
		Paths: daemon.SystemPaths{
			BrokerConf: filepath.Join(tmpDir, "broker.conf"),
			DataDir:    filepath.Join(tmpDir, "data"),
		},
	}
	app := daemon.NewForTests(t, &config, issuerURL)

	var warnings []string
	log.SetHandler(func(_ context.Context, level log.Level, format string, args ...interface{}) {
		if level == log.WarnLevel {
			warnings = append(warnings, fmt.Sprintf(format, args...))
		}
	})
	t.Cleanup(func() { log.SetHandler(nil) })

	runErr := make(chan error, 1)
	go func() {
		runErr <- app.Run()
	}()
	app.WaitReady()
	app.Quit()
	require.NoError(t, <-runErr)

	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], "Failed to disable legacy configuration compatibility")
	_, err := os.Lstat(markerPath)
	require.NoError(t, err)
}

func TestAppRunSurfacesSnapDataInspectionErrors(t *testing.T) {
	missingSnapData := filepath.Join(t.TempDir(), "missing")
	t.Setenv("SNAP_DATA", missingSnapData)

	a := daemon.NewForTests(t, nil, issuerURL)
	err := a.Run()
	require.ErrorContains(t, err, "error determining broker configuration validation mode")
	require.ErrorContains(t, err, "could not inspect SNAP_DATA directory")
	require.NotPanics(t, a.Quit)
}

func TestBrokerConfigPermissionsDuringStartup(t *testing.T) {
	tests := map[string]struct {
		strict           bool
		configDirMode    os.FileMode
		mainConfMode     os.FileMode
		dropInDirMode    os.FileMode
		dropInFileMode   os.FileMode
		wantError        string
		wantWarningFor   []string
		wantLegacyMarker bool
	}{
		"legacy_private_drop_in": {
			configDirMode:  0700,
			mainConfMode:   0600,
			dropInDirMode:  0700,
			dropInFileMode: 0777,
		},
		"legacy_public_drop_in": {
			configDirMode:    0700,
			mainConfMode:     0600,
			dropInDirMode:    0755,
			dropInFileMode:   0644,
			wantWarningFor:   []string{"dropInFile"},
			wantLegacyMarker: true,
		},
		"legacy_main_config": {
			configDirMode:    0700,
			mainConfMode:     0644,
			dropInDirMode:    0700,
			dropInFileMode:   0644,
			wantWarningFor:   []string{"mainConfig"},
			wantLegacyMarker: true,
		},
		"legacy_writable_drop_in_directory": {
			configDirMode:    0700,
			mainConfMode:     0600,
			dropInDirMode:    0777,
			dropInFileMode:   0600,
			wantError:        "must not be writable by group or others",
			wantLegacyMarker: true,
		},
		"legacy_writable_drop_in_file": {
			configDirMode:    0700,
			mainConfMode:     0600,
			dropInDirMode:    0755,
			dropInFileMode:   0666,
			wantError:        "must not be writable by group or others",
			wantLegacyMarker: true,
		},
		"strict_private_drop_in": {
			strict:         true,
			configDirMode:  0700,
			mainConfMode:   0600,
			dropInDirMode:  0700,
			dropInFileMode: 0644,
		},
		"legacy_writable_config_parent_and_drop_in_directory": {
			configDirMode:    0777,
			mainConfMode:     0644,
			dropInDirMode:    0777,
			dropInFileMode:   0644,
			wantError:        "must not be writable by group or others",
			wantWarningFor:   []string{"mainConfig"},
			wantLegacyMarker: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			snapData := t.TempDir()
			t.Setenv("SNAP_DATA", snapData)
			if !tc.strict {
				marker := filepath.Join(snapData, ".allow-legacy-config-pre-0.5.0")
				require.NoError(t, os.WriteFile(marker, nil, 0600))
			}

			tmpDir := t.TempDir()
			brokerConf := filepath.Join(tmpDir, "broker.conf")
			config := daemon.DaemonConfig{
				Paths: daemon.SystemPaths{
					BrokerConf: brokerConf,
					DataDir:    filepath.Join(tmpDir, "data"),
				},
			}
			a := daemon.NewForTests(t, &config, issuerURL)
			require.NoError(t, os.Chmod(tmpDir, tc.configDirMode))
			require.NoError(t, os.Chmod(brokerConf, tc.mainConfMode))

			dropInDir := brokerConf + ".d"
			require.NoError(t, os.Mkdir(dropInDir, tc.dropInDirMode))
			require.NoError(t, os.Chmod(dropInDir, tc.dropInDirMode))
			dropInFile := filepath.Join(dropInDir, "extra.conf")
			require.NoError(t, os.WriteFile(dropInFile, []byte("[users]\nallowed_users = OWNER\n"), tc.dropInFileMode))
			require.NoError(t, os.Chmod(dropInFile, tc.dropInFileMode))

			var warnings []string
			log.SetHandler(func(_ context.Context, level log.Level, format string, args ...interface{}) {
				if level == log.WarnLevel {
					warnings = append(warnings, fmt.Sprintf(format, args...))
				}
			})
			t.Cleanup(func() { log.SetHandler(nil) })

			runErr := make(chan error, 1)
			go func() {
				runErr <- a.Run()
			}()
			a.WaitReady()
			if tc.wantError != "" {
				require.ErrorContains(t, <-runErr, tc.wantError)
				a.Quit()
			} else {
				a.Quit()
				require.NoError(t, <-runErr)
			}

			require.Len(t, warnings, len(tc.wantWarningFor))
			warningText := strings.Join(warnings, "\n")
			warningPaths := map[string]string{
				"mainConfig": brokerConf,
				"dropInDir":  dropInDir,
				"dropInFile": dropInFile,
			}
			for _, expected := range tc.wantWarningFor {
				path, ok := warningPaths[expected]
				require.Truef(t, ok, "unknown warning source %q", expected)
				require.Contains(t, warningText, fmt.Sprintf("%q", path))
			}

			markerPath := filepath.Join(snapData, ".allow-legacy-config-pre-0.5.0")
			if tc.wantLegacyMarker {
				_, err := os.Lstat(markerPath)
				require.NoError(t, err, "legacy validation must remain enabled")
			} else {
				_, err := os.Lstat(markerPath)
				require.ErrorIs(t, err, os.ErrNotExist, "clean startup must enable strict validation")
			}

			for path, wantMode := range map[string]os.FileMode{
				tmpDir:     tc.configDirMode,
				brokerConf: tc.mainConfMode,
				dropInDir:  tc.dropInDirMode,
				dropInFile: tc.dropInFileMode,
			} {
				fileInfo, err := os.Stat(path)
				require.NoError(t, err)
				require.Equal(t, wantMode, fileInfo.Mode().Perm(),
					"startup must not change existing permissions")
			}
		})
	}
}

func TestAppCanSigHupWhenExecute(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err, "Setup: pipe shouldn't fail")

	a, wait := startDaemon(t, nil)

	defer wait()
	defer a.Quit()

	orig := os.Stdout
	os.Stdout = w

	a.Hup()

	os.Stdout = orig
	w.Close()

	var out bytes.Buffer
	_, err = io.Copy(&out, r)
	require.NoError(t, err, "Couldn't copy stdout to buffer")
	require.NotEmpty(t, out.String(), "Stacktrace is printed")
}

func TestAppCanSigHupAfterExecute(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err, "Setup: pipe shouldn't fail")

	a, wait := startDaemon(t, nil)
	a.Quit()
	wait()

	orig := os.Stdout
	os.Stdout = w

	a.Hup()

	os.Stdout = orig
	w.Close()

	var out bytes.Buffer
	_, err = io.Copy(&out, r)
	require.NoError(t, err, "Couldn't copy stdout to buffer")
	require.NotEmpty(t, out.String(), "Stacktrace is printed")
}

func TestAppCanSigHupWithoutExecute(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err, "Setup: pipe shouldn't fail")

	a := daemon.NewForTests(t, nil, issuerURL)

	orig := os.Stdout
	os.Stdout = w

	a.Hup()

	os.Stdout = orig
	w.Close()

	var out bytes.Buffer
	_, err = io.Copy(&out, r)
	require.NoError(t, err, "Couldn't copy stdout to buffer")
	require.NotEmpty(t, out.String(), "Stacktrace is printed")
}

func TestAppGetRootCmd(t *testing.T) {
	a := daemon.NewForTests(t, nil, issuerURL)
	require.NotNil(t, a.RootCmd(), "Returns root command")
}

func TestConfigLoad(t *testing.T) {
	tmpDir := t.TempDir()
	config := daemon.DaemonConfig{
		Verbosity: 1,
		Paths: daemon.SystemPaths{
			BrokerConf: filepath.Join(tmpDir, "broker.conf"),
			DataDir:    filepath.Join(tmpDir, "data"),
		},
	}

	a, wait := startDaemon(t, &config)
	defer wait()
	defer a.Quit()

	require.Equal(t, config, a.Config(), "Config is loaded")
}

func TestConfigHasPrecedenceOverPathsConfig(t *testing.T) {
	tmpDir := t.TempDir()
	config := daemon.DaemonConfig{
		Verbosity: 1,
		Paths: daemon.SystemPaths{
			BrokerConf: filepath.Join(tmpDir, "broker.conf"),
			DataDir:    filepath.Join(tmpDir, "data"),
		},
	}

	overrideBrokerConfPath := filepath.Join(tmpDir, "override", "via", "config", "broker.conf")
	daemon.GenerateBrokerConfig(t, overrideBrokerConfPath, issuerURL)
	a := daemon.NewForTests(t, &config, issuerURL, "--config", overrideBrokerConfPath)

	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := a.Run()
		require.NoError(t, err, "Run should exits without any error")
	}()
	a.WaitReady()
	time.Sleep(50 * time.Millisecond)

	defer wg.Wait()
	defer a.Quit()

	want := config
	want.Paths.BrokerConf = overrideBrokerConfPath
	require.Equal(t, want, a.Config(), "Config is loaded")
}

func TestAutoDetectConfig(t *testing.T) {
	tmpDir := t.TempDir()
	config := daemon.DaemonConfig{
		Verbosity: 1,
		Paths: daemon.SystemPaths{
			BrokerConf: filepath.Join(tmpDir, "broker.conf"),
			DataDir:    filepath.Join(tmpDir, "data"),
		},
	}

	configPath := daemon.GenerateTestConfig(t, &config, issuerURL)
	configNextToBinaryPath := filepath.Join(filepath.Dir(os.Args[0]), t.Name()+".yaml")
	err := os.Rename(configPath, configNextToBinaryPath)
	require.NoError(t, err, "Could not relocate authd configuration file in the binary directory")
	// Remove configuration next binary for other tests to not pick it up.
	defer os.Remove(configNextToBinaryPath)

	a := daemon.New(t.Name())
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := a.Run()
		require.NoError(t, err, "Run should exits without any error")
	}()
	a.WaitReady()
	time.Sleep(50 * time.Millisecond)

	defer wg.Wait()
	defer a.Quit()

	require.Equal(t, config, a.Config(), "Did not load configuration next to binary")
}

func TestNoConfigSetDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("SNAP_DATA", tmpDir)

	a := daemon.New(t.Name()) // Use version to still run preExec to load no config but without running server
	a.SetArgs("version")

	err := a.Run()
	require.NoError(t, err, "Run should not return an error")

	require.Equal(t, 0, a.Config().Verbosity, "Default Verbosity")
	require.Equal(t, filepath.Join(tmpDir, "broker.conf"), a.Config().Paths.BrokerConf, "Default broker configuration path")
	require.Equal(t, tmpDir, a.Config().Paths.DataDir, "Default data directory")
}

func TestBadConfigReturnsError(t *testing.T) {
	a := daemon.New(t.Name()) // Use version to still run preExec to load no config but without running server
	a.SetArgs("version", "--paths-config", "/does/not/exist.yaml")

	err := a.Run()
	require.Error(t, err, "Run should return an error on config file")
}

// requireGoroutineStarted starts a goroutine and blocks until it has been launched.
func requireGoroutineStarted(t *testing.T, f func()) {
	t.Helper()

	launched := make(chan struct{})

	go func() {
		close(launched)
		f()
	}()

	<-launched
}

// startDaemon prepares and starts the daemon in the background. The done function should be called
// to wait for the daemon to stop.
func startDaemon(t *testing.T, conf *daemon.DaemonConfig) (app *daemon.App, done func()) {
	t.Helper()

	a := daemon.NewForTests(t, conf, issuerURL)

	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := a.Run()
		require.NoError(t, err, "Run should exits without any error")
	}()
	a.WaitReady()
	time.Sleep(50 * time.Millisecond)

	return a, func() {
		wg.Wait()
	}
}

// captureStdout capture current process stdout and returns a function to get the captured buffer.
func captureStdout(t *testing.T) func() string {
	t.Helper()

	r, w, err := os.Pipe()
	require.NoError(t, err, "Setup: pipe shouldn't fail")

	orig := os.Stdout
	os.Stdout = w

	t.Cleanup(func() {
		os.Stdout = orig
		w.Close()
	})

	var out bytes.Buffer
	errch := make(chan error)
	go func() {
		_, err = io.Copy(&out, r)
		errch <- err
		close(errch)
	}()

	return func() string {
		w.Close()
		w = nil
		require.NoError(t, <-errch, "Couldn't copy stdout to buffer")

		return out.String()
	}
}

func TestMain(m *testing.M) {
	// Start system bus mock.
	cleanup, err := testutils.StartSystemBusMock()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	defer cleanup()

	// Start provider mock
	issuerURL, cleanup = testutils.StartMockProviderServer("", nil)
	defer cleanup()

	m.Run()
}
