package main

import (
	"crocs/internal/output"
	"crocs/internal/vcs"

	"github.com/spf13/cobra"
)

type logResponse struct {
	Name    string         `json:"name"`
	Shallow bool           `json:"shallow"`
	Hint    string         `json:"hint,omitempty"`
	Commits []vcs.LogEntry `json:"commits"`
}

var logLimit int

var logCmd = &cobra.Command{
	Use:   "log <name>",
	Short: "Show recent commits for a tracked project",
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
		commits, err := vcs.Log(p.Path, logLimit)
		if err != nil {
			return err
		}
		if commits == nil {
			commits = []vcs.LogEntry{}
		}
		hint := ""
		if p.Shallow {
			hint = shallowHint(p.Name, "history is truncated at fetch depth")
		}
		return output.Write(cmd.OutOrStdout(), "log", logResponse{
			Name:    p.Name,
			Shallow: p.Shallow,
			Hint:    hint,
			Commits: commits,
		})
	},
}

func init() {
	logCmd.Flags().IntVarP(&logLimit, "limit", "n", 20, "max commits to return (0 = no cap)")
	rootCmd.AddCommand(logCmd)
}
