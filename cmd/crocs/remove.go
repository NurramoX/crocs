package main

import (
	"fmt"
	"os"

	"crocs/internal/output"
	"crocs/internal/project"
	"crocs/internal/registry"
	"crocs/internal/vcs"

	"github.com/spf13/cobra"
)

type removeResponse struct {
	Repo        string         `json:"repo"`
	RepoRemoved bool           `json:"repo_removed"`
	Removed     []checkoutJSON `json:"removed"`
	DiskErrors  []string       `json:"disk_errors,omitempty"`
}

var removeCmd = &cobra.Command{
	Use:     "remove <name>",
	Aliases: []string{"rm"},
	Short:   "Delete a checkout (<name>@<ref>) or a whole repo with all its checkouts",
	Long: `Delete the working tree(s) from disk and untrack them. Given a pinned
checkout handle (<name>@<ref>) only that worktree goes; given the bare repo
name, every checkout and the clone itself are removed.`,
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
		targets := []registry.Project{p}
		if p.IsDefault() {
			if targets, err = repoCheckouts(ctx, db, p.Repo); err != nil {
				return err
			}
		}

		// Best-effort disk delete first, versioned worktrees before the clone
		// that owns their metadata. If a dir is gone or read-only, we still
		// drop the registry rows so the user isn't stuck with a phantom entry;
		// failures are surfaced in the response instead of aborting.
		//
		// A registry-stored path is only trusted if it lives under the
		// managed projects root — a corrupted or hand-edited row must never
		// turn remove into an arbitrary `rm -rf`.
		resp := removeResponse{Repo: p.Repo, RepoRemoved: p.IsDefault()}
		for i := len(targets) - 1; i >= 0; i-- {
			t := targets[i]
			resp.Removed = append(resp.Removed, checkoutToJSON(t))
			var diskErr string
			if within, err := project.UnderProjectsRoot(t.Path); err != nil {
				diskErr = "verify clone path: " + err.Error()
			} else if !within {
				diskErr = fmt.Sprintf("refusing to delete %s: outside the managed projects root", t.Path)
			} else if t.IsDefault() {
				if err := os.RemoveAll(t.Path); err != nil {
					diskErr = err.Error()
				}
			} else if err := vcs.RemoveWorktree(ctx, t.RepoPath, t.Path); err != nil {
				diskErr = err.Error()
			}
			if diskErr != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "crocs: disk delete failed:", diskErr)
				resp.DiskErrors = append(resp.DiskErrors, diskErr)
			}
		}
		if p.IsDefault() {
			err = db.DeleteRepo(ctx, p.Repo)
		} else {
			err = db.DeleteCheckout(ctx, p.Name)
		}
		if err != nil {
			return fmt.Errorf("untrack %s: %w", p.Name, err)
		}
		return output.Write(cmd.OutOrStdout(), "remove", resp)
	},
}

func init() {
	rootCmd.AddCommand(removeCmd)
}
