package main

import (
	"fmt"
	"time"

	"crocs/internal/output"
	"crocs/internal/vcs"

	"github.com/spf13/cobra"
)

type checkoutResponse struct {
	Name         string `json:"name"`
	Ref          string `json:"ref"`
	SymbolCount  int    `json:"symbol_count"`
	ReindexError string `json:"reindex_error,omitempty"`
}

var checkoutCmd = &cobra.Command{
	Use:   "checkout <name> <ref>",
	Short: "Check out a branch, tag, or commit (must exist locally)",
	Long: `Switch the tracked clone to another ref and rebuild the symbol index for
the new snapshot. The ref must already exist locally: the default fetch is a
shallow single-branch clone, so most branches and tags require
` + "`crocs unshallow <name>`" + ` first.`,
	Args: cobra.ExactArgs(2),
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
			if p.Shallow {
				return fmt.Errorf("%w (project is a shallow single-branch clone — run `crocs unshallow %s` to make other refs available)", err, p.Name)
			}
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
		reindexErr := ""
		nSym, perr := reparseProject(ctx, db, p)
		if perr != nil {
			reindexErr = perr.Error()
		}

		return output.Write(cmd.OutOrStdout(), "checkout", checkoutResponse{
			Name:         p.Name,
			Ref:          actual,
			SymbolCount:  nSym,
			ReindexError: reindexErr,
		})
	},
}

func init() {
	rootCmd.AddCommand(checkoutCmd)
}
