package main

import (
	"crocs/internal/output"
	"crocs/internal/treemap"

	"github.com/spf13/cobra"
)

type treeResponse struct {
	Name  string   `json:"name"`
	Files []string `json:"files"`
}

var (
	treeIncludes []string
	treeExcludes []string
)

var treeCmd = &cobra.Command{
	Use:   "tree <name>",
	Short: "List project files (one path per array entry)",
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

		files, err := treemap.Walk(p.Path, treemap.Filters{
			Includes: treeIncludes,
			Excludes: treeExcludes,
		})
		if err != nil {
			return err
		}
		if files == nil {
			files = []string{} // emit `[]`, never `null`
		}
		return output.Write(cmd.OutOrStdout(), "tree", treeResponse{
			Name:  p.Name,
			Files: files,
		})
	},
}

func init() {
	treeCmd.Flags().StringSliceVarP(&treeIncludes, "include", "i", nil, "include only paths under this path (repeatable)")
	treeCmd.Flags().StringSliceVarP(&treeExcludes, "exclude", "e", nil, "exclude paths under this path (repeatable)")
	rootCmd.AddCommand(treeCmd)
}
