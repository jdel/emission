package cmd

import (
	"fmt"

	"github.com/jdel/emission/internal/client"
	"github.com/spf13/cobra"
)

func clientsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clients",
		Short: "List all available client profiles",
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, v := range client.Versions() {
				fmt.Println(v)
			}
			return nil
		},
	}
}
