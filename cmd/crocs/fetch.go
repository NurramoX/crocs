package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"crocs/internal/output"
	"crocs/internal/project"
	"crocs/internal/registry"
	"crocs/internal/vcs"

	"github.com/spf13/cobra"
)

type fetchResponse struct {
	Project     projectJSON `json:"project"`
	Engine      string      `json:"engine"`
	SymbolCount int         `json:"symbol_count"`
}

var (
	fetchName string
	fetchFull bool
	fetchRef  string
)

var fetchCmd = &cobra.Command{
	Use:   "fetch <url>",
	Short: "Clone a git repo and track it",
	Long: `Clone a git repo to $XDG_DATA_HOME/crocs/projects/<name> and register it
in the local registry. By default the clone is shallow + blobless (when the
git CLI is available); use --full for a complete history.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmdCtx(cmd)
		url := args[0]

		name := fetchName
		if name == "" {
			name = project.NameFromURL(url)
		}
		if name == "" {
			return fmt.Errorf("could not infer a project name from URL %q; pass --name", url)
		}

		db, err := openRegistry(ctx)
		if err != nil {
			return err
		}
		defer db.Close()

		if _, err := db.GetProject(ctx, name); err == nil {
			return fmt.Errorf("project %q already tracked (use `crocs remove %s` first, or pass --name)", name, name)
		} else if !errors.Is(err, registry.ErrNotFound) {
			return err
		}

		dest, err := project.ProjectPath(name)
		if err != nil {
			return err
		}
		if _, err := os.Stat(dest); err == nil {
			return fmt.Errorf("clone destination %s already exists on disk; pass --name or remove the directory first", dest)
		}

		res, err := vcs.Clone(ctx, vcs.CloneOptions{
			URL:      url,
			Dest:     dest,
			Ref:      fetchRef,
			Full:     fetchFull,
			Progress: cmd.ErrOrStderr(),
		})
		if err != nil {
			return fmt.Errorf("clone %s: %w", url, err)
		}

		ref, refErr := vcs.CurrentRef(dest)
		if refErr != nil {
			// Non-fatal: we still tracked the repo. Surface in the response.
			ref = ""
		}

		p := registry.Project{
			Name:       name,
			URL:        url,
			Path:       dest,
			DefaultRef: ref,
			Shallow:    res.Shallow,
			FetchedAt:  time.Now().UTC(),
		}
		if err := db.InsertProject(ctx, p); err != nil {
			return fmt.Errorf("register project: %w", err)
		}

		// PLAN.md §2 #4: fetch eagerly parses and persists the symbol index
		// at clone time. Budget ≤60s on the largest target repos.
		nSym, err := reparseProject(ctx, db, p)
		if err != nil {
			// Symbol indexing failure is non-fatal: the clone is on disk and
			// registered, so grep/read-files still work. Surface in stderr so
			// the user knows the index is empty.
			fmt.Fprintln(cmd.ErrOrStderr(), "crocs: symbol index build failed:", err)
		}

		// Reload to pick up parsed_at updated by ReplaceSymbols.
		if refreshed, err := db.GetProject(ctx, p.Name); err == nil {
			p = refreshed
		}

		return output.Write(cmd.OutOrStdout(), "fetch", fetchResponse{
			Project:     projectToJSON(p),
			Engine:      res.Engine,
			SymbolCount: nSym,
		})
	},
}

func init() {
	fetchCmd.Flags().StringVar(&fetchName, "name", "", "override the project name (default: derived from URL)")
	fetchCmd.Flags().BoolVar(&fetchFull, "full", false, "force a full (non-shallow, non-blobless) clone")
	fetchCmd.Flags().StringVar(&fetchRef, "ref", "", "branch or tag to check out (default: remote HEAD)")
	rootCmd.AddCommand(fetchCmd)
}
