package main

import (
	"crocs/internal/output"
	"crocs/internal/vcs"

	"github.com/spf13/cobra"
)

type unshallowResponse struct {
	Name         string `json:"name"`
	Shallow      bool   `json:"shallow"`
	SymbolCount  int    `json:"symbol_count"`
	ReindexError string `json:"reindex_error,omitempty"`
}

var unshallowCmd = &cobra.Command{
	Use:   "unshallow <name>",
	Short: "Convert a shallow/blobless clone to full history",
	Long: `Fetch the full history for a shallow clone and drop the blob filter. This
un-cripples branches/tags/log/diff/checkout, which see only the fetched ref
on the default clone. The working tree does not change, so the symbol index
is left as-is (it is built only if missing).`,
	Args: cobra.ExactArgs(1),
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
		if err := vcs.Unshallow(ctx, p.Path); err != nil {
			return err
		}
		if err := db.SetShallow(ctx, p.Name, false); err != nil {
			return err
		}
		// fetch --unshallow doesn't touch the working tree, so an existing
		// symbol index is still exact — only build one if it never existed.
		resp := unshallowResponse{Name: p.Name, Shallow: false}
		if p.ParsedAt == nil {
			nSym, perr := reparseProject(ctx, db, p)
			if perr != nil {
				resp.ReindexError = perr.Error()
			}
			resp.SymbolCount = nSym
		} else {
			if nSym, cerr := db.CountSymbols(ctx, p.Name); cerr == nil {
				resp.SymbolCount = nSym
			}
		}
		return output.Write(cmd.OutOrStdout(), "unshallow", resp)
	},
}

func init() {
	rootCmd.AddCommand(unshallowCmd)
}
