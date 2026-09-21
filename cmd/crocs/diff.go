package main

import (
	"crocs/internal/output"
	"crocs/internal/project"
	"crocs/internal/vcs"

	"github.com/spf13/cobra"
)

type diffResponse struct {
	Name       string `json:"name"`
	From       string `json:"from,omitempty"`
	FromCommit string `json:"from_commit,omitempty"`
	To         string `json:"to,omitempty"`
	ToCommit   string `json:"to_commit,omitempty"`
	Diff       string `json:"diff"`
	Truncated  bool   `json:"truncated"`
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
diff the two refs; --from alone diffs that ref against the checkout's working
tree; neither shows unstaged changes. Refs (branches, tags, commits) are
fetched from origin on demand, so no unshallow is needed. Use --stat for a
summary and --path to scope the diff. Output is capped at --max-kb
(truncated flag set when hit).`,
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
		resp := diffResponse{Name: p.Name, From: diffFrom, To: diffTo}
		opts := vcs.DiffOptions{Stat: diffStat, Paths: diffPaths}
		for _, ref := range []string{diffFrom, diffTo} {
			if ref != "" {
				if err := project.ValidateRef(ref); err != nil {
					return err
				}
			}
		}
		if diffFrom != "" {
			r, err := vcs.ResolveRef(ctx, p.RepoPath, diffFrom, vcs.ResolveOptions{Shallow: p.Shallow})
			if err != nil {
				return err
			}
			opts.From, resp.FromCommit = r.Hash, r.Hash
		}
		if diffTo != "" {
			r, err := vcs.ResolveRef(ctx, p.RepoPath, diffTo, vcs.ResolveOptions{Shallow: p.Shallow})
			if err != nil {
				return err
			}
			opts.To, resp.ToCommit = r.Hash, r.Hash
		}
		d, err := vcs.Diff(ctx, p.Path, opts)
		if err != nil {
			return err
		}
		if maxBytes := diffMaxKB * 1024; maxBytes > 0 && len(d) > maxBytes {
			d = d[:maxBytes] + "\n[truncated by --max-kb]\n"
			resp.Truncated = true
		}
		resp.Diff = d
		return output.Write(cmd.OutOrStdout(), "diff", resp)
	},
}

func init() {
	diffCmd.Flags().StringVar(&diffFrom, "from", "", "from-ref (alone: diff this ref against the working tree)")
	diffCmd.Flags().StringVar(&diffTo, "to", "", "to-ref (fetched on demand if absent locally)")
	diffCmd.Flags().BoolVar(&diffStat, "stat", false, "show a diffstat summary instead of the full patch")
	diffCmd.Flags().StringArrayVar(&diffPaths, "path", nil, "limit the diff to this path (repeatable)")
	diffCmd.Flags().IntVar(&diffMaxKB, "max-kb", 256, "cap diff output at this many KB (0 = no cap)")
	rootCmd.AddCommand(diffCmd)
}
