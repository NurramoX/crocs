package main

import (
	"crocs/internal/detect"
	"crocs/internal/output"
	"crocs/internal/treemap"

	"github.com/spf13/cobra"
)

type detectResponse struct {
	Name      string             `json:"name"`
	Languages []detect.LangCount `json:"languages"`
	TotalFiles int               `json:"total_files"`
}

var detectCmd = &cobra.Command{
	Use:   "detect <name>",
	Short: "Detect languages by file extension; histogram with share",
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

		files, err := treemap.Walk(p.Path, treemap.Filters{})
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
	rootCmd.AddCommand(detectCmd)
}
