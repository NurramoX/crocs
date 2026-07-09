package main

import (
	"crocs/internal/detect"
	"crocs/internal/output"
	"crocs/internal/treemap"

	"github.com/spf13/cobra"
)

type detectResponse struct {
	Name       string             `json:"name"`
	Languages  []detect.LangCount `json:"languages"`
	TotalFiles int                `json:"total_files"`
}

var (
	detectIncludes []string
	detectExcludes []string
)

var detectCmd = &cobra.Command{
	Use:   "detect <name>",
	Short: "Detect languages by file extension; histogram with share",
	Long: `Per-language file histogram. Unlike summary (which reports the whole
repo), detect can be scoped with -i/-e — e.g. the language mix of just
src/ or everything except vendored code.`,
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

		files, err := treemap.Walk(p.Path, treemap.Filters{
			Includes: detectIncludes,
			Excludes: detectExcludes,
		})
		if err != nil {
			return err
		}
		langs := detect.Histogram(files)
		if langs == nil {
			langs = []detect.LangCount{}
		}
		return output.Write(cmd.OutOrStdout(), "detect", detectResponse{
			Name:       p.Name,
			Languages:  langs,
			TotalFiles: len(files),
		})
	},
}

func init() {
	detectCmd.Flags().StringSliceVarP(&detectIncludes, "include", "i", nil, "include only paths under this path (repeatable)")
	detectCmd.Flags().StringSliceVarP(&detectExcludes, "exclude", "e", nil, "exclude paths under this path (repeatable)")
	rootCmd.AddCommand(detectCmd)
}
