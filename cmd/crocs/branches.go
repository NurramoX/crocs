package main

import (
	"crocs/internal/output"
	"crocs/internal/vcs"

	"github.com/spf13/cobra"
)

type branchesResponse struct {
	Name     string       `json:"name"`
	Branches []vcs.Branch `json:"branches"`
}

var branchesCmd = &cobra.Command{
	Use:   "branches <name>",
	Short: "List branches for a tracked project",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmdCtx(cmd)
		db, err := openRegistry(ctx)
		if err != nil {
			return err
		}
		defer db.Close()

		p, err := requireProject(ctx, db, args[0])
		if err != nil {
			return err
		}
		branches, err := vcs.Branches(p.Path)
		if err != nil {
			return err
		}
		if branches == nil {
			branches = []vcs.Branch{}
		}
		return output.Write(cmd.OutOrStdout(), "branches", branchesResponse{
			Name:     p.Name,
			Branches: branches,
		})
	},
}

func init() {
	rootCmd.AddCommand(branchesCmd)
}
