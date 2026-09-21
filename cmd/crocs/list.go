package main

import (
	"fmt"

	"crocs/internal/output"

	"github.com/spf13/cobra"
)

type listResponse struct {
	Projects []projectJSON `json:"projects"`
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all tracked repos and their checkouts",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmdCtx(cmd)
		db, err := openRegistry(ctx)
		if err != nil {
			return err
		}
		defer db.Close()

		all, err := db.ListProjects(ctx)
		if err != nil {
			return fmt.Errorf("list projects: %w", err)
		}
		return output.Write(cmd.OutOrStdout(), "list", listResponse{Projects: groupProjects(all)})
	},
}

func init() {
	rootCmd.AddCommand(listCmd)
}
