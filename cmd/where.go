package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// whereCmd prints emission's resolved data and config locations and the config
// file viper loaded.
func whereCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "where",
		Short: "Print resolved data and config locations",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := initConfig(); err != nil {
				return err
			}
			dataDir, err := appScope.DataPath("")
			if err != nil {
				return err
			}
			configDir, err := appScope.ConfigPath("")
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "data dir:    %s\n", dataDir)
			fmt.Fprintf(out, "config dir:  %s\n", configDir)
			fmt.Fprintf(out, "config file: %s\n", viper.ConfigFileUsed())
			return nil
		},
	}
}
