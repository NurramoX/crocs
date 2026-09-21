package main

import (
	"crocs/internal/output"

	"github.com/spf13/cobra"
)

type infoResponse struct {
	Project  projectJSON  `json:"project"`
	Checkout checkoutJSON `json:"checkout"`
}

var infoCmd = &cobra.Command{
	Use:   "info <name>",
	Short: "Show a tracked repo, all its checkouts, and the one addressed",
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
		all, err := repoCheckouts(ctx, db, p.Repo)
		if err != nil {
			return err
		}
		return output.Write(cmd.OutOrStdout(), "info", infoResponse{
			Project:  groupProjects(all)[0],
			Checkout: checkoutToJSON(p),
		})
	},
}

func init() {
	rootCmd.AddCommand(infoCmd)
}
