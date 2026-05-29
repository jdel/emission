package cmd

import (
	"errors"
	"os"
	"strings"
	"time"

	gap "github.com/muesli/go-app-paths"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var version = "dev"

// appScope locates emission's XDG-compliant config and data directories.
var appScope = gap.NewScope(gap.User, "emission")

// RootCmd builds the top-level cobra command tree (emission seed | serve | clients)
// with the shared flag set and viper bindings used by every subcommand.
func RootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "emission",
		Short:         "Spoof BitTorrent tracker announces to boost your ratio",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			lvlStr := viper.GetString("log-level")
			lvl, err := zerolog.ParseLevel(lvlStr)
			if err != nil {
				return err
			}
			zerolog.SetGlobalLevel(lvl)
			return nil
		},
	}
	cmd.PersistentFlags().String("config", "", "config file (yaml/toml/json); auto-discovered if unset")
	cmd.PersistentFlags().String("log-level", "info", "log level (trace, debug, info, warn, error)")

	// Flags shared by seed and serve — defined once here so viper binds a single flag object.
	cmd.PersistentFlags().String("storage.torrents", "", "directory of .torrent files to watch (default: XDG data dir)")
	cmd.PersistentFlags().String("client.name", "transmission-4.0.6", "client profile to impersonate")
	cmd.PersistentFlags().String("client.bandwidth", "1M", "per-user upload bandwidth ceiling (also each new torrent's default max), shared across a user's torrents by leecher share")
	cmd.PersistentFlags().Int("client.max-peers", 0, "peers to request from each tracker (0 = client default)")
	cmd.PersistentFlags().Float64("client.max-ratio", 0, "stop accumulating upload once uploaded reaches N × torrent size (0 = unlimited)")
	cmd.PersistentFlags().Bool("client.autoremove", false, "automatically remove the torrent when the ratio cap is reached")

	for _, name := range []string{
		"config", "log-level",
		"storage.torrents",
		"client.name", "client.bandwidth", "client.max-peers", "client.max-ratio",
		"client.autoremove",
	} {
		_ = viper.BindPFlag(name, cmd.PersistentFlags().Lookup(name))
	}

	log.Logger = zerolog.New(
		zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.DateTime},
	).With().Timestamp().Logger()

	cmd.AddCommand(clientsCmd())
	cmd.AddCommand(seedCmd())
	cmd.AddCommand(serveCmd())
	return cmd
}

// initConfig loads configuration from a file and the environment.
//
// Resolution order, lowest to highest precedence:
//
//	flag default  <  config file  <  EMISSION_* env var  <  explicit flag
//
// If --config is given, that file must exist. Otherwise a file named
// emission.{yaml,toml,json} is looked up in the current directory first,
// then in the XDG config dirs (gap.ConfigDirs) — a missing file there is
// not an error.
//
// Env vars use the EMISSION_ prefix with dashes mapped to underscores, e.g.
// flag --log-level is EMISSION_LOG_LEVEL.
func initConfig() error {
	viper.SetEnvPrefix("EMISSION")
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
	viper.AutomaticEnv()

	if explicit := viper.GetString("config"); explicit != "" {
		viper.SetConfigFile(explicit)
		return viper.ReadInConfig()
	}

	viper.SetConfigName("emission")
	viper.AddConfigPath(".")
	if dirs, err := appScope.ConfigDirs(); err == nil {
		for _, d := range dirs {
			viper.AddConfigPath(d)
		}
	}
	if err := viper.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if errors.As(err, &notFound) {
			return nil
		}
		return err
	}
	return nil
}
