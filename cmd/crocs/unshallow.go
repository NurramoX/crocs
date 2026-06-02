package main

import (
	"fmt"

	"crocs/internal/output"
	"crocs/internal/vcs"

	"github.com/spf13/cobra"
)

type unshallowResponse struct {
	Name        string `json:"name"`
	Shallow     bool   `json:"shallow"`
	SymbolCount int    `json:"symbol_count"`
}

var unshallowCmd = &cobra.Command{
	Use:   "unshallow <name>",
	Short: "Convert a shallow/blobless clone to full history",
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
		if err := vcs.Unshallow(ctx, p.Path); err != nil {
			return err
		}
		if err := db.SetShallow(ctx, p.Name, false); err != nil {
			return err
		}
		p.Shallow = false
		nSym, perr := reparseProject(ctx, db, p)
		if perr != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), "crocs: symbol reindex failed:", perr)
		}
		return output.Write(cmd.OutOrStdout(), "unshallow", unshallowResponse{
			Name:        p.Name,
			Shallow:     false,
			SymbolCount: nSym,
		})
	},
}

func init() {
	rootCmd.AddCommand(unshallowCmd)
}
