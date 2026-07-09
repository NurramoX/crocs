package main

import (
	"time"

	"crocs/internal/output"
	"crocs/internal/vcs"

	"github.com/spf13/cobra"
)

type updateResponse struct {
	Name         string `json:"name"`
	Ref          string `json:"ref"`
	Changed      bool   `json:"changed"`
	SymbolCount  int    `json:"symbol_count"`
	ReindexError string `json:"reindex_error,omitempty"`
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
		before, _ := vcs.HeadHash(p.Path)
		if err := vcs.Pull(ctx, p.Path); err != nil {
			return err
		}
		ref, err := vcs.CurrentRef(p.Path)
		if err != nil || ref == "" {
			ref = p.DefaultRef
		}
		if err := db.UpdateProjectRef(ctx, p.Name, ref, time.Now().UTC()); err != nil {
			return err
		}

		changed, nSym, perr := syncAfterPull(ctx, db, p, before)
		resp := updateResponse{Name: p.Name, Ref: ref, Changed: changed, SymbolCount: nSym}
		if perr != nil {
			resp.ReindexError = perr.Error()
		}
		return output.Write(cmd.OutOrStdout(), "update", resp)
	},
}

func init() {
	rootCmd.AddCommand(updateCmd)
}
