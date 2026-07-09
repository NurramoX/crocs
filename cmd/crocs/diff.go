package main

import (
	"fmt"

	"crocs/internal/output"
	"crocs/internal/vcs"

	"github.com/spf13/cobra"
)

type diffResponse struct {
	Name      string `json:"name"`
	From      string `json:"from,omitempty"`
	To        string `json:"to,omitempty"`
	Shallow   bool   `json:"shallow"`
	Hint      string `json:"hint,omitempty"`
	Diff      string `json:"diff"`
	Truncated bool   `json:"truncated"`
}

var (
	diffFrom  string
	diffTo    string
	diffStat  bool
	diffPaths []string
	diffMaxKB int
)

var diffCmd = &cobra.Command{
	Use:   "diff <name>",
	Short: "Show a diff for a tracked project",
	Long: `Show a unified diff. Ref semantics follow git: --from and --to together
diff the two refs; --from alone diffs that ref against the working tree;
neither shows unstaged changes. Use --stat for a summary and --path to
scope the diff. Output is capped at --max-kb (truncated flag set when hit).`,
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
		d, err := vcs.Diff(p.Path, vcs.DiffOptions{
			From:  diffFrom,
			To:    diffTo,
			Stat:  diffStat,
			Paths: diffPaths,
		})
		if err != nil {
			if p.Shallow && (diffFrom != "" || diffTo != "") {
				return fmt.Errorf("%w (project is a shallow clone — most refs are absent until `crocs unshallow %s`)", err, p.Name)
			}
			return err
		}

		truncated := false
		if maxBytes := diffMaxKB * 1024; maxBytes > 0 && len(d) > maxBytes {
			d = d[:maxBytes] + "\n[truncated by --max-kb]\n"
			truncated = true
		}
		hint := ""
		if p.Shallow {
			hint = fmt.Sprintf("shallow clone: only fetched refs exist; run `crocs unshallow %s` for full history", p.Name)
		}
		return output.Write(cmd.OutOrStdout(), "diff", diffResponse{
			Name:      p.Name,
			From:      diffFrom,
			To:        diffTo,
			Shallow:   p.Shallow,
			Hint:      hint,
			Diff:      d,
			Truncated: truncated,
		})
	},
}

func init() {
	diffCmd.Flags().StringVar(&diffFrom, "from", "", "from-ref (alone: diff this ref against the working tree)")
	diffCmd.Flags().StringVar(&diffTo, "to", "", "to-ref (requires refs present locally)")
	diffCmd.Flags().BoolVar(&diffStat, "stat", false, "show a diffstat summary instead of the full patch")
	diffCmd.Flags().StringArrayVar(&diffPaths, "path", nil, "limit the diff to this path (repeatable)")
	diffCmd.Flags().IntVar(&diffMaxKB, "max-kb", 256, "cap diff output at this many KB (0 = no cap)")
	rootCmd.AddCommand(diffCmd)
}
