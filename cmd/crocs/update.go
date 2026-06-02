package main

import (
	"fmt"
	"time"

	"crocs/internal/output"
	"crocs/internal/vcs"

	"github.com/spf13/cobra"
)

type updateResponse struct {
	Name        string `json:"name"`
	Ref         string `json:"ref"`
	SymbolCount int    `json:"symbol_count"`
}

var updateCmd = &cobra.Command{
	Use:   "update <name>",
	Short: "Pull latest changes for a tracked project",
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
		if err := vcs.Pull(ctx, p.Path); err != nil {
			return err
		}
		ref, _ := vcs.CurrentRef(p.Path)
		if err := db.UpdateProjectRef(ctx, p.Name, ref, time.Now().UTC()); err != nil {
			return err
		}
		nSym, perr := reparseProject(ctx, db, p)
		if perr != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), "crocs: symbol reindex failed:", perr)
		}
		return output.Write(cmd.OutOrStdout(), "update", updateResponse{Name: p.Name, Ref: ref, SymbolCount: nSym})
	},
}

func init() {
	rootCmd.AddCommand(updateCmd)
}
