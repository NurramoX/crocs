package main

import (
	"fmt"
	"os"

	"crocs/internal/output"

	"github.com/spf13/cobra"
)

type removeResponse struct {
	Removed projectJSON `json:"removed"`
}

var removeCmd = &cobra.Command{
	Use:     "remove <name>",
	Aliases: []string{"rm"},
	Short:   "Untrack a project and delete its clone from disk",
	Args:    cobra.ExactArgs(1),
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

		// Best-effort disk delete first. If the dir is gone or read-only, we
		// still want to drop the registry row so the user isn't stuck with
		// a phantom entry.
		if err := os.RemoveAll(p.Path); err != nil {
			return fmt.Errorf("delete %s: %w", p.Path, err)
		}
		if err := db.DeleteProject(ctx, p.Name); err != nil {
			return fmt.Errorf("untrack %s: %w", p.Name, err)
		}
		return output.Write(cmd.OutOrStdout(), "remove", removeResponse{Removed: projectToJSON(p)})
	},
}

func init() {
	rootCmd.AddCommand(removeCmd)
}
