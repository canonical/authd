package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

type (
	DaemonConfig = daemonConfig
	SystemPaths  = systemPaths
)

func NewForTests(t *testing.T, conf *DaemonConfig, providerURL string, args ...string) *App {
	t.Helper()

	p := GenerateTestConfig(t, conf, providerURL)
	argsWithConf := []string{"--paths-config", p}
	argsWithConf = append(argsWithConf, args...)

	a := New(t.Name())
	a.rootCmd.SetArgs(argsWithConf)
	return a
}

func GenerateTestConfig(t *testing.T, origConf *daemonConfig, providerURL string) string {
	t.Helper()

	var conf daemonConfig

	if origConf != nil {
		conf = *origConf
	}

	if conf.Verbosity == 0 {
		conf.Verbosity = 2
	}
	if conf.Paths.DataDir == "" {
		conf.Paths.DataDir = t.TempDir()
		//nolint: gosec // This is a directory owned only by the current user for tests.
		err := os.Chmod(conf.Paths.DataDir, 0700)
		require.NoError(t, err, "Setup: could not change permission on cache directory for tests")
	}
	if conf.Paths.BrokerConf == "" {
		conf.Paths.BrokerConf = filepath.Join(t.TempDir(), strings.ReplaceAll(t.Name(), "/", "_")+".yaml")
	}

	GenerateBrokerConfig(t, conf.Paths.BrokerConf, providerURL)

	d, err := yaml.Marshal(conf)
	require.NoError(t, err, "Setup: could not marshal configuration for tests")

	confPath := filepath.Join(t.TempDir(), "testconfig.yaml")
	err = os.WriteFile(confPath, d, 0600)
	require.NoError(t, err, "Setup: could not create configuration for tests")

	return confPath
}

// GenerateBrokerConfig creates a broker configuration file for tests.
func GenerateBrokerConfig(t *testing.T, p, providerURL string) {
	t.Helper()

	err := os.MkdirAll(filepath.Dir(p), 0700)
	require.NoError(t, err, "Setup: could not create parent broker configuration directory for tests")

	brokerCfg := fmt.Sprintf(`
	[authd]
	name = %[1]s
	brand_icon = broker_icon.png
	dbus_name = com.ubuntu.authd.%[1]s
	dbus_object = /com/ubuntu/authd/%[1]s

	[oidc]
	issuer = %[2]s
	client_id = client_id
	`, strings.ReplaceAll(t.Name(), "/", "_"), providerURL)
	err = os.WriteFile(p, []byte(brokerCfg), 0600)
	require.NoError(t, err, "Setup: could not create broker configuration for tests")
}

// Config returns a DaemonConfig for tests.
func (a App) Config() DaemonConfig {
	return a.config
}

// SetArgs set some arguments on root command for tests.
func (a *App) SetArgs(args ...string) {
	a.rootCmd.SetArgs(args)
}
