package main

import (
	"fmt"

	"crocs/internal/output"
	"crocs/internal/vcs"

	"github.com/spf13/cobra"
)

type branchesResponse struct {
	Name      string       `json:"name"`
	Shallow   bool         `json:"shallow"`
	Hint      string       `json:"hint,omitempty"`
	Branches  []vcs.Branch `json:"branches"`
	Truncated bool         `json:"truncated"`
}

var branchesLimit int

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
		truncated := false
		if branchesLimit > 0 && len(branches) > branchesLimit {
			branches = branches[:branchesLimit]
			truncated = true
		}
		hint := ""
		if p.Shallow {
			hint = fmt.Sprintf("shallow single-branch clone: remote branches were not fetched; run `crocs unshallow %s` to see them all", p.Name)
		}
		return output.Write(cmd.OutOrStdout(), "branches", branchesResponse{
			Name:      p.Name,
			Shallow:   p.Shallow,
			Hint:      hint,
			Branches:  branches,
			Truncated: truncated,
		})
	},
}

func init() {
	branchesCmd.Flags().IntVarP(&branchesLimit, "limit", "n", 0, "max branches to return (0 = no cap)")
	rootCmd.AddCommand(branchesCmd)
}
