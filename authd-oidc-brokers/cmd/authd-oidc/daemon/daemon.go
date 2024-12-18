// Package daemon represents the oidc broker binary
package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/ubuntu/authd-oidc-brokers/internal/broker"
	"github.com/ubuntu/authd-oidc-brokers/internal/daemon"
	"github.com/ubuntu/authd-oidc-brokers/internal/dbusservice"
)

// App encapsulate commands and options of the daemon, which can be controlled by env variables and config files.
type App struct {
	rootCmd cobra.Command
	viper   *viper.Viper
	config  daemonConfig

	daemon *daemon.Daemon
	name   string

	ready chan struct{}
}

// only overriable for tests.
type systemPaths struct {
	BrokerConf            string
	DataDir               string
	OldEncryptedTokensDir string
}

// daemonConfig defines configuration parameters of the daemon.
type daemonConfig struct {
	Verbosity int
	Paths     systemPaths
}

// New registers commands and return a new App.
func New(name string) *App {
	a := App{ready: make(chan struct{}), name: name}
	a.rootCmd = cobra.Command{
		Use:   fmt.Sprintf("%s COMMAND", name),
		Short: fmt.Sprintf("%s authentication broker", name),
		Long:  fmt.Sprintf("Authentication daemon %s to communicate with our authentication daemon.", name),
		Args:  cobra.NoArgs,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Command parsing has been successful. Returns to not print usage anymore.
			a.rootCmd.SilenceUsage = true

			// Before version 0.2, we used to store the tokens encrypted
			// in a different directory. For backward compatibility, we
			// try to use the encrypted tokens from the old directory
			// if they are not found in the new one.
			oldEncryptedTokensDir := filepath.Join("/var", "lib", name)
			dataDir := filepath.Join("/var", "lib", name)
			configDir := "."
			if snapData := os.Getenv("SNAP_DATA"); snapData != "" {
				oldEncryptedTokensDir = filepath.Join(snapData, "cache")
				dataDir = snapData
				configDir = snapData
			}
			// Set config defaults
			a.config = daemonConfig{
				Paths: systemPaths{
					BrokerConf:            filepath.Join(configDir, "broker.conf"),
					DataDir:               dataDir,
					OldEncryptedTokensDir: oldEncryptedTokensDir,
				},
			}

			// Install and unmarshall configuration
			if err := initViperConfig(name, &a.rootCmd, a.viper); err != nil {
				return err
			}
			if err := a.viper.Unmarshal(&a.config); err != nil {
				return fmt.Errorf("unable to decode configuration into struct: %w", err)
			}

			// FIXME: for now, config is only the broker.conf file. It should be merged with the viper configuration.
			if v, err := cmd.Flags().GetString("config"); err == nil && v != "" {
				a.config.Paths.BrokerConf = v
			}

			setVerboseMode(a.config.Verbosity)
			slog.Debug("Debug mode is enabled")

			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.serve(a.config)
		},
		// We display usage error ourselves
		SilenceErrors: true,
	}
	viper := viper.New()

	a.viper = viper

	installVerbosityFlag(&a.rootCmd, a.viper)
	installConfigFlag(&a.rootCmd)
	// FIXME: This option is for the viper path configuration. We should merge --config and this one in the future.
	a.rootCmd.PersistentFlags().StringP("paths-config", "", "", "use a specific paths configuration file")

	// subcommands
	a.installVersion()

	return &a
}

// serve creates new dbus service on the system bus. This call is blocking until we quit it.
func (a *App) serve(config daemonConfig) error {
	ctx := context.Background()

	// When the data directory is SNAP_DATA, it has permission 0755, else we want to create it with 0700.
	if err := ensureDirWithPerms(config.Paths.DataDir, 0700, os.Geteuid()); err != nil {
		if err := ensureDirWithPerms(config.Paths.DataDir, 0755, os.Geteuid()); err != nil {
			close(a.ready)
			return fmt.Errorf("error initializing data directory %q: %v", config.Paths.DataDir, err)
		}
	}

	brokerConfigDir := broker.GetDropInDir(config.Paths.BrokerConf)
	if err := ensureDirWithPerms(brokerConfigDir, 0700, os.Geteuid()); err != nil {
		close(a.ready)
		return fmt.Errorf("error initializing broker configuration directory %q: %v", brokerConfigDir, err)
	}

	b, err := broker.New(broker.Config{
		ConfigFile:            config.Paths.BrokerConf,
		DataDir:               config.Paths.DataDir,
		OldEncryptedTokensDir: config.Paths.OldEncryptedTokensDir,
	})
	if err != nil {
		return err
	}

	s, err := dbusservice.New(ctx, b)
	if err != nil {
		close(a.ready)
		return err
	}

	var daemonopts []daemon.Option
	daemon, err := daemon.New(ctx, s, daemonopts...)
	if err != nil {
		_ = s.Stop()
		close(a.ready)
		return err
	}

	a.daemon = daemon
	close(a.ready)

	return daemon.Serve(ctx)
}

// installVerbosityFlag adds the -v and -vv options and returns the reference to it.
func installVerbosityFlag(cmd *cobra.Command, viper *viper.Viper) *int {
	r := cmd.PersistentFlags().CountP("verbosity", "v" /*i18n.G(*/, "issue INFO (-v), DEBUG (-vv) or DEBUG with caller (-vvv) output") //)

	if err := viper.BindPFlag("verbosity", cmd.PersistentFlags().Lookup("verbosity")); err != nil {
		slog.Warn(err.Error())
	}

	return r
}

// Run executes the command and associated process. It returns an error on syntax/usage error.
func (a *App) Run() error {
	return a.rootCmd.Execute()
}

// UsageError returns if the error is a command parsing or runtime one.
func (a App) UsageError() bool {
	return !a.rootCmd.SilenceUsage
}

// Hup prints all goroutine stack traces and return false to signal you shouldn't quit.
func (a App) Hup() (shouldQuit bool) {
	buf := make([]byte, 1<<16)
	runtime.Stack(buf, true)
	fmt.Printf("%s", buf)
	return false
}

// Quit gracefully shutdown the service.
func (a *App) Quit() {
	a.WaitReady()
	if a.daemon == nil {
		return
	}
	a.daemon.Quit()
}

// WaitReady signals when the daemon is ready
// Note: we need to use a pointer to not copy the App object before the daemon is ready, and thus, creates a data race.
func (a *App) WaitReady() {
	<-a.ready
}

// RootCmd returns a copy of the root command for the app. Shouldn't be in general necessary apart when running generators.
func (a App) RootCmd() cobra.Command {
	return a.rootCmd
}
