package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

type (
	DaemonConfig = daemonConfig
	SystemPaths  = systemPaths
)

func NewForTests(t *testing.T, conf *DaemonConfig, args ...string) *App {
	t.Helper()

	p := GenerateTestConfig(t, conf)
	argsWithConf := []string{"--config", p}
	argsWithConf = append(argsWithConf, args...)

	a := New()
	a.rootCmd.SetArgs(argsWithConf)
	return a
}

func GenerateTestConfig(t *testing.T, origConf *daemonConfig) string {
	t.Helper()

	var conf daemonConfig

	if origConf != nil {
		conf = *origConf
	}

	if conf.Verbosity == 0 {
		conf.Verbosity = 2
	}
	if conf.Paths.Database == "" {
		conf.Paths.Database = t.TempDir()
		//nolint: gosec // This is a directory owned only by the current user for tests.
		err := os.Chmod(conf.Paths.Database, 0700)
		require.NoError(t, err, "Setup: could not change permission on database directory for tests")
	}
	if conf.Paths.Socket == "" {
		conf.Paths.Socket = filepath.Join(t.TempDir(), "authd.socket")
	}
	d, err := yaml.Marshal(conf)
	require.NoError(t, err, "Setup: could not marshal configuration for tests")

	t.Logf("Test config file is\n%s", d)
	confPath := filepath.Join(t.TempDir(), "testconfig.yaml")
	err = os.WriteFile(confPath, d, 0600)
	require.NoError(t, err, "Setup: could not create configuration for tests")

	return confPath
}

// Config returns a DaemonConfig for tests.
func (a App) Config() DaemonConfig {
	return a.config
}

// SetArgs set some arguments on root command for tests.
func (a *App) SetArgs(args ...string) {
	a.rootCmd.SetArgs(args)
}
