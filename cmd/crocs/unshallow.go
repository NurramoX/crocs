package main

import (
	"crocs/internal/output"
	"crocs/internal/vcs"

	"github.com/spf13/cobra"
)

type unshallowResponse struct {
	Repo    string `json:"repo"`
	Shallow bool   `json:"shallow"`
}

var unshallowCmd = &cobra.Command{
	Use:   "unshallow <name>",
	Short: "Convert a shallow/blobless clone to full history",
	Long: `Fetch the full history, all remote branches, and all tags for a shallow
clone. This is what makes branches/tags/log complete; it is not needed for
checkout or diff, which fetch the refs they need on demand. File contents
stay lazily fetched (the clone remains blobless), no working tree changes,
and symbol indexes are left as-is. Accepts any checkout handle of the repo.`,
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
		if err := vcs.Unshallow(ctx, p.RepoPath); err != nil {
			return err
		}
		if err := db.SetShallow(ctx, p.Repo, false); err != nil {
			return err
		}
		return output.Write(cmd.OutOrStdout(), "unshallow", unshallowResponse{Repo: p.Repo, Shallow: false})
	},
}

func init() {
	rootCmd.AddCommand(unshallowCmd)
}
