package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"crocs/internal/output"
	"crocs/internal/project"
	"crocs/internal/registry"
	"crocs/internal/vcs"

	"github.com/spf13/cobra"
)

type fetchResponse struct {
	Project      projectJSON `json:"project"`
	Engine       string      `json:"engine"`
	SymbolCount  int         `json:"symbol_count"`
	RefError     string      `json:"ref_error,omitempty"`
	ReindexError string      `json:"reindex_error,omitempty"`
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
git CLI is available); use --full for a complete history. The clone is the
repo's default checkout; pin other versions beside it with
` + "`crocs checkout <name> <ref>`" + `.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmdCtx(cmd)
		url := args[0]

		name := fetchName
		if name == "" {
			name = project.NameFromURL(url)
			if name == "" {
				return fmt.Errorf("could not infer a project name from URL %q; pass --name", url)
			}
		}
		if err := project.ValidateName(name); err != nil {
			return err
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
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("stat clone destination %s: %w", dest, err)
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

		resp := fetchResponse{Engine: res.Engine}
		ref, refErr := vcs.CurrentRef(dest)
		if refErr != nil {
			// Non-fatal: we still track the repo, just without a ref.
			ref = ""
			resp.RefError = refErr.Error()
		}
		commit, _ := vcs.HeadHash(dest)

		now := time.Now().UTC()
		if err := db.InsertRepo(ctx, registry.Repo{
			Name: name, URL: url, Path: dest, Shallow: res.Shallow, FetchedAt: now,
		}); err == nil {
			err = db.InsertCheckout(ctx, registry.Checkout{
				ID: name, Repo: name, Ref: ref, Kind: string(vcs.HeadKind(dest)), Commit: commit, Path: dest, UpdatedAt: now,
			})
			if err != nil {
				_ = db.DeleteRepo(ctx, name)
			}
		}
		if err != nil {
			// Don't strand the fresh clone on disk: an orphaned directory
			// blocks the next fetch of the same name.
			if rmErr := os.RemoveAll(dest); rmErr != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "crocs: cleanup of failed fetch left", dest, "behind:", rmErr)
			}
			return fmt.Errorf("register project: %w", err)
		}

		p, err := db.GetProject(ctx, name)
		if err != nil {
			return err
		}
		// fetch eagerly parses and persists the symbol index at clone time.
		nSym, err := reparseProject(ctx, db, p)
		if err != nil {
			// Symbol indexing failure is non-fatal: the clone is on disk and
			// registered, so grep/read-files still work.
			resp.ReindexError = err.Error()
		}
		// Reload to pick up parsed_at updated by ReplaceSymbols.
		if refreshed, err := db.GetProject(ctx, name); err == nil {
			p = refreshed
		}
		resp.Project = groupProjects([]registry.Project{p})[0]
		resp.SymbolCount = nSym
		return output.Write(cmd.OutOrStdout(), "fetch", resp)
	},
}

func init() {
	fetchCmd.Flags().StringVar(&fetchName, "name", "", "override the project name (default: derived from URL)")
	fetchCmd.Flags().BoolVar(&fetchFull, "full", false, "force a full (non-shallow, non-blobless) clone")
	fetchCmd.Flags().StringVar(&fetchRef, "ref", "", "branch or tag for the default checkout (default: remote HEAD)")
	rootCmd.AddCommand(fetchCmd)
}
