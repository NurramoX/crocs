package main

import (
	"crocs/internal/output"

	"github.com/spf13/cobra"
)

type updateResponse struct {
	Name         string `json:"name"`
	Ref          string `json:"ref"`
	Kind         string `json:"kind,omitempty"`
	PrevCommit   string `json:"previous_commit,omitempty"`
	Commit       string `json:"commit,omitempty"`
	Changed      bool   `json:"changed"`
	SymbolCount  int    `json:"symbol_count"`
	ReindexError string `json:"reindex_error,omitempty"`
}

var updateCmd = &cobra.Command{
	Use:   "update <name>",
	Short: "Bring one checkout up to date with origin",
	Long: `Advance a checkout to what origin has now. The default checkout (on a
branch) is fetched and reset to origin's tip; a pinned checkout
(<name>@<ref>) re-resolves its ref against origin and moves to the new
commit if the branch or tag has moved (a commit pin never moves, so it
only repairs a tampered tree). The symbol index is rebuilt only when the
working tree actually changed.`,
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
		res, err := advanceCheckout(ctx, db, p)
		if err != nil {
			return err
		}
		resp := updateResponse{Name: p.Name, Ref: res.Ref, Kind: res.Kind, PrevCommit: res.Before, Commit: res.Commit, Changed: res.Changed, SymbolCount: res.SymbolCount}
		if res.ReindexError != nil {
			resp.ReindexError = res.ReindexError.Error()
		}
		return output.Write(cmd.OutOrStdout(), "update", resp)
	},
}

func init() {
	rootCmd.AddCommand(updateCmd)
}
