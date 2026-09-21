package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"crocs/internal/project"
	"crocs/internal/registry"

	"github.com/spf13/cobra"
)

// projectJSON is the wire shape for a tracked repo: the clone that owns the
// git object store, plus every checkout of it. The first checkout is always
// the default one (id == repo name).
type projectJSON struct {
	Name      string         `json:"name"`
	URL       string         `json:"url"`
	Path      string         `json:"path"`
	Shallow   bool           `json:"shallow"`
	FetchedAt string         `json:"fetched_at"`
	Checkouts []checkoutJSON `json:"checkouts"`
}

// checkoutJSON is the wire shape for one working tree. Its id is the handle
// every other command takes as <name>: the bare repo name for the default
// checkout, "<repo>@<ref>" for a pinned version.
type checkoutJSON struct {
	ID        string  `json:"id"`
	Ref       string  `json:"ref,omitempty"`
	Kind      string  `json:"kind"`
	Commit    string  `json:"commit,omitempty"`
	Path      string  `json:"path"`
	UpdatedAt string  `json:"updated_at"`
	ParsedAt  *string `json:"parsed_at,omitempty"`
}

func checkoutToJSON(p registry.Project) checkoutJSON {
	out := checkoutJSON{
		ID:        p.Name,
		Ref:       p.Ref,
		Kind:      p.Kind,
		Commit:    p.Commit,
		Path:      p.Path,
		UpdatedAt: p.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if p.ParsedAt != nil {
		s := p.ParsedAt.UTC().Format(time.RFC3339)
		out.ParsedAt = &s
	}
	return out
}

// groupProjects folds the registry's flat checkout list (grouped by repo,
// default first) into one projectJSON per repo.
func groupProjects(all []registry.Project) []projectJSON {
	out := make([]projectJSON, 0)
	for _, p := range all {
		if n := len(out); n == 0 || out[n-1].Name != p.Repo {
			out = append(out, projectJSON{
				Name:      p.Repo,
				URL:       p.URL,
				Path:      p.RepoPath,
				Shallow:   p.Shallow,
				FetchedAt: p.FetchedAt.UTC().Format(time.RFC3339),
				Checkouts: make([]checkoutJSON, 0, 1),
			})
		}
		out[len(out)-1].Checkouts = append(out[len(out)-1].Checkouts, checkoutToJSON(p))
	}
	return out
}

// repoCheckouts returns every checkout of the given repo, default first.
func repoCheckouts(ctx context.Context, db *registry.DB, repo string) ([]registry.Project, error) {
	all, err := db.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	var out []registry.Project
	for _, p := range all {
		if p.Repo == repo {
			out = append(out, p)
		}
	}
	return out, nil
}

// cmdCtx returns a context for the command. Falls back to background if
// cobra hasn't wired one (which would only happen in tests without
// SetContext).
func cmdCtx(cmd *cobra.Command) context.Context {
	if c := cmd.Context(); c != nil {
		return c
	}
	return context.Background()
}

// openRegistry opens the registry. Failure here is fatal for callers.
func openRegistry(ctx context.Context) (*registry.DB, error) {
	return registry.Open(ctx)
}

// requireProject loads a checkout by handle or returns a clear "not
// tracked" error — with the command that would create it when the repo is
// tracked but the requested version isn't checked out yet.
func requireProject(ctx context.Context, db *registry.DB, name string) (registry.Project, error) {
	p, err := db.GetProject(ctx, name)
	if err == nil {
		return p, checkOnDisk(p)
	}
	if !errors.Is(err, registry.ErrNotFound) {
		return p, err
	}
	repo, ref := project.SplitID(name)
	if ref != "" {
		if _, rerr := db.GetProject(ctx, repo); rerr == nil {
			return registry.Project{}, fmt.Errorf("checkout %q does not exist (run `crocs checkout %s %s` to create it, or `crocs info %s` to see existing checkouts)", name, repo, ref, repo)
		}
	}
	return registry.Project{}, fmt.Errorf("project %q is not tracked (run `crocs list` to see tracked projects)", name)
}

// checkOnDisk turns a working tree that vanished behind crocs's back into
// an actionable error instead of raw lstat/chdir failures downstream.
func checkOnDisk(p registry.Project) error {
	if _, err := os.Stat(p.RepoPath); err != nil {
		return fmt.Errorf("clone directory for %q is missing (%s); run `crocs remove %s` and fetch it again", p.Repo, p.RepoPath, p.Repo)
	}
	if _, err := os.Stat(p.Path); err != nil {
		return fmt.Errorf("working tree for %q is missing (%s); run `crocs checkout %s %s` to recreate it", p.Name, p.Path, p.Repo, p.Ref)
	}
	return nil
}

// shallowHint is the standard hint VCS commands attach on shallow clones;
// missing names what the shallow clone lacks for that command.
func shallowHint(name, missing string) string {
	return fmt.Sprintf("shallow clone: %s; run `crocs unshallow %s` to fetch the rest", missing, name)
}

// capList truncates list to limit entries (0 = no cap) and reports whether
// it did. Shared --limit handling for branches/tags.
func capList[T any](list []T, limit int) ([]T, bool) {
	if limit > 0 && len(list) > limit {
		return list[:limit], true
	}
	return list, false
}
