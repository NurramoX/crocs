package main

import (
	"fmt"
	"time"

	"crocs/internal/output"
	"crocs/internal/vcs"

	"github.com/spf13/cobra"
)

type checkoutResponse struct {
	Name        string `json:"name"`
	Ref         string `json:"ref"`
	SymbolCount int    `json:"symbol_count"`
}

var checkoutCmd = &cobra.Command{
	Use:   "checkout <name> <ref>",
	Short: "Check out a branch or tag",
	Args:  cobra.ExactArgs(2),
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
		ref := args[1]
		if err := vcs.Checkout(ctx, p.Path, ref); err != nil {
			return err
		}
		// Refresh the registry's idea of "current ref" (the input may have
		// been an alias).
		actual, err := vcs.CurrentRef(p.Path)
		if err != nil || actual == "" {
			actual = ref
		}
		if err := db.UpdateProjectRef(ctx, p.Name, actual, time.Now().UTC()); err != nil {
			return err
		}

		// Snapshot changed → reparse the symbol index. PLAN.md §5 mandates this
		// on checkout/update/unshallow.
		nSym, perr := reparseProject(ctx, db, p)
		if perr != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), "crocs: symbol reindex failed:", perr)
		}

		return output.Write(cmd.OutOrStdout(), "checkout", checkoutResponse{
			Name:        p.Name,
			Ref:         actual,
			SymbolCount: nSym,
		})
	},
}

func init() {
	rootCmd.AddCommand(checkoutCmd)
}
