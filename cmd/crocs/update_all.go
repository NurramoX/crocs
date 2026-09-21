package main

import (
	"crocs/internal/output"

	"github.com/spf13/cobra"
)

type updateAllItem struct {
	Name         string `json:"name"`
	Ref          string `json:"ref,omitempty"`
	Kind         string `json:"kind,omitempty"`
	PrevCommit   string `json:"previous_commit,omitempty"`
	Commit       string `json:"commit,omitempty"`
	OK           bool   `json:"ok"`
	Changed      bool   `json:"changed"`
	Error        string `json:"error,omitempty"`
	SymbolCount  int    `json:"symbol_count"`
	ReindexError string `json:"reindex_error,omitempty"`
}

type updateAllResponse struct {
	Updated []updateAllItem `json:"updated"`
}

var updateAllCmd = &cobra.Command{
	Use:   "update-all",
	Short: "Bring every checkout of every tracked repo up to date",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmdCtx(cmd)
		db, err := openRegistry(ctx)
		if err != nil {
			return err
		}
		defer db.Close()

		all, err := db.ListProjects(ctx)
		if err != nil {
			return err
		}

		resp := updateAllResponse{Updated: make([]updateAllItem, 0, len(all))}
		for _, p := range all {
			item := updateAllItem{Name: p.Name}
			res, err := advanceCheckout(ctx, db, p)
			if err != nil {
				item.Error = err.Error()
				resp.Updated = append(resp.Updated, item)
				continue
			}
			// The git advance and registry update succeeded — that's what OK
			// means. A reindex failure is reported separately so it doesn't
			// read as "the git state didn't advance" when it did.
			item.OK = true
			item.Ref = res.Ref
			item.Kind = res.Kind
			item.PrevCommit = res.Before
			item.Commit = res.Commit
			item.Changed = res.Changed
			item.SymbolCount = res.SymbolCount
			if res.ReindexError != nil {
				item.ReindexError = res.ReindexError.Error()
			}
			resp.Updated = append(resp.Updated, item)
		}
		return output.Write(cmd.OutOrStdout(), "update-all", resp)
	},
}

func init() {
	rootCmd.AddCommand(updateAllCmd)
}
