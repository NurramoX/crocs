// Package vcs wraps git operations. Clones default to the git CLI with
// blobless+shallow filtering (PLAN.md §1 "The git decision, expanded");
// go-git is the in-process fallback and the engine for read-only inspection
// (branches, tags, and the no-git log path). diff and unshallow require the
// CLI. The clone path is the single largest perf dial in the tool — Phase 0
// benched git CLI at ~10× faster on large repos.
package vcs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// CloneOptions controls a single clone.
type CloneOptions struct {
	URL  string
	Dest string // target directory; must not already exist
	Ref  string // optional branch/tag to check out; empty = remote HEAD
	Full bool   // disable shallow + blobless filtering (slower, complete history)

	// Progress, if non-nil, is the writer git CLI / go-git stream their
	// progress text into. Typically os.Stderr in interactive use; nil for
	// JSON-only callers.
	Progress io.Writer
}

// CloneResult summarizes what happened.
type CloneResult struct {
	// Shallow reports whether the resulting clone is blobless/shallow.
	// Always false when Full was true; true when the CLI path applied
	// --filter=blob:none. The go-git fallback path is always full → false.
	Shallow bool
	// Engine reports which backend was used: "git" or "go-git".
	Engine string
}

// HasGit returns true if a usable `git` binary is on $PATH.
func HasGit() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// Clone clones the repo. With git on $PATH it uses the CLI with blobless+
// shallow filters (skippable via opts.Full); otherwise it falls back to
// go-git's PlainCloneContext (full clone only — go-git's partial-clone
// support is too thin to rely on).
func Clone(ctx context.Context, opts CloneOptions) (CloneResult, error) {
	if opts.URL == "" {
		return CloneResult{}, errors.New("vcs.Clone: URL is required")
	}
	if opts.Dest == "" {
		return CloneResult{}, errors.New("vcs.Clone: Dest is required")
	}
	if _, err := os.Stat(opts.Dest); err == nil {
		return CloneResult{}, fmt.Errorf("vcs.Clone: %s already exists", opts.Dest)
	}

	if HasGit() {
		shallow, err := cloneCLI(ctx, opts)
		if err != nil {
			return CloneResult{}, err
		}
		return CloneResult{Shallow: shallow, Engine: "git"}, nil
	}
	if err := cloneGoGit(ctx, opts); err != nil {
		return CloneResult{}, err
	}
	return CloneResult{Shallow: false, Engine: "go-git"}, nil
}

func cloneCLI(ctx context.Context, opts CloneOptions) (bool, error) {
	args := []string{"clone"}
	shallow := !opts.Full
	if shallow {
		args = append(args, "--depth=1", "--filter=blob:none")
	}
	if opts.Ref != "" {
		args = append(args, "--branch", opts.Ref)
	}
	args = append(args, opts.URL, opts.Dest)

	cmd := exec.CommandContext(ctx, "git", args...)
	if opts.Progress != nil {
		cmd.Stdout = opts.Progress
		cmd.Stderr = opts.Progress
	} else {
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
	}
	if err := cmd.Run(); err != nil {
		return false, fmt.Errorf("git clone: %w", err)
	}
	return shallow, nil
}

func cloneGoGit(ctx context.Context, opts CloneOptions) error {
	clone := func(ref plumbing.ReferenceName) error {
		gco := &gogit.CloneOptions{URL: opts.URL}
		if opts.Progress != nil {
			gco.Progress = opts.Progress
		}
		if ref != "" {
			gco.ReferenceName = ref
			gco.SingleBranch = true
		}
		_, err := gogit.PlainCloneContext(ctx, opts.Dest, false, gco)
		return err
	}
	if opts.Ref == "" {
		if err := clone(""); err != nil {
			return fmt.Errorf("go-git clone: %w", err)
		}
		return nil
	}
	// --ref promises "branch or tag"; go-git needs a fully-qualified ref
	// name, so try branch first, then tag. A failed attempt may leave a
	// partial dest behind — clear it before retrying.
	branchErr := clone(plumbing.NewBranchReferenceName(opts.Ref))
	if branchErr == nil {
		return nil
	}
	_ = os.RemoveAll(opts.Dest)
	if err := clone(plumbing.NewTagReferenceName(opts.Ref)); err != nil {
		_ = os.RemoveAll(opts.Dest)
		return fmt.Errorf("go-git clone: ref %q matched neither a branch (%v) nor a tag: %w", opts.Ref, branchErr, err)
	}
	return nil
}

// CurrentRef returns the branch or tag name currently checked out in
// repoDir. On a detached HEAD with no matching tag, returns the 12-char
// short hash.
func CurrentRef(repoDir string) (string, error) {
	repo, err := gogit.PlainOpen(repoDir)
	if err != nil {
		return "", fmt.Errorf("open repo: %w", err)
	}
	head, err := repo.Head()
	if err != nil {
		return "", fmt.Errorf("read HEAD: %w", err)
	}
	if head.Name().IsBranch() {
		return head.Name().Short(), nil
	}
	tags, err := repo.Tags()
	if err == nil {
		hash := head.Hash()
		var match string
		_ = tags.ForEach(func(ref *plumbing.Reference) error {
			// Annotated tags: ref.Hash() is the tag *object*, not the commit
			// it points to — peel it or annotated tags never match HEAD.
			h := ref.Hash()
			if tagObj, terr := repo.TagObject(h); terr == nil {
				h = tagObj.Target
			}
			if h == hash {
				match = ref.Name().Short()
				return io.EOF
			}
			return nil
		})
		if match != "" {
			return match, nil
		}
	}
	return head.Hash().String()[:12], nil
}

// HeadHash returns the full commit hash HEAD points at. Used for cheap
// did-anything-change checks around pull.
func HeadHash(repoDir string) (string, error) {
	repo, err := gogit.PlainOpen(repoDir)
	if err != nil {
		return "", fmt.Errorf("open repo: %w", err)
	}
	head, err := repo.Head()
	if err != nil {
		return "", fmt.Errorf("read HEAD: %w", err)
	}
	return head.Hash().String(), nil
}

// Branch is one branch reference, local or remote.
type Branch struct {
	Name   string `json:"name"`
	Remote bool   `json:"remote"`
	Hash   string `json:"hash"`
}

// Branches lists every branch in repoDir, sorted by name. Both local and
// remote-tracking branches are included.
func Branches(repoDir string) ([]Branch, error) {
	repo, err := gogit.PlainOpen(repoDir)
	if err != nil {
		return nil, err
	}
	refs, err := repo.References()
	if err != nil {
		return nil, err
	}
	var out []Branch
	err = refs.ForEach(func(ref *plumbing.Reference) error {
		name := ref.Name()
		switch {
		case name.IsBranch():
			out = append(out, Branch{Name: name.Short(), Hash: ref.Hash().String()})
		case strings.HasPrefix(string(name), "refs/remotes/"):
			short := strings.TrimPrefix(string(name), "refs/remotes/")
			if strings.HasSuffix(short, "/HEAD") {
				return nil
			}
			out = append(out, Branch{Name: short, Remote: true, Hash: ref.Hash().String()})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Tag is one annotated/lightweight tag reference.
type Tag struct {
	Name string `json:"name"`
	Hash string `json:"hash"`
}

// Tags lists every tag in repoDir, sorted by name.
func Tags(repoDir string) ([]Tag, error) {
	repo, err := gogit.PlainOpen(repoDir)
	if err != nil {
		return nil, err
	}
	tags, err := repo.Tags()
	if err != nil {
		return nil, err
	}
	var out []Tag
	err = tags.ForEach(func(ref *plumbing.Reference) error {
		// Peel annotated tags to the commit they point at, so Hash means the
		// same thing for lightweight and annotated tags (and matches what
		// branches reports).
		h := ref.Hash()
		if tagObj, terr := repo.TagObject(h); terr == nil {
			h = tagObj.Target
		}
		out = append(out, Tag{Name: ref.Name().Short(), Hash: h.String()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// LogEntry is one commit returned by Log.
type LogEntry struct {
	Hash    string `json:"hash"`
	Author  string `json:"author"`
	Email   string `json:"email"`
	Date    string `json:"date"` // RFC3339 UTC
	Subject string `json:"subject"`
}

// Log returns up to `limit` commits from HEAD, newest first. limit=0 means
// "no cap"; the caller is responsible for not asking for millions.
//
// Uses `git log` when the CLI is present — go-git's commit iterator
// dereferences tree/blob objects in some paths, which fails on partial
// (blobless) clones. The CLI handles partial clones natively.
func Log(repoDir string, limit int) ([]LogEntry, error) {
	if HasGit() {
		return logCLI(repoDir, limit)
	}
	return logGoGit(repoDir, limit)
}

// gitLogSep is an ASCII unit separator chosen because it is virtually never
// found in author names, emails, dates, or commit subjects.
const gitLogSep = "\x1f"

func logCLI(repoDir string, limit int) ([]LogEntry, error) {
	format := "%H" + gitLogSep + "%an" + gitLogSep + "%ae" + gitLogSep + "%aI" + gitLogSep + "%s"
	args := []string{"-C", repoDir, "log", "--no-decorate", "--pretty=format:" + format}
	if limit > 0 {
		args = append(args, fmt.Sprintf("-n%d", limit))
	}
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, fmt.Errorf("git log: %w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("git log: %w", err)
	}
	var entries []LogEntry
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, gitLogSep, 5)
		if len(parts) != 5 {
			continue
		}
		entries = append(entries, LogEntry{
			Hash:    parts[0],
			Author:  parts[1],
			Email:   parts[2],
			Date:    normalizeDate(parts[3]),
			Subject: parts[4],
		})
	}
	return entries, nil
}

// normalizeDate converts git's %aI output (author-local offset, e.g.
// "2024-05-01T12:00:00+02:00") to the UTC RFC3339 shape LogEntry documents.
// Both log engines emit the same date format this way. Unparseable input
// passes through untouched rather than being dropped.
func normalizeDate(s string) string {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	return t.UTC().Format(time.RFC3339)
}

func logGoGit(repoDir string, limit int) ([]LogEntry, error) {
	repo, err := gogit.PlainOpen(repoDir)
	if err != nil {
		return nil, err
	}
	iter, err := repo.Log(&gogit.LogOptions{})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	var out []LogEntry
	count := 0
	err = iter.ForEach(func(c *object.Commit) error {
		out = append(out, LogEntry{
			Hash:    c.Hash.String(),
			Author:  c.Author.Name,
			Email:   c.Author.Email,
			Date:    c.Author.When.UTC().Format(time.RFC3339),
			Subject: firstLine(c.Message),
		})
		count++
		if limit > 0 && count >= limit {
			return errStopIter
		}
		return nil
	})
	if err != nil && !errors.Is(err, errStopIter) {
		return nil, err
	}
	return out, nil
}

// DiffOptions narrows a Diff call.
type DiffOptions struct {
	From  string   // from-ref; empty = working tree base (see below)
	To    string   // to-ref; empty = working tree
	Stat  bool     // --stat summary instead of a full patch
	Paths []string // limit the diff to these paths
}

// Diff returns a unified diff in repoDir. Ref semantics follow git:
// From+To = From..To, From alone = From against the working tree, To alone
// = To against the working tree, neither = unstaged changes.
func Diff(ctx context.Context, repoDir string, opts DiffOptions) (string, error) {
	if !HasGit() {
		return "", errors.New("vcs.Diff: requires git CLI on $PATH")
	}
	args := []string{"-C", repoDir, "diff"}
	if opts.Stat {
		args = append(args, "--stat")
	}
	switch {
	case opts.From != "" && opts.To != "":
		args = append(args, opts.From+".."+opts.To)
	case opts.From != "":
		args = append(args, opts.From)
	case opts.To != "":
		args = append(args, opts.To)
	}
	if len(opts.Paths) > 0 {
		args = append(args, "--")
		args = append(args, opts.Paths...)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return "", fmt.Errorf("git diff: %w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("git diff: %w", err)
	}
	return string(out), nil
}

// Checkout switches repoDir to the given ref. The ref must already exist
// locally — no fetch is attempted (on the default shallow single-branch
// clone that means most refs need `unshallow` first). Falls back to go-git
// Worktree.Checkout without a git CLI, trying branch, tag, then raw hash.
func Checkout(ctx context.Context, repoDir, ref string) error {
	if ref == "" {
		return errors.New("vcs.Checkout: ref is required")
	}
	if HasGit() {
		return runGit(ctx, repoDir, "checkout", ref)
	}
	repo, err := gogit.PlainOpen(repoDir)
	if err != nil {
		return err
	}
	wt, err := repo.Worktree()
	if err != nil {
		return err
	}
	branchErr := wt.Checkout(&gogit.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName(ref),
	})
	if branchErr == nil {
		return nil
	}
	if err := wt.Checkout(&gogit.CheckoutOptions{
		Branch: plumbing.NewTagReferenceName(ref),
	}); err == nil {
		return nil
	}
	if plumbing.IsHash(ref) {
		return wt.Checkout(&gogit.CheckoutOptions{Hash: plumbing.NewHash(ref)})
	}
	return branchErr
}

// Pull fetches and fast-forwards repoDir.
func Pull(ctx context.Context, repoDir string) error {
	if HasGit() {
		return runGit(ctx, repoDir, "pull", "--ff-only")
	}
	repo, err := gogit.PlainOpen(repoDir)
	if err != nil {
		return err
	}
	wt, err := repo.Worktree()
	if err != nil {
		return err
	}
	if err := wt.PullContext(ctx, &gogit.PullOptions{RemoteName: "origin"}); err != nil && !errors.Is(err, gogit.NoErrAlreadyUpToDate) {
		return err
	}
	return nil
}

// Unshallow converts a shallow/blobless clone to a full one. Requires git
// CLI; go-git lacks a clean unshallow primitive.
func Unshallow(ctx context.Context, repoDir string) error {
	if !HasGit() {
		return errors.New("vcs.Unshallow: requires git CLI on $PATH")
	}
	// A --depth=1 clone is also single-branch: its fetch refspec names only
	// the cloned branch, so `fetch --unshallow` alone would deepen history
	// without ever surfacing the other remote branches (or tags reachable
	// only from them). Widen the refspec first so the fetch below delivers
	// what the shallow-clone hints promise.
	if err := runGit(ctx, repoDir, "config", "remote.origin.fetch",
		"+refs/heads/*:refs/remotes/origin/*"); err != nil {
		return err
	}
	if err := runGit(ctx, repoDir, "fetch", "--unshallow", "--tags", "origin"); err != nil {
		// "--unshallow on a complete repository does not make sense" is fine —
		// but still fetch, so the widened refspec takes effect on such repos.
		if !strings.Contains(err.Error(), "does not make sense") {
			return err
		}
		if err := runGit(ctx, repoDir, "fetch", "--tags", "origin"); err != nil {
			return err
		}
	}
	// Drop the blob filter so subsequent fetches pull full content. Both
	// configs may be absent on a non-partial clone — ignore "not found" errors.
	_ = runGit(ctx, repoDir, "config", "--unset", "remote.origin.promisor")
	_ = runGit(ctx, repoDir, "config", "--unset", "remote.origin.partialclonefilter")
	return nil
}

// RemoteURL returns the URL of the "origin" remote in repoDir.
func RemoteURL(repoDir string) (string, error) {
	repo, err := gogit.PlainOpen(repoDir)
	if err != nil {
		return "", err
	}
	remote, err := repo.Remote("origin")
	if err != nil {
		return "", err
	}
	urls := remote.Config().URLs
	if len(urls) == 0 {
		return "", errors.New("origin has no URLs")
	}
	return urls[0], nil
}

func runGit(ctx context.Context, repoDir string, args ...string) error {
	full := append([]string{"-C", repoDir}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	// Pin the message locale: callers (notably Unshallow) match on English
	// substrings of git's output.
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// errStopIter is a sentinel used to early-terminate go-git iterators when
// a caller-supplied limit has been hit. It is never returned to callers.
var errStopIter = errors.New("vcs: stop iteration sentinel")
