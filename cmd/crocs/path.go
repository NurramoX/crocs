package main

import (
	"crocs/internal/output"

	"github.com/spf13/cobra"
)

type pathResponse struct {
	Path string `json:"path"`
}

var pathCmd = &cobra.Command{
	Use:   "path <name>",
	Short: "Print a tracked project's on-disk path",
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
		return output.Write(cmd.OutOrStdout(), "path", pathResponse{Path: p.Path})
	},
}

func init() {
	rootCmd.AddCommand(pathCmd)
}
