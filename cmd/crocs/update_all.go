package main

import (
	"time"

	"crocs/internal/output"
	"crocs/internal/vcs"

	"github.com/spf13/cobra"
)

type updateAllItem struct {
	Name        string `json:"name"`
	Ref         string `json:"ref,omitempty"`
	OK          bool   `json:"ok"`
	Error       string `json:"error,omitempty"`
	SymbolCount int    `json:"symbol_count,omitempty"`
}

type updateAllResponse struct {
	Updated []updateAllItem `json:"updated"`
}

var updateAllCmd = &cobra.Command{
	Use:   "update-all",
	Short: "Pull latest changes for every tracked project",
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
			if err := vcs.Pull(ctx, p.Path); err != nil {
				item.Error = err.Error()
				resp.Updated = append(resp.Updated, item)
				continue
			}
			ref, _ := vcs.CurrentRef(p.Path)
			if err := db.UpdateProjectRef(ctx, p.Name, ref, time.Now().UTC()); err != nil {
				item.Error = err.Error()
				resp.Updated = append(resp.Updated, item)
				continue
			}
			item.OK = true
			item.Ref = ref
			nSym, perr := reparseProject(ctx, db, p)
			if perr != nil {
				item.Error = "reindex: " + perr.Error()
				item.OK = false
			} else {
				item.SymbolCount = nSym
			}
			resp.Updated = append(resp.Updated, item)
		}
		return output.Write(cmd.OutOrStdout(), "update-all", resp)
	},
}

func init() {
	rootCmd.AddCommand(updateAllCmd)
}
