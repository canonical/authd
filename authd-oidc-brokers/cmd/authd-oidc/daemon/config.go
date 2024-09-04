package daemon

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/ubuntu/authd-oidc-brokers/internal/consts"
	"github.com/ubuntu/authd-oidc-brokers/internal/log"
	"github.com/ubuntu/decorate"
	"gopkg.in/ini.v1"
)

// initViperConfig sets verbosity level and add config env variables and file support based on name prefix.
func initViperConfig(name string, cmd *cobra.Command, vip *viper.Viper) (err error) {
	defer decorate.OnError(&err, "can't load configuration")

	// Force a visit of the local flags so persistent flags for all parents are merged.
	//cmd.LocalFlags() // TODO: still necessary?

	// Get cmdline flag for verbosity to configure logger until we have everything parsed.
	v, err := cmd.Flags().GetCount("verbosity")
	if err != nil {
		return fmt.Errorf("internal error: no persistent verbosity flag installed on cmd: %w", err)
	}
	setVerboseMode(v)

	// Handle configuration.
	if v, err := cmd.Flags().GetString("paths-config"); err == nil && v != "" {
		vip.SetConfigFile(v)
	} else {
		vip.SetConfigName(name)
		vip.AddConfigPath("./")
		vip.AddConfigPath("$HOME/")
		vip.AddConfigPath("$SNAP_DATA/")
		vip.AddConfigPath(filepath.Join("/etc", name))
		// Add the executable path to the config search path.
		if binPath, err := os.Executable(); err != nil {
			slog.Warn(fmt.Sprintf("Failed to get current executable path, not adding it as a config dir: %v", err))
		} else {
			vip.AddConfigPath(filepath.Dir(binPath))
		}
	}

	if err := vip.ReadInConfig(); err != nil {
		var e viper.ConfigFileNotFoundError
		if errors.As(err, &e) {
			slog.Info(fmt.Sprintf("No configuration file: %v.\nWe will only use the defaults, env variables or flags.", e))
		} else {
			return fmt.Errorf("invalid configuration file: %w", err)
		}
	} else {
		slog.Info(fmt.Sprintf("Using configuration file: %v", vip.ConfigFileUsed()))
	}

	// Handle environment.
	vip.SetEnvPrefix(name)
	vip.AutomaticEnv()

	// Visit manually env to bind every possibly related environment variable to be able to unmarshall
	// those into a struct.
	// More context on https://github.com/spf13/viper/pull/1429.
	prefix := strings.ToUpper(name) + "_"
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, prefix) {
			continue
		}

		s := strings.Split(e, "=")
		k := strings.ReplaceAll(strings.TrimPrefix(s[0], prefix), "_", ".")
		if err := vip.BindEnv(k, s[0]); err != nil {
			return fmt.Errorf("could not bind environment variable: %w", err)
		}
	}

	return nil
}

// installConfigFlag installs a --config option.
func installConfigFlag(cmd *cobra.Command) *string {
	return cmd.PersistentFlags().StringP("config", "c", "", "use a specific configuration file")
}

// SetVerboseMode change ErrorFormat and logs between very, middly and non verbose.
func setVerboseMode(level int) {
	//var reportCaller bool
	switch level {
	case 0:
		log.SetLevel(consts.DefaultLevelLog)
	case 1:
		log.SetLevel(slog.LevelDebug)
	case 3:
		//reportCaller = true
		fallthrough
	default:
		log.SetLevel(slog.LevelDebug)
	}

	//slog.SetReportCaller(reportCaller)
}

// parseConfig parses the config file and returns a map with the configuration keys and values.
func parseConfig(cfgPath string) (map[string]map[string]string, error) {
	iniCfg, err := ini.Load(cfgPath)
	if err != nil {
		return nil, err
	}

	cfg := make(map[string]map[string]string)
	for _, section := range iniCfg.Sections() {
		cfg[section.Name()] = make(map[string]string)
		for _, key := range section.Keys() {
			if strings.Contains(key.String(), "<") && strings.Contains(key.String(), ">") {
				err = errors.Join(err, fmt.Errorf("found invalid character in section %q, key %q", section.Name(), key.Name()))
				continue
			}
			cfg[section.Name()][key.Name()] = key.String()
		}
	}

	// This means we found at least one section that was potentially not edited.
	if err != nil {
		return nil, fmt.Errorf("config file has invalid values, did you edit the file %q?\n%w", cfgPath, err)
	}
	return cfg, nil
}
