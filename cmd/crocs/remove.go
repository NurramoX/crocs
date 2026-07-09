package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"crocs/internal/output"
	"crocs/internal/project"

	"github.com/spf13/cobra"
)

type removeResponse struct {
	Removed   projectJSON `json:"removed"`
	DiskError string      `json:"disk_error,omitempty"`
}

var removeCmd = &cobra.Command{
	Use:     "remove <name>",
	Aliases: []string{"rm"},
	Short:   "Untrack a project and delete its clone from disk",
	Args:    cobra.ExactArgs(1),
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

		// Best-effort disk delete first. If the dir is gone or read-only, we
		// still drop the registry row so the user isn't stuck with a phantom
		// entry; the failure is surfaced in the response instead of aborting.
		//
		// The registry-stored path is only trusted if it lives under the
		// managed projects root — a corrupted or hand-edited row must never
		// turn remove into an arbitrary `rm -rf`.
		diskErr := ""
		if within, err := underProjectsRoot(p.Path); err != nil {
			diskErr = "verify clone path: " + err.Error()
		} else if !within {
			diskErr = fmt.Sprintf("refusing to delete %s: outside the managed projects root", p.Path)
		} else if err := os.RemoveAll(p.Path); err != nil {
			diskErr = err.Error()
		}
		if diskErr != "" {
			fmt.Fprintln(cmd.ErrOrStderr(), "crocs: disk delete failed:", diskErr)
		}
		if err := db.DeleteProject(ctx, p.Name); err != nil {
			return fmt.Errorf("untrack %s: %w", p.Name, err)
		}
		return output.Write(cmd.OutOrStdout(), "remove", removeResponse{
			Removed:   projectToJSON(p),
			DiskError: diskErr,
		})
	},
}

// underProjectsRoot reports whether path is a strict descendant of the
// managed projects root.
func underProjectsRoot(path string) (bool, error) {
	root, err := project.ProjectsRoot()
	if err != nil {
		return false, err
	}
	rel, err := filepath.Rel(root, filepath.Clean(path))
	if err != nil {
		return false, err
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false, nil
	}
	return true, nil
}

func init() {
	rootCmd.AddCommand(removeCmd)
}
