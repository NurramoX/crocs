package main

import (
	"crocs/internal/output"
	"crocs/internal/vcs"

	"github.com/spf13/cobra"
)

type diffResponse struct {
	Name string `json:"name"`
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	Diff string `json:"diff"`
}

var (
	diffFrom string
	diffTo   string
)

var diffCmd = &cobra.Command{
	Use:   "diff <name>",
	Short: "Show a diff for a tracked project",
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
		d, err := vcs.Diff(p.Path, diffFrom, diffTo)
		if err != nil {
			return err
		}
		return output.Write(cmd.OutOrStdout(), "diff", diffResponse{
			Name: p.Name,
			From: diffFrom,
			To:   diffTo,
			Diff: d,
		})
	},
}

func init() {
	diffCmd.Flags().StringVar(&diffFrom, "from", "", "from-ref (default: working tree base)")
	diffCmd.Flags().StringVar(&diffTo, "to", "", "to-ref (default: HEAD)")
	rootCmd.AddCommand(diffCmd)
}
