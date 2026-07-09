package main

import (
	"crocs/internal/grepx"
	"crocs/internal/output"

	"github.com/spf13/cobra"
)

type grepResponse struct {
	Name      string        `json:"name"`
	Pattern   string        `json:"pattern"`
	Matches   []grepx.Match `json:"matches"`
	Truncated bool          `json:"truncated"`
	Engine    string        `json:"engine"`
}

var (
	grepIncludes   []string
	grepExcludes   []string
	grepContext    int
	grepMaxResults int
	grepMultiline  bool
)

var grepCmd = &cobra.Command{
	Use:   "grep <name> <pattern>",
	Short: "Search project files for a regex pattern",
	Args:  cobra.ExactArgs(2),
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
		pattern := args[1]

		res, err := grepx.Run(ctx, p.Path, grepx.Options{
			Pattern:    pattern,
			Includes:   grepIncludes,
			Excludes:   grepExcludes,
			Context:    grepContext,
			MaxResults: grepMaxResults,
			Multiline:  grepMultiline,
		})
		if err != nil {
			return err
		}
		if res.Matches == nil {
			res.Matches = []grepx.Match{}
		}
		return output.Write(cmd.OutOrStdout(), "grep", grepResponse{
			Name:      p.Name,
			Pattern:   pattern,
			Matches:   res.Matches,
			Truncated: res.Truncated,
			Engine:    res.Engine,
		})
	},
}

func init() {
	grepCmd.Flags().StringSliceVarP(&grepIncludes, "include", "i", nil, "include only paths matching prefix/glob (repeatable)")
	grepCmd.Flags().StringSliceVarP(&grepExcludes, "exclude", "e", nil, "exclude paths matching prefix/glob (repeatable)")
	grepCmd.Flags().IntVarP(&grepContext, "context", "C", 0, "lines of context around each match")
	grepCmd.Flags().IntVar(&grepMaxResults, "max-results", grepx.DefaultMaxResults, "cap total matches returned")
	grepCmd.Flags().BoolVarP(&grepMultiline, "multiline", "M", false, "pattern may span lines")
	rootCmd.AddCommand(grepCmd)
}
