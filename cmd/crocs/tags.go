package main

import (
	"crocs/internal/output"
	"crocs/internal/vcs"

	"github.com/spf13/cobra"
)

type tagsResponse struct {
	Name string    `json:"name"`
	Tags []vcs.Tag `json:"tags"`
}

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
		return output.Write(cmd.OutOrStdout(), "tags", tagsResponse{
			Name: p.Name,
			Tags: tags,
		})
	},
}

func init() {
	rootCmd.AddCommand(tagsCmd)
}
