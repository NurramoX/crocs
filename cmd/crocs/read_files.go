package main

import (
	"crocs/internal/files"

	"github.com/spf13/cobra"
)

var (
	rfLines   string
	rfMaxSize int
)

var readFilesCmd = &cobra.Command{
	Use:   "read-files <name> <path>...",
	Short: "Bundle files from a project in XML-tagged format",
	Long: `Bundle one or more files into a single XML document for an agent to read.
The output is XML, not JSON — PLAN.md §2 Breaking Change #2: read-files is the
sole carve-out from the JSON-default contract because file bodies are easier to
read and debug as plain text than as JSON-escaped strings.`,
	Args: cobra.MinimumNArgs(2),
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
		paths := args[1:]

		return files.Bundle(cmd.OutOrStdout(), files.Request{
			Root:      p.Path,
			Paths:     paths,
			LineRange: rfLines,
			MaxSizeKB: rfMaxSize,
		})
	},
}

func init() {
	readFilesCmd.Flags().StringVar(&rfLines, "lines", "", "line range, e.g. 200-300 or just 250")
	readFilesCmd.Flags().IntVar(&rfMaxSize, "max-size", files.DefaultMaxSizeKB, "skip files larger than this many KB")
	rootCmd.AddCommand(readFilesCmd)
}
