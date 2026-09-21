package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"crocs/internal/output"
	"crocs/internal/project"
	"crocs/internal/registry"
	"crocs/internal/vcs"

	"github.com/spf13/cobra"
)

type checkoutResponse struct {
	Repo         string       `json:"repo"`
	Checkout     checkoutJSON `json:"checkout"`
	Existing     bool         `json:"existing"`
	SymbolCount  int          `json:"symbol_count"`
	ReindexError string       `json:"reindex_error,omitempty"`
}

var checkoutCmd = &cobra.Command{
	Use:   "checkout <name> <ref>",
	Short: "Pin a branch, tag, or commit as its own checkout (<name>@<ref>)",
	Long: `Create a second working tree of a tracked repo at <ref> — a git worktree
that shares the repo's object store — and index its symbols. The result is
addressed as <name>@<ref> by every other command, so several versions can be
queried side by side and the default checkout is never disturbed.

The ref is fetched from origin on demand (at depth 1 on shallow clones), so
no unshallow is needed. <ref> may be a branch, a tag, or a commit hash.
The handle is the literal ref you pass: "main", "v1.8.0", and the commit
hash they point to are three distinct checkouts. Creating a checkout that
already exists is a no-op that returns it (existing: true); a single
"<name>@<ref>" argument is accepted as shorthand.`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmdCtx(cmd)
		repo, ref := project.SplitID(args[0])
		if len(args) == 2 {
			if ref != "" {
				return fmt.Errorf("pass the repo name, not a checkout handle: `crocs checkout %s %s`", repo, args[1])
			}
			ref = args[1]
		}
		if ref == "" {
			return errors.New("checkout needs a ref: `crocs checkout <name> <ref>`")
		}
		if err := project.ValidateRef(ref); err != nil {
			return err
		}

		db, err := openRegistry(ctx)
		if err != nil {
			return err
		}
		defer db.Close()

		base, err := requireProject(ctx, db, repo)
		if err != nil {
			return err
		}
		id := project.CheckoutID(repo, ref)
		dest, err := project.CheckoutPath(repo, ref)
		if err != nil {
			return err
		}
		// A tracked checkout whose directory vanished behind crocs's back is
		// recreated rather than reported as existing.
		recreate := false
		if existing, err := db.GetProject(ctx, id); err == nil {
			if _, serr := os.Stat(existing.Path); serr == nil {
				resp := checkoutResponse{Repo: repo, Checkout: checkoutToJSON(existing), Existing: true}
				if n, err := db.CountSymbols(ctx, id); err == nil {
					resp.SymbolCount = n
				}
				return output.Write(cmd.OutOrStdout(), "checkout", resp)
			}
			recreate, dest = true, existing.Path
		} else if !errors.Is(err, registry.ErrNotFound) {
			return err
		}
		if !recreate {
			if _, err := os.Stat(dest); err == nil {
				siblings, _ := repoCheckouts(ctx, db, repo)
				for _, s := range siblings {
					if strings.EqualFold(s.Path, dest) {
						return fmt.Errorf("checkout destination %s is already used by %q (refs that differ only in case or in '/' vs '-' share a directory); use that handle instead", dest, s.Name)
					}
				}
				return fmt.Errorf("checkout destination %s already exists on disk but is not tracked; move it away, or `crocs remove %s` and re-fetch if it is a stale clone", dest, repo)
			} else if !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("stat checkout destination %s: %w", dest, err)
			}
		}

		res, err := vcs.ResolveRef(ctx, base.RepoPath, ref, vcs.ResolveOptions{Shallow: base.Shallow})
		if err != nil {
			return err
		}
		if err := vcs.AddWorktree(ctx, base.RepoPath, dest, res.Hash); err != nil {
			return err
		}
		if recreate {
			err = db.SetSnapshot(ctx, id, ref, string(res.Kind), res.Hash, time.Now().UTC())
		} else {
			err = db.InsertCheckout(ctx, registry.Checkout{
				ID: id, Repo: repo, Ref: ref, Kind: string(res.Kind), Commit: res.Hash, Path: dest, UpdatedAt: time.Now().UTC(),
			})
		}
		if err != nil {
			_ = vcs.RemoveWorktree(ctx, base.RepoPath, dest)
			return fmt.Errorf("register checkout: %w", err)
		}

		p, err := db.GetProject(ctx, id)
		if err != nil {
			return err
		}
		resp := checkoutResponse{Repo: repo}
		nSym, perr := reparseProject(ctx, db, p)
		if perr != nil {
			resp.ReindexError = perr.Error()
		}
		if refreshed, err := db.GetProject(ctx, id); err == nil {
			p = refreshed
		}
		resp.Checkout = checkoutToJSON(p)
		resp.SymbolCount = nSym
		return output.Write(cmd.OutOrStdout(), "checkout", resp)
	},
}

func init() {
	rootCmd.AddCommand(checkoutCmd)
}
