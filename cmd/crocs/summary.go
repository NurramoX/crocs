package main

import (
	"crocs/internal/detect"
	"crocs/internal/output"
	"crocs/internal/summary"
	"crocs/internal/treemap"

	"github.com/spf13/cobra"
)

type summaryReadme struct {
	Path    string `json:"path"`
	Excerpt string `json:"excerpt"`
}

type summaryResponse struct {
	Project        projectJSON        `json:"project"`
	TotalFiles     int                `json:"total_files"`
	Languages      []detect.LangCount `json:"languages"`
	ImportantFiles []string           `json:"important_files"`
	Readme         *summaryReadme     `json:"readme,omitempty"`
}

var summaryCmd = &cobra.Command{
	Use:   "summary <name>",
	Short: "Project shape in one call: README excerpt + manifests + languages",
	Long: `Return the project's shape — README excerpt (badges filtered out),
important manifest/config files (go.mod, package.json, Dockerfile, …), and
the per-language file histogram — in a single JSON envelope. This is the
one cheap call an agent makes to orient on an unfamiliar repo.`,
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

		files, err := treemap.Walk(p.Path, treemap.Filters{})
		if err != nil {
			return err
		}
		langs := detect.Histogram(files)
		if langs == nil {
			langs = []detect.LangCount{}
		}

		important, err := summary.ImportantFiles(p.Path)
		if err != nil {
			return err
		}
		if important == nil {
			important = []string{}
		}

		readme, err := summary.ReadReadme(p.Path)
		if err != nil {
			return err
		}
		var readmeOut *summaryReadme
		if readme.Path != "" {
			readmeOut = &summaryReadme{Path: readme.Path, Excerpt: readme.Excerpt}
		}

		return output.Write(cmd.OutOrStdout(), "summary", summaryResponse{
			Project:        projectToJSON(p),
			TotalFiles:     len(files),
			Languages:      langs,
			ImportantFiles: important,
			Readme:         readmeOut,
		})
	},
}

func init() {
	rootCmd.AddCommand(summaryCmd)
}
