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
	Short: "List all tracked projects",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmdCtx(cmd)
		db, err := openRegistry(ctx)
		if err != nil {
			return err
		}
		defer db.Close()

		projects, err := db.ListProjects(ctx)
		if err != nil {
			return fmt.Errorf("list projects: %w", err)
		}

		resp := listResponse{Projects: make([]projectJSON, 0, len(projects))}
		for _, p := range projects {
			resp.Projects = append(resp.Projects, projectToJSON(p))
		}
		return output.Write(cmd.OutOrStdout(), "list", resp)
	},
}

func init() {
	rootCmd.AddCommand(listCmd)
}
