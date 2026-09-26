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

func TestSnapInstallHookCreatesMarkerAndRefreshPreservesIt(t *testing.T) {
	snapDir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(snapDir, "conf"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(snapDir, "conf", "broker.conf.orig"), []byte("[oidc]\n"), 0600))
	snapData := t.TempDir()

	binDir := t.TempDir()
	installArgsPath := filepath.Join(t.TempDir(), "install-args")
	installShim := `#!/bin/sh
if [ "$#" -ne 8 ] ||
   [ "$1" != "-o" ] || [ "$2" != "root" ] ||
   [ "$3" != "-g" ] || [ "$4" != "root" ] ||
   [ "$5" != "-m" ] || [ "$6" != "0600" ] ||
   [ "$7" != "/dev/null" ]; then
  echo "install hook used unexpected marker creation options" >&2
  exit 1
fi
printf '%s\n' "$*" > "$HOOK_INSTALL_ARGS"
if [ "$HOOK_RUN_AS_ROOT" = "1" ]; then
  exec /usr/bin/install "$@"
fi
: > "$8"
chmod 0600 "$8"
`
	writeHookTestCommand(t, binDir, "install", installShim)

	path := binDir + string(os.PathListSeparator) + os.Getenv("PATH")
	runSnapHook(t, "install", snapDir, snapData, path, map[string]string{
		"HOOK_INSTALL_ARGS": installArgsPath,
		"HOOK_RUN_AS_ROOT":  map[bool]string{true: "1", false: "0"}[os.Geteuid() == 0],
	})

	markerPath := filepath.Join(snapData, "strict-config-validation")
	markerInfo, err := os.Lstat(markerPath)
	require.NoError(t, err)
	require.True(t, markerInfo.Mode().IsRegular())
	require.Equal(t, os.FileMode(0600), markerInfo.Mode().Perm())
	if os.Geteuid() == 0 {
		stat, ok := markerInfo.Sys().(*syscall.Stat_t)
		require.True(t, ok)
		require.Zero(t, stat.Uid)
		require.Zero(t, stat.Gid)
	}
	args, err := os.ReadFile(installArgsPath)
	require.NoError(t, err)
	require.Equal(t, []string{"-o", "root", "-g", "root", "-m", "0600", "/dev/null", markerPath},
		strings.Fields(string(args)))

	binDir = t.TempDir()
	writeHookTestCommand(t, binDir, "snapctl", "#!/bin/sh\nprintf '1.0.0\\n'\n")
	writeHookTestCommand(t, binDir, "semver", `#!/bin/sh
case "$1" in
  compare) printf 'greater\n' ;;
  check) printf 'valid\n' ;;
  *) exit 1 ;;
esac
`)
	writeHookTestCommand(t, binDir, "logger", "#!/bin/sh\nexit 0\n")
	beforeRefresh, err := os.Stat(markerPath)
	require.NoError(t, err)

	path = binDir + string(os.PathListSeparator) + os.Getenv("PATH")
	runSnapHook(t, "post-refresh", snapDir, snapData, path, nil)

	afterRefresh, err := os.Stat(markerPath)
	require.NoError(t, err)
	require.True(t, os.SameFile(beforeRefresh, afterRefresh))
	require.Equal(t, os.FileMode(0600), afterRefresh.Mode().Perm())
}

func TestSnapRefreshHookDoesNotCreateStrictMarker(t *testing.T) {
	snapDir := t.TempDir()
	snapData := t.TempDir()
	binDir := t.TempDir()
	writeHookTestCommand(t, binDir, "snapctl", "#!/bin/sh\nprintf '1.0.0\\n'\n")
	writeHookTestCommand(t, binDir, "semver", `#!/bin/sh
case "$1" in
  compare) printf 'greater\n' ;;
  check) printf 'valid\n' ;;
  *) exit 1 ;;
esac
`)
	writeHookTestCommand(t, binDir, "logger", "#!/bin/sh\nexit 0\n")

	path := binDir + string(os.PathListSeparator) + os.Getenv("PATH")
	runSnapHook(t, "post-refresh", snapDir, snapData, path, nil)

	_, err := os.Lstat(filepath.Join(snapData, "strict-config-validation"))
	require.ErrorIs(t, err, os.ErrNotExist)
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
	output, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "hook %s failed: %s", name, output)
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
