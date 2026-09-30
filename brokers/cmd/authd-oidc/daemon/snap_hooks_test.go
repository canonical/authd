package daemon_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSnapInstallHookLeavesConfigurationStrictByDefault(t *testing.T) {
	snapDir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(snapDir, "conf"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(snapDir, "conf", "broker.conf.orig"), []byte("[oidc]\n"), 0600))
	snapData := t.TempDir()

	runSnapHook(t, "install", snapDir, snapData, writeRefreshHookShims(t), nil)

	requireMarkerAbsent(t, filepath.Join(snapData, ".allow-legacy-config-pre-0.5.0"))
}

func TestSnapRefreshHookMigratesOlderInstall(t *testing.T) {
	snapData := t.TempDir()
	runSnapHook(t, "post-refresh", t.TempDir(), snapData, writeRefreshHookShims(t), map[string]string{
		"MOCK_PREVIOUS_VERSION_SET":      "yes",
		"MOCK_PREVIOUS_VERSION":          "0.4.9",
		"MOCK_LEGACY_CONFIG_COMPARISON":  "less",
		"MOCK_WORLD_READABLE_COMPARISON": "greater",
	})

	requireLegacyMarker(t, filepath.Join(snapData, ".allow-legacy-config-pre-0.5.0"))
}

func TestSnapRefreshHookMigratesVersionsBeforeMigrationBoundary(t *testing.T) {
	tests := map[string]string{
		"at_validation_rules_release":    "0.5.0",
		"after_validation_rules_release": "0.5.1",
	}

	for name, previousVersion := range tests {
		t.Run(name, func(t *testing.T) {
			snapData := t.TempDir()
			runSnapHook(t, "post-refresh", t.TempDir(), snapData, writeRefreshHookShims(t), map[string]string{
				"MOCK_PREVIOUS_VERSION_SET":      "yes",
				"MOCK_PREVIOUS_VERSION":          previousVersion,
				"MOCK_LEGACY_CONFIG_COMPARISON":  "less",
				"MOCK_WORLD_READABLE_COMPARISON": "greater",
			})

			requireLegacyMarker(t, filepath.Join(snapData, ".allow-legacy-config-pre-0.5.0"))
		})
	}
}

func TestSnapRefreshHookDoesNotMigrateVersionsAtOrAfterMigrationBoundary(t *testing.T) {
	tests := map[string]struct {
		previousVersion  string
		legacyComparison string
	}{
		"at_migration_boundary": {
			previousVersion:  "0.5.2-pre1",
			legacyComparison: "equal",
		},
		"after_migration_boundary": {
			previousVersion:  "0.5.2",
			legacyComparison: "greater",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			snapData := t.TempDir()
			runSnapHook(t, "post-refresh", t.TempDir(), snapData, writeRefreshHookShims(t), map[string]string{
				"MOCK_PREVIOUS_VERSION_SET":      "yes",
				"MOCK_PREVIOUS_VERSION":          tc.previousVersion,
				"MOCK_LEGACY_CONFIG_COMPARISON":  tc.legacyComparison,
				"MOCK_WORLD_READABLE_COMPARISON": "greater",
			})

			requireMarkerAbsent(t, filepath.Join(snapData, ".allow-legacy-config-pre-0.5.0"))
		})
	}
}

func TestSnapRefreshHookRunsBothMigrations(t *testing.T) {
	snapData := t.TempDir()
	configDir := filepath.Join(snapData, "broker.conf.d")
	require.NoError(t, os.Mkdir(configDir, 0700))
	configPath := filepath.Join(configDir, "provider.conf")
	require.NoError(t, os.WriteFile(configPath, []byte("[oidc]\n"), 0600))
	//nolint:gosec // The test creates an insecure mode to verify that migration tightens it.
	require.NoError(t, os.Chmod(configPath, 0644))

	runSnapHook(t, "post-refresh", t.TempDir(), snapData, writeRefreshHookShims(t), map[string]string{
		"MOCK_PREVIOUS_VERSION_SET":      "yes",
		"MOCK_PREVIOUS_VERSION":          "0.4.9",
		"MOCK_WORLD_READABLE_COMPARISON": "less",
		"MOCK_LEGACY_CONFIG_COMPARISON":  "less",
	})

	configDirInfo, err := os.Stat(configDir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0755), configDirInfo.Mode().Perm())
	configInfo, err := os.Stat(configPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), configInfo.Mode().Perm())
	requireLegacyMarker(t, filepath.Join(snapData, ".allow-legacy-config-pre-0.5.0"))
}

func TestSnapRefreshHookPreservesExistingLegacyMarker(t *testing.T) {
	snapData := t.TempDir()
	markerPath := filepath.Join(snapData, ".allow-legacy-config-pre-0.5.0")
	require.NoError(t, os.WriteFile(markerPath, []byte("existing"), 0600))
	runSnapHook(t, "post-refresh", t.TempDir(), snapData, writeRefreshHookShims(t), map[string]string{
		"MOCK_PREVIOUS_VERSION_SET":      "yes",
		"MOCK_PREVIOUS_VERSION":          "0.4.9",
		"MOCK_LEGACY_CONFIG_COMPARISON":  "less",
		"MOCK_WORLD_READABLE_COMPARISON": "greater",
	})

	content, err := os.ReadFile(markerPath)
	require.NoError(t, err)
	require.Equal(t, "existing", string(content))
}

func TestSnapRefreshHookPreservesLegacyMarkerOnLaterRefresh(t *testing.T) {
	snapData := t.TempDir()
	path := writeRefreshHookShims(t)
	markerPath := filepath.Join(snapData, ".allow-legacy-config-pre-0.5.0")
	runSnapHook(t, "post-refresh", t.TempDir(), snapData, path, map[string]string{
		"MOCK_PREVIOUS_VERSION_SET":      "yes",
		"MOCK_PREVIOUS_VERSION":          "0.5.1",
		"MOCK_LEGACY_CONFIG_COMPARISON":  "less",
		"MOCK_WORLD_READABLE_COMPARISON": "greater",
	})
	requireLegacyMarker(t, markerPath)

	require.NoError(t, os.WriteFile(markerPath, []byte("existing"), 0600))

	runSnapHook(t, "post-refresh", t.TempDir(), snapData, path, map[string]string{
		"MOCK_PREVIOUS_VERSION_SET":      "yes",
		"MOCK_PREVIOUS_VERSION":          "0.5.2",
		"MOCK_LEGACY_CONFIG_COMPARISON":  "greater",
		"MOCK_WORLD_READABLE_COMPARISON": "greater",
	})

	content, err := os.ReadFile(markerPath)
	require.NoError(t, err)
	require.Equal(t, "existing", string(content))
}

func TestSnapRefreshHookHandlesUnknownPreviousVersion(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		snapData := t.TempDir()
		configDir := filepath.Join(snapData, "broker.conf.d")
		require.NoError(t, os.Mkdir(configDir, 0700))
		configPath := filepath.Join(configDir, "provider.conf")
		require.NoError(t, os.WriteFile(configPath, []byte("[oidc]\n"), 0600))
		//nolint:gosec // The test creates an insecure mode to verify that migration tightens it.
		require.NoError(t, os.Chmod(configPath, 0644))

		output, err := runSnapHookResult(t, "post-refresh", t.TempDir(), snapData, writeRefreshHookShims(t), map[string]string{
			"MOCK_PREVIOUS_VERSION_SET": "yes",
			"MOCK_PREVIOUS_VERSION":     "",
		})
		require.NoErrorf(t, err, "hook post-refresh failed: %s", output)
		require.Contains(t, string(output), "previous-version: <not set>; treating as older than migration boundaries")

		configDirInfo, err := os.Stat(configDir)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0755), configDirInfo.Mode().Perm())
		configInfo, err := os.Stat(configPath)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0600), configInfo.Mode().Perm())
		requireLegacyMarker(t, filepath.Join(snapData, ".allow-legacy-config-pre-0.5.0"))
	})

	t.Run("invalid", func(t *testing.T) {
		snapData := t.TempDir()
		output, err := runSnapHookResult(t, "post-refresh", t.TempDir(), snapData, writeRefreshHookShims(t), map[string]string{
			"MOCK_PREVIOUS_VERSION_SET": "yes",
			"MOCK_PREVIOUS_VERSION":     "not-a-semver",
		})
		require.Error(t, err)
		require.Contains(t, string(output), "cannot determine required migrations")
		requireMarkerAbsent(t, filepath.Join(snapData, ".allow-legacy-config-pre-0.5.0"))
	})
}

func TestSnapRefreshHookFailsIfPreviousVersionCannotBeRead(t *testing.T) {
	snapData := t.TempDir()
	output, err := runSnapHookResult(t, "post-refresh", t.TempDir(), snapData, writeRefreshHookShims(t), map[string]string{
		"MOCK_PREVIOUS_VERSION_GET_FAIL": "yes",
	})

	require.Error(t, err)
	require.Contains(t, string(output), "Could not read previous-version from snap configuration")
	requireMarkerAbsent(t, filepath.Join(snapData, ".allow-legacy-config-pre-0.5.0"))
}

func TestSnapRefreshHookDoesNotReenableCompatibilityAfterPromotion(t *testing.T) {
	for name, previousVersion := range map[string]string{
		"at_migration_boundary":    "0.5.2-pre1",
		"after_migration_boundary": "0.5.2",
	} {
		t.Run(name, func(t *testing.T) {
			snapData := t.TempDir()
			path := writeRefreshHookShims(t)
			runSnapHook(t, "post-refresh", t.TempDir(), snapData, path, map[string]string{
				"MOCK_PREVIOUS_VERSION_SET":      "yes",
				"MOCK_PREVIOUS_VERSION":          "0.5.1",
				"MOCK_LEGACY_CONFIG_COMPARISON":  "less",
				"MOCK_WORLD_READABLE_COMPARISON": "greater",
			})

			markerPath := filepath.Join(snapData, ".allow-legacy-config-pre-0.5.0")
			requireLegacyMarker(t, markerPath)
			require.NoError(t, os.Remove(markerPath))

			runSnapHook(t, "post-refresh", t.TempDir(), snapData, path, map[string]string{
				"MOCK_PREVIOUS_VERSION_SET": "yes",
				"MOCK_PREVIOUS_VERSION":     previousVersion,
			})

			requireMarkerAbsent(t, markerPath)
		})
	}
}

func TestSnapInstallHookKeepsLaterRefreshStrict(t *testing.T) {
	for name, previousVersion := range map[string]string{
		"at_migration_boundary":    "0.5.2-pre1",
		"after_migration_boundary": "0.5.2",
	} {
		t.Run(name, func(t *testing.T) {
			snapDir := t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(snapDir, "conf"), 0700))
			require.NoError(t, os.WriteFile(filepath.Join(snapDir, "conf", "broker.conf.orig"), []byte("[oidc]\n"), 0600))
			snapData := t.TempDir()
			path := writeRefreshHookShims(t)
			runSnapHook(t, "install", snapDir, snapData, path, nil)
			runSnapHook(t, "post-refresh", t.TempDir(), snapData, path, map[string]string{
				"MOCK_PREVIOUS_VERSION_SET": "yes",
				"MOCK_PREVIOUS_VERSION":     previousVersion,
			})

			requireMarkerAbsent(t, filepath.Join(snapData, ".allow-legacy-config-pre-0.5.0"))
		})
	}
}

func requireLegacyMarker(t *testing.T, path string) {
	requireRootOwnedMarker(t, path)
}

func requireRootOwnedMarker(t *testing.T, path string) {
	t.Helper()

	markerInfo, err := os.Lstat(path)
	require.NoError(t, err)
	require.True(t, markerInfo.Mode().IsRegular())
	require.Equal(t, os.FileMode(0600), markerInfo.Mode().Perm())
	if os.Geteuid() == 0 {
		stat, ok := markerInfo.Sys().(*syscall.Stat_t)
		require.True(t, ok)
		require.Zero(t, stat.Uid)
		require.Zero(t, stat.Gid)
	}
}

func requireMarkerAbsent(t *testing.T, path string) {
	t.Helper()

	_, err := os.Lstat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func writeRefreshHookShims(t *testing.T) string {
	t.Helper()

	binDir := t.TempDir()
	writeHookTestCommand(t, binDir, "snapctl", `#!/bin/sh
case "$1:$2" in
  get:previous-version)
    if [ "${MOCK_PREVIOUS_VERSION_GET_FAIL:-no}" = "yes" ]; then
      exit 1
    fi
    if [ "${MOCK_PREVIOUS_VERSION_SET:-no}" = "yes" ]; then
      printf '%s\n' "${MOCK_PREVIOUS_VERSION:-}"
    else
      printf '1.0.0\n'
    fi
    ;;
  *) exit 1 ;;
esac
`)
	writeHookTestCommand(t, binDir, "semver", `#!/bin/sh
case "$1" in
  compare)
    case "$3" in
      0.5.0-pre1) printf '%s\n' "${MOCK_WORLD_READABLE_COMPARISON:-greater}" ;;
      0.5.2-pre1) printf '%s\n' "${MOCK_LEGACY_CONFIG_COMPARISON:-greater}" ;;
      *) exit 1 ;;
    esac
    ;;
  check)
    case "$2" in
      ''|not-a-semver) printf 'invalid\n'; exit 1 ;;
      *) printf 'valid\n' ;;
    esac
    ;;
  *) exit 1 ;;
esac
`)
	writeHookTestCommand(t, binDir, "logger", "#!/bin/sh\nprintf '%s\\n' \"$*\" >&2\n")
	writeHookTestCommand(t, binDir, "install", `#!/bin/bash
owner=
group=
args=()
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o|--owner)
      if [ "$#" -lt 2 ]; then
        echo "post-refresh hook omitted the owner value" >&2
        exit 1
      fi
      owner=$2
      if [ "$EUID" -eq 0 ]; then
        args+=("$1" "$2")
      else
        args+=("$1" "$(id -u)")
      fi
      shift 2
      ;;
    --owner=*)
      owner=${1#*=}
      if [ "$EUID" -eq 0 ]; then
        args+=("$1")
      else
        args+=("--owner=$(id -u)")
      fi
      shift
      ;;
    -g|--group)
      if [ "$#" -lt 2 ]; then
        echo "post-refresh hook omitted the group value" >&2
        exit 1
      fi
      group=$2
      if [ "$EUID" -eq 0 ]; then
        args+=("$1" "$2")
      else
        args+=("$1" "$(id -g)")
      fi
      shift 2
      ;;
    --group=*)
      group=${1#*=}
      if [ "$EUID" -eq 0 ]; then
        args+=("$1")
      else
        args+=("--group=$(id -g)")
      fi
      shift
      ;;
    *)
      args+=("$1")
      shift
      ;;
  esac
done

if [ "$owner" != "root" ] || [ "$group" != "root" ]; then
  echo "post-refresh hook must create the marker as root" >&2
  exit 1
fi

# Translate root ownership to the current user when tests run unprivileged.
exec /usr/bin/install "${args[@]}"
`)

	return binDir + string(os.PathListSeparator) + os.Getenv("PATH")
}

func writeHookTestCommand(t *testing.T, dir, name, content string) {
	t.Helper()

	path := filepath.Join(dir, name)
	err := os.WriteFile(path, []byte(content), 0600)
	require.NoError(t, err)
	//nolint:gosec // The test writes executable shell shims to run the snap hooks.
	require.NoError(t, os.Chmod(path, 0700))
}

func runSnapHook(t *testing.T, name, snapDir, snapData, path string, additionalEnv map[string]string) {
	t.Helper()

	output, err := runSnapHookResult(t, name, snapDir, snapData, path, additionalEnv)
	require.NoErrorf(t, err, "hook %s failed: %s", name, output)
}

func runSnapHookResult(t *testing.T, name, snapDir, snapData, path string, additionalEnv map[string]string) ([]byte, error) {
	t.Helper()

	_, source, _, ok := runtime.Caller(0)
	require.True(t, ok)
	hookPath := filepath.Join(filepath.Dir(source), "..", "..", "..", "..", "snap", "hooks", name)
	//nolint:gosec // The test runs a fixed hook script from this repository.
	cmd := exec.Command("/bin/sh", hookPath)
	cmd.Env = hookTestEnv(map[string]string{
		"PATH":      path,
		"SNAP":      snapDir,
		"SNAP_DATA": snapData,
		"SNAP_NAME": "authd-google",
	}, additionalEnv)
	return cmd.CombinedOutput()
}

func hookTestEnv(env, additionalEnv map[string]string) []string {
	for key, value := range additionalEnv {
		env[key] = value
	}

	result := make([]string, 0, len(os.Environ())+len(env))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := env[key]; replaced {
			continue
		}
		result = append(result, entry)
	}
	for key, value := range env {
		result = append(result, key+"="+value)
	}
	return result
}
