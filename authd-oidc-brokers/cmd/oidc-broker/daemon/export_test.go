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

	a := New(t.Name())
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
	if conf.Paths.Cache == "" {
		conf.Paths.Cache = t.TempDir()
		//nolint: gosec // This is a directory owned only by the current user for tests.
		err := os.Chmod(conf.Paths.Cache, 0700)
		require.NoError(t, err, "Setup: could not change permission on cache directory for tests")
	}
	d, err := yaml.Marshal(conf)
	require.NoError(t, err, "Setup: could not marshal configuration for tests")

	confPath := filepath.Join(t.TempDir(), "testconfig.yaml")
	err = os.WriteFile(confPath, d, 0600)
	require.NoError(t, err, "Setup: could not create configuration for tests")

	return confPath
}

// Config returns a DaemonConfig for tests.
//
//nolint:revive // DaemonConfig is a type alias for tests
func (a App) Config() DaemonConfig {
	return a.config
}

// SetArgs set some arguments on root command for tests.
func (a *App) SetArgs(args ...string) {
	a.rootCmd.SetArgs(args)
}
