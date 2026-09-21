package main

import (
	"crocs/internal/output"
	"crocs/internal/vcs"

	"github.com/spf13/cobra"
)

type tagsResponse struct {
	Name      string    `json:"name"`
	Repo      string    `json:"repo"`
	Shallow   bool      `json:"shallow"`
	Hint      string    `json:"hint,omitempty"`
	Tags      []vcs.Tag `json:"tags"`
	Truncated bool      `json:"truncated"`
}

var tagsLimit int

var tagsCmd = &cobra.Command{
	Use:   "tags <name>",
	Short: "List tags for a tracked project",
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
		tags, err := vcs.Tags(p.Path)
		if err != nil {
			return err
		}
		if tags == nil {
			tags = []vcs.Tag{}
		}
		tags, truncated := capList(tags, tagsLimit)
		hint := ""
		if p.Shallow {
			// The single most misleading output in the old CLI: a confident
			// empty tag list on every fresh clone. Say why it's empty.
			hint = shallowHint(p.Repo, "tags were not fetched")
		}
		return output.Write(cmd.OutOrStdout(), "tags", tagsResponse{
			Name:      p.Name,
			Repo:      p.Repo,
			Shallow:   p.Shallow,
			Hint:      hint,
			Tags:      tags,
			Truncated: truncated,
		})
	},
}

func init() {
	tagsCmd.Flags().IntVarP(&tagsLimit, "limit", "n", 0, "max tags to return (0 = no cap)")
	rootCmd.AddCommand(tagsCmd)
}
