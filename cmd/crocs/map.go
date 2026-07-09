package main

import (
	"crocs/internal/output"
	"crocs/internal/treemap"

	"github.com/spf13/cobra"
)

type mapResponse struct {
	Name        string             `json:"name"`
	Directories []treemap.DirCount `json:"directories"`
}

var (
	mapIncludes []string
	mapExcludes []string
)

var mapCmd = &cobra.Command{
	Use:   "map <name>",
	Short: "Directory heatmap: per-directory file counts (non-recursive), hottest first",
	Long: `Per-directory file counts, hottest first. Counts are non-recursive: each
directory counts only the files directly in it, so parents don't drown out
the dense leaf directories agents are looking for.`,
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

		dirs, err := treemap.DirCounts(p.Path, treemap.Filters{
			Includes: mapIncludes,
			Excludes: mapExcludes,
		})
		if err != nil {
			return err
		}
		if dirs == nil {
			dirs = []treemap.DirCount{}
		}
		return output.Write(cmd.OutOrStdout(), "map", mapResponse{
			Name:        p.Name,
			Directories: dirs,
		})
	},
}

func init() {
	mapCmd.Flags().StringSliceVarP(&mapIncludes, "include", "i", nil, "include only paths under this path (repeatable)")
	mapCmd.Flags().StringSliceVarP(&mapExcludes, "exclude", "e", nil, "exclude paths under this path (repeatable)")
	rootCmd.AddCommand(mapCmd)
}
