package testutils

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/canonical/authd/internal/testlog"
	"github.com/canonical/authd/internal/testutils/golden"
	"github.com/stretchr/testify/require"
	"gorbe.io/go/osrelease"
)

const (
	// lxdTestEnvVar is set inside LXD containers to prevent recursive re-entry.
	lxdTestEnvVar = "AUTHD_LXD_TEST"

	// lxdProject is the dedicated LXD project used for test containers.
	lxdProject = "authd-tests"

	// UseLXDEnvVar enables running version-specific tests in LXD containers.
	// When set, tests targeting a different Ubuntu version than the host will
	// be executed inside an LXD container with the correct Ubuntu version.
	UseLXDEnvVar = "AUTHD_TESTS_USE_LXD"

	// provisionedMarker is created inside the container after provisioning to
	// avoid re-running the full provisioning on subsequent test runs.
	provisionedMarker = "/var/lib/authd-test/provisioned"

	// LXDUbuntuUserID is Ubuntu's default non-root user/group inside cloud images.
	LXDUbuntuUserID = 1000
)

var (
	lxdContainersMu  sync.Mutex
	lxdContainerOnce = map[string]*sync.Once{} // ubuntuVersion → once guard for get-or-create

	hostUbuntuVersionOnce sync.Once
	hostUbuntuVersion     string // e.g. "24.04"

	canUseLXDOnce sync.Once
	canUseLXD     bool

	lxdProvisionHooksMu sync.Mutex
	lxdProvisionHooks   []func(t *testing.T, containerName string)
)

// RegisterLXDProvisionHook registers a function to run during container
// provisioning, after system packages are installed but before the
// provisioning marker is written. Use this to pre-warm build caches or
// perform any other one-time test-specific setup.
//
// Hooks registered after provisioning has already run for a given container
// will not execute for that container.
func RegisterLXDProvisionHook(fn func(t *testing.T, containerName string)) {
	lxdProvisionHooksMu.Lock()
	defer lxdProvisionHooksMu.Unlock()
	lxdProvisionHooks = append(lxdProvisionHooks, fn)
}

// RunningInLXD returns true if the test is being run inside an LXD container
// spawned by RunTestInLXD.
func RunningInLXD() bool {
	return os.Getenv(lxdTestEnvVar) == "1"
}

// RunTestInLXD re-runs the current test inside an LXD container with the
// specified Ubuntu version if the host version doesn't match.
//
// The caller MUST return immediately when this function returns true:
//
//	if testutils.RunTestInLXD(t, "24.04") {
//	   return
//	}
//
// Behavior:
//   - If already inside LXD (AUTHD_LXD_TEST=1): returns false (caller proceeds normally)
//   - If host Ubuntu version matches: returns false (caller proceeds normally)
//   - If TESTS_UPDATE_GOLDEN or AUTHD_TESTS_USE_LXD is set: runs test in LXD, returns true
//   - Otherwise: skips the test, returns true
func RunTestInLXD(t *testing.T, ubuntuVersion string) bool {
	t.Helper()

	// Already inside an LXD container — run the test normally.
	if RunningInLXD() {
		return false
	}

	// Host version matches — run the test normally.
	if getHostUbuntuVersion(t) == ubuntuVersion {
		return false
	}

	// Neither golden update nor LXD mode enabled — skip.
	if !golden.UpdateEnabled() && os.Getenv(UseLXDEnvVar) == "" {
		t.Skipf("Skipping: requires Ubuntu %s (set %s=1 or %s=1 to run in LXD)",
			ubuntuVersion, golden.UpdateGoldenFilesEnv, UseLXDEnvVar)
		return true
	}

	requireLXD(t)
	containerName := getOrCreateLXDContainer(t, ubuntuVersion)

	cwd, err := os.Getwd()
	require.NoError(t, err, "Setup: could not get working directory")

	env := []string{
		lxdTestEnvVar + "=1",
		"PATH=/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/snap/bin",
		"HOME=/home/ubuntu",
	}
	if golden.UpdateEnabled() {
		env = append(env, golden.UpdateGoldenFilesEnv+"=1")
	}
	env = AppendCovEnv(env)
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "AUTHD_TESTS_") {
			env = append(env, e)
		}
	}

	goTestArgs := []string{
		"go", "test",
		"-count=1",
		"-run", "^" + regexp.QuoteMeta(t.Name()) + "$",
	}
	if testing.Verbose() {
		goTestArgs = append(goTestArgs, "-v")
	}
	if coverDir := CoverDirForTests(); coverDir != "" {
		goTestArgs = append(goTestArgs, "-cover", fmt.Sprintf("-test.gocoverdir=%s", coverDir))
	}
	goTestArgs = append(goTestArgs, ".")

	ubuntuID := fmt.Sprintf("%d", LXDUbuntuUserID)
	args := []string{"exec", containerName, "--cwd", cwd, "--user", ubuntuID, "--group", ubuntuID, "--", "env"}
	args = append(args, env...)
	args = append(args, goTestArgs...)

	// #nosec:G204 - we control the command arguments in tests
	cmd := lxcCommand(args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	testlog.LogCommand(t, fmt.Sprintf("Running %s in LXD (Ubuntu %s)", t.Name(), ubuntuVersion), cmd)
	err = cmd.Run()
	if err != nil {
		testlog.LogEndSeparator(t, fmt.Sprintf("%s in LXD (Ubuntu %s) failed", t.Name(), ubuntuVersion))
		t.Fatalf("Running %s in LXD (Ubuntu %s) failed: %v", t.Name(), ubuntuVersion, err)
	}
	testlog.LogEndSeparatorf(t, "%s in LXD finished", t.Name())

	return true
}

// CleanupLXDContainers stops all LXD containers that were used during this
// test run. Containers are kept (not deleted) so they can be reused on the
// next run; their provisioned build cache persists across restarts.
// Call this from TestMain after m.Run().
func CleanupLXDContainers() {
	lxdContainersMu.Lock()
	defer lxdContainersMu.Unlock()

	for version := range lxdContainerOnce {
		name := containerNameForVersion(version)
		//#nosec:G204 - we control the command arguments
		cmd := lxcCommand("stop", name)
		if out, err := cmd.CombinedOutput(); err != nil {
			if bytes.Contains(out, []byte("Instance not found")) {
				continue
			}
			fmt.Fprintf(os.Stderr, "Warning: failed to stop LXD container %s (version %s): %v\n%s\n",
				name, version, err, out)
		}
	}
}

// getHostUbuntuVersion returns the host's Ubuntu version (e.g. "24.04").
func getHostUbuntuVersion(t *testing.T) string {
	t.Helper()

	hostUbuntuVersionOnce.Do(func() {
		err := osrelease.Parse()
		if err != nil {
			return
		}
		switch osrelease.Release.ID {
		case "ubuntu":
			hostUbuntuVersion = osrelease.Release.VersionID
		case "ubuntu-core":
			hostUbuntuVersion = osrelease.Release.VersionID + ".04"
		}
	})

	return hostUbuntuVersion
}

// requireLXD checks that LXD is available and skips/fails appropriately.
func requireLXD(t *testing.T) {
	t.Helper()

	canUseLXDOnce.Do(func() {
		if _, err := exec.LookPath("lxc"); err != nil {
			return
		}
		// Check that the LXD daemon is reachable.
		// Probe via the default project first so we can bootstrap our dedicated
		// test project when it does not exist yet (e.g. on fresh CI runners).
		cmd := exec.Command("lxc", "list", "--format=csv", "--columns=n")
		if err := cmd.Run(); err != nil {
			return
		}
		if err := ensureLXDProject(); err != nil {
			return
		}
		canUseLXD = true
	})

	require.True(t, canUseLXD, "LXD is not available (install LXD and ensure the daemon is running)")
}

// ensureLXDProject ensures the dedicated test project exists and reuses
// default-project profiles (features.profiles=false).
func ensureLXDProject() error {
	// #nosec:G204 - we control the command arguments in tests
	show := exec.Command("lxc", "project", "show", lxdProject)
	if err := show.Run(); err == nil {
		return nil
	}

	// #nosec:G204 - we control the command arguments in tests
	create := exec.Command("lxc", "project", "create", lxdProject, "-c", "features.profiles=false")
	out, err := create.CombinedOutput()
	if err != nil && !bytes.Contains(out, []byte("already exists")) {
		return fmt.Errorf("creating LXD project %q failed: %w: %s", lxdProject, err, strings.TrimSpace(string(out)))
	}

	return nil
}

// containerNameForVersion returns the deterministic container name for a given
// Ubuntu version. The name is stable across test runs so containers can be reused.
func containerNameForVersion(ubuntuVersion string) string {
	slug := strings.ReplaceAll(ubuntuVersion, ".", "")
	return "authd-test-" + slug
}

// getOrCreateLXDContainer returns the name of a persistent LXD container for
// the given Ubuntu version. The container is reused across test runs.
func getOrCreateLXDContainer(t *testing.T, ubuntuVersion string) string {
	t.Helper()

	lxdContainersMu.Lock()
	if _, ok := lxdContainerOnce[ubuntuVersion]; !ok {
		lxdContainerOnce[ubuntuVersion] = &sync.Once{}
	}
	once := lxdContainerOnce[ubuntuVersion]
	lxdContainersMu.Unlock()

	once.Do(func() {
		ensureLXDContainer(t, ubuntuVersion)
	})

	return containerNameForVersion(ubuntuVersion)
}

// ensureLXDContainer ensures an LXD container for the given Ubuntu version
// exists and is running, creating and provisioning it if necessary.
func ensureLXDContainer(t *testing.T, ubuntuVersion string) {
	t.Helper()

	containerName := containerNameForVersion(ubuntuVersion)

	switch lxcContainerState(containerName) {
	case "RUNNING":
		t.Logf("Reusing running LXD container %s (Ubuntu %s)", containerName, ubuntuVersion)
	case "STOPPED":
		t.Logf("Starting stopped LXD container %s (Ubuntu %s)", containerName, ubuntuVersion)
		lxcRun(t, "start", containerName)
		lxcRun(t, "exec", containerName, "--", "cloud-init", "status", "--wait")
	default:
		// Container doesn't exist — create and provision it.
		t.Logf("Creating LXD container %s (Ubuntu %s)...", containerName, ubuntuVersion)
		createLXDContainer(t, containerName, ubuntuVersion)
	}

	// Ensure transient state is present (lost on container restart).
	ensureContainerReady(t, containerName)
}

// lxcContainerState returns the state of an LXD container ("RUNNING", "STOPPED",
// or "" if it does not exist).
func lxcContainerState(containerName string) string {
	// #nosec:G204 - we control the command arguments
	cmd := lxcCommand("list",
		"--format=csv", "--columns=ns",
		"^"+regexp.QuoteMeta(containerName)+"$")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	// Output format: "<name>,<STATE>\n"
	line := strings.TrimSpace(string(out))
	if line == "" {
		return ""
	}
	parts := strings.SplitN(line, ",", 2)
	if len(parts) < 2 {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

// createLXDContainer creates and provisions a new LXD container.
func createLXDContainer(t *testing.T, containerName, ubuntuVersion string) {
	t.Helper()

	// Map the host UID/GID to Ubuntu's default user (UID/GID 1000) inside the
	// container so tests can run unprivileged while preserving host ownership on
	// bind-mounted source files (including golden updates).
	uid := os.Getuid()
	gid := os.Getgid()
	rawIDMap := fmt.Sprintf("uid %d %d\ngid %d %d", uid, LXDUbuntuUserID, gid, LXDUbuntuUserID)

	lxcRun(t, "launch",
		"--config", "raw.idmap="+rawIDMap,
		"--config", "boot.autostart=false",
		"ubuntu:"+ubuntuVersion, containerName)

	// Wait for the container to be ready (cloud-init / networking).
	lxcRun(t, "exec", containerName, "--", "cloud-init", "status", "--wait")

	// Bind-mount the project source tree at the same path inside the container.
	projectRoot := ProjectRoot()
	lxcRun(t, "config", "device", "add", containerName, "project",
		"disk", "source="+projectRoot, "path="+projectRoot)

	// Install dependencies.
	provisionLXDContainer(t, containerName)

	t.Logf("LXD container %s ready", containerName)
}

// provisionLXDContainer installs all required build/test dependencies.
func provisionLXDContainer(t *testing.T, containerName string) {
	t.Helper()

	// Check if already provisioned.
	// #nosec:G204 - we control the command arguments
	cmd := lxcCommand("exec", containerName, "--",
		"test", "-f", provisionedMarker)
	if cmd.Run() == nil {
		t.Logf("Container %s already provisioned, skipping", containerName)
		return
	}

	start := time.Now()
	testlog.LogStartSeparatorf(t, "Provisioning LXD container %s", containerName)

	lxcExec(t, containerName, "apt-get", "update", "-y")

	// Enable the Go backports PPA so Build-Depends can resolve golang-go >= 1.25.
	lxcExec(t, containerName, "apt-get", "install", "-y", "--no-install-recommends",
		"software-properties-common")
	lxcExec(t, containerName, "add-apt-repository", "-y",
		"ppa:ubuntu-enterprise-desktop/golang")
	lxcExec(t, containerName, "apt-get", "update", "-y")

	// Install package build-dependencies from debian/control.
	LXCExecFromDir(t, containerName, ProjectRoot(),
		"apt-get", "build-dep", "-y", ".")

	// Align toolchain resolution with Debian packaging logic.
	toolchainPathScript := `if ! command -v go >/dev/null 2>&1; then
		go_dep_bin="$(./debian/get-depends-go-bin-path.sh || true)"
		if [ -n "$go_dep_bin" ] && [ -x "$go_dep_bin/go" ]; then
			ln -sf "$go_dep_bin/go" /usr/local/bin/go
		fi
	fi

	cargo_dep_paths="$(./debian/get-depends-cargo-bin-paths.sh)"
	cargo_dep_bin="$(PATH="$cargo_dep_paths:$PATH" command -v cargo || true)"
	rustc_dep_bin="$(PATH="$cargo_dep_paths:$PATH" command -v rustc || true)"

	if [ -n "$cargo_dep_bin" ]; then
		ln -sf "$cargo_dep_bin" /usr/local/bin/cargo
	fi
	if [ -n "$rustc_dep_bin" ]; then
		ln -sf "$rustc_dep_bin" /usr/local/bin/rustc
	fi`
	LXCExecFromDir(t, containerName, ProjectRoot(), "sh", "-ec", toolchainPathScript)

	// Run any registered provisioning hooks (e.g. build-cache warmup).
	lxdProvisionHooksMu.Lock()
	hooks := lxdProvisionHooks
	lxdProvisionHooksMu.Unlock()
	for _, hook := range hooks {
		hook(t, containerName)
	}

	// Write the provisioning marker so we skip this on the next run.
	markerDir := filepath.Dir(provisionedMarker)
	lxcExec(t, containerName, "mkdir", "-p", markerDir)
	lxcExec(t, containerName, "touch", provisionedMarker)

	testlog.LogEndSeparatorf(t, "Provisioned LXD container %s in %s",
		containerName, time.Since(start).Round(time.Millisecond))
}

// ensureContainerReady recreates transient state that is lost when the
// container is restarted (e.g. tmpfs directories).
func ensureContainerReady(t *testing.T, containerName string) {
	t.Helper()

	// /run/sshd is required by OpenSSH's privilege separation on Ubuntu 24.04.
	lxcExec(t, containerName, "mkdir", "-p", "/run/sshd")

	// Ensure the project bind-mount is present (it should persist, but check
	// in case the container was recreated without it).
	ensureBindMount(t, containerName)
}

// ensureBindMount adds the project source tree bind-mount to the container if
// it is not already present.
func ensureBindMount(t *testing.T, containerName string) {
	t.Helper()

	// #nosec:G204 - we control the command arguments
	cmd := lxcCommand("config", "device", "show", containerName)
	out, err := cmd.Output()
	if err != nil {
		// If we can't read devices, try to add (it will fail if already present,
		// which is harmless).
		projectRoot := ProjectRoot()
		lxcRun(t, "config", "device", "add", containerName, "project",
			"disk", "source="+projectRoot, "path="+projectRoot)
		return
	}

	// The device config is YAML; just check if "project:" appears in it.
	if !bytes.Contains(out, []byte("project:")) {
		projectRoot := ProjectRoot()
		lxcRun(t, "config", "device", "add", containerName, "project",
			"disk", "source="+projectRoot, "path="+projectRoot)
	}
}

// lxcRun runs an lxc command and fails the test on error.
func lxcRun(t *testing.T, args ...string) {
	t.Helper()

	// #nosec:G204 - we control the command arguments in tests
	cmd := lxcCommand(args...)
	testlog.LogCommand(t, "lxc "+args[0], cmd)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "lxc %s failed: %s", args[0], out)
}

// lxcExec runs a command inside an LXD container.
func lxcExec(t *testing.T, containerName string, args ...string) {
	t.Helper()

	fullArgs := append([]string{"exec", containerName, "--"}, args...)
	// #nosec:G204 - we control the command arguments in tests
	cmd := lxcCommand(fullArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	testlog.LogCommand(t, fmt.Sprintf("lxc exec %s -- %s", containerName, args[0]), cmd)
	err := cmd.Run()
	require.NoError(t, err, "lxc exec %s -- %s failed", containerName, strings.Join(args, " "))
}

// LXCExecFromDir runs a command inside an LXD container with a specific
// working directory.
func LXCExecFromDir(t *testing.T, containerName, dir string, args ...string) {
	t.Helper()

	fullArgs := append([]string{"exec", containerName, "--cwd", dir, "--"}, args...)
	// #nosec:G204 - we control the command arguments in tests
	cmd := lxcCommand(fullArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	testlog.LogCommand(t, fmt.Sprintf("lxc exec %s -- %s", containerName, args[0]), cmd)
	err := cmd.Run()
	require.NoError(t, err, "lxc exec %s -- %s failed", containerName, strings.Join(args, " "))
}

// LXCExecFromDirAsUser runs a command inside an LXD container with a specific
// working directory and UID/GID.
func LXCExecFromDirAsUser(t *testing.T, containerName, dir string, userID, groupID int, args ...string) {
	t.Helper()

	fullArgs := append([]string{
		"exec", containerName, "--cwd", dir,
		"--user", fmt.Sprintf("%d", userID),
		"--group", fmt.Sprintf("%d", groupID),
		"--",
	}, args...)
	// #nosec:G204 - we control the command arguments in tests
	cmd := lxcCommand(fullArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	testlog.LogCommand(t, fmt.Sprintf("lxc exec %s -- %s", containerName, args[0]), cmd)
	err := cmd.Run()
	require.NoError(t, err, "lxc exec %s -- %s failed", containerName, strings.Join(args, " "))
}

// lxcCommand returns an lxc command scoped to the authd test project.
func lxcCommand(args ...string) *exec.Cmd {
	// #nosec:G204 - we control the command arguments in tests
	return exec.Command("lxc", append([]string{"--project", lxdProject}, args...)...)
}
