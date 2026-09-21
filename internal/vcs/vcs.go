// Package vcs wraps git operations. Clones default to the git CLI with
// blobless+shallow filtering; go-git is the in-process fallback and the
// engine for read-only inspection (branches, tags, and the no-git log
// path). diff, unshallow, and everything around versioned checkouts
// (ResolveRef, worktrees) require the CLI. The clone path is the single
// largest perf dial in the tool — the git CLI benched ~10× faster than go-git
// on large repos.
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

// openRepo opens dir with go-git. Linked worktrees (versioned checkouts)
// keep HEAD locally but share refs and objects through .git/commondir;
// without that option go-git sees an empty ref list there.
func openRepo(dir string) (*gogit.Repository, error) {
	return gogit.PlainOpenWithOptions(dir, &gogit.PlainOpenOptions{EnableDotGitCommonDir: true})
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
	args := []string{"-c", "advice.detachedHead=false", "clone"}
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
	repo, err := openRepo(repoDir)
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
	repo, err := openRepo(repoDir)
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
	repo, err := openRepo(repoDir)
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
	repo, err := openRepo(repoDir)
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
	repo, err := openRepo(repoDir)
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

// RefKind classifies what a ref resolved to on the remote.
type RefKind string

const (
	RefTag    RefKind = "tag"
	RefBranch RefKind = "branch"
	RefCommit RefKind = "commit"
)

// ResolvedRef is the outcome of ResolveRef: the commit a ref names, and
// how it got there.
type ResolvedRef struct {
	Ref  string
	Kind RefKind
	Hash string // full commit hash
}

// ResolveOptions tunes ResolveRef.
type ResolveOptions struct {
	// Shallow keeps on-demand fetches at depth 1 so pinning a version of a
	// shallow clone costs one commit's worth of history, not the whole
	// graph. Set from the repo's shallow flag.
	Shallow bool
	// Refresh asks the remote even when the ref already resolves locally,
	// so a moved branch (or re-pointed tag) is picked up. Used by update.
	Refresh bool
}

// ResolveRef turns a branch, tag, or commit hash into a commit that exists
// in repoDir's object store, fetching it from origin on demand. A shallow
// single-branch clone therefore never needs unshallowing just to look at
// another version. Requires the git CLI.
//
// Order: a tag or hash already present locally wins without a network
// round-trip (unless Refresh); otherwise origin is asked what the name is
// (one ls-remote), the matching ref is fetched, and the result is read back
// from the local ref. Names that origin doesn't know fall back to local
// resolution (HEAD, origin/x, abbreviated hashes) and, for full hashes, to
// fetching the commit by id.
func ResolveRef(ctx context.Context, repoDir, ref string, opts ResolveOptions) (ResolvedRef, error) {
	if ref == "" {
		return ResolvedRef{}, errors.New("vcs.ResolveRef: ref is required")
	}
	if !HasGit() {
		return ResolvedRef{}, errors.New("vcs.ResolveRef: requires git CLI on $PATH")
	}
	if !opts.Refresh {
		if h, ok := localCommit(ctx, repoDir, "refs/tags/"+ref); ok {
			return ResolvedRef{Ref: ref, Kind: RefTag, Hash: h}, nil
		}
		if isHex(ref) {
			if h, ok := localCommit(ctx, repoDir, ref); ok {
				return ResolvedRef{Ref: ref, Kind: RefCommit, Hash: h}, nil
			}
		}
	}

	remote, lsErr := lsRemote(ctx, repoDir, ref)
	if lsErr == nil && remote.Kind != "" {
		local := "refs/tags/" + ref
		refspec := "+refs/tags/" + ref + ":" + local
		if remote.Kind == RefBranch {
			local = "refs/remotes/origin/" + ref
			refspec = "+refs/heads/" + ref + ":" + local
		}
		if h, ok := localCommit(ctx, repoDir, local); !ok || h != remote.Hash {
			// The commit may already be here under another name (a release
			// tag on main's tip, say): point the ref at it instead of
			// fetching, which on a shallow repo would re-record the commit
			// as a history boundary.
			if _, have := localCommit(ctx, repoDir, remote.Hash); have {
				if err := runGit(ctx, repoDir, "update-ref", local, remote.Hash); err != nil {
					return ResolvedRef{}, err
				}
			} else if err := fetch(ctx, repoDir, opts.Shallow, refspec); err != nil {
				return ResolvedRef{}, err
			}
		}
		h, ok := localCommit(ctx, repoDir, local)
		if !ok {
			return ResolvedRef{}, fmt.Errorf("fetched %s but %s does not resolve to a commit", ref, local)
		}
		return ResolvedRef{Ref: ref, Kind: remote.Kind, Hash: h}, nil
	}

	// origin doesn't publish the name (or is unreachable): local-only refs
	// and abbreviated hashes still resolve; full hashes can be fetched by id.
	if h, ok := localCommit(ctx, repoDir, ref); ok {
		return ResolvedRef{Ref: ref, Kind: RefCommit, Hash: h}, nil
	}
	if lsErr != nil {
		return ResolvedRef{}, fmt.Errorf("resolve %q: not present locally and origin is unreachable: %w", ref, lsErr)
	}
	if isHex(ref) && (len(ref) == 40 || len(ref) == 64) {
		if err := fetch(ctx, repoDir, opts.Shallow, ref); err != nil {
			return ResolvedRef{}, fmt.Errorf("resolve %q: not a branch or tag on origin, and fetching it as a commit failed: %w", ref, err)
		}
		if h, ok := localCommit(ctx, repoDir, ref); ok {
			return ResolvedRef{Ref: ref, Kind: RefCommit, Hash: h}, nil
		}
	}
	return ResolvedRef{}, fmt.Errorf("resolve %q: not a branch, tag, or commit on origin (abbreviated hashes only resolve once the commit is local)", ref)
}

// lsRemote asks origin whether ref is a tag or a branch and which commit
// it points at. Kind is empty when neither exists. Tags win over same-named
// branches, matching git's own rev-parse precedence. For annotated tags the
// peeled "^{}" line is used so Hash is the commit, comparable with what
// localCommit returns — otherwise every refresh would look like a change.
func lsRemote(ctx context.Context, repoDir, ref string) (ResolvedRef, error) {
	out, err := gitOutput(ctx, repoDir, "ls-remote", "origin", "refs/tags/"+ref, "refs/tags/"+ref+"^{}", "refs/heads/"+ref)
	if err != nil {
		return ResolvedRef{}, err
	}
	var tag, peeled, branch string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		hash, name, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		switch name {
		case "refs/tags/" + ref:
			tag = hash
		case "refs/tags/" + ref + "^{}":
			peeled = hash
		case "refs/heads/" + ref:
			branch = hash
		}
	}
	switch {
	case peeled != "":
		return ResolvedRef{Ref: ref, Kind: RefTag, Hash: peeled}, nil
	case tag != "":
		return ResolvedRef{Ref: ref, Kind: RefTag, Hash: tag}, nil
	case branch != "":
		return ResolvedRef{Ref: ref, Kind: RefBranch, Hash: branch}, nil
	}
	return ResolvedRef{}, nil
}

// localCommit resolves rev to a full commit hash in repoDir, peeling tags.
func localCommit(ctx context.Context, repoDir, rev string) (string, bool) {
	out, err := gitOutput(ctx, repoDir, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if err != nil {
		return "", false
	}
	h := strings.TrimSpace(out)
	return h, h != ""
}

func fetch(ctx context.Context, repoDir string, shallow bool, refspec string) error {
	args := []string{"fetch", "--no-tags"}
	if shallow {
		args = append(args, "--depth=1")
	}
	return runGit(ctx, repoDir, append(args, "origin", refspec)...)
}

func isHex(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !('0' <= r && r <= '9' || 'a' <= r && r <= 'f') {
			return false
		}
	}
	return true
}

// AddWorktree creates a detached git worktree of repoDir at dest, checked
// out at hash. On a blobless clone this fetches the tree's blobs from the
// promisor remote. Requires the git CLI.
func AddWorktree(ctx context.Context, repoDir, dest, hash string) error {
	if !HasGit() {
		return errors.New("vcs.AddWorktree: requires git CLI on $PATH")
	}
	if _, err := os.Stat(dest); err == nil {
		return fmt.Errorf("vcs.AddWorktree: %s already exists", dest)
	}
	// A worktree whose directory was deleted by hand is still registered;
	// prune so re-adding at the same path is not refused.
	_ = runGit(ctx, repoDir, "worktree", "prune")
	return runGit(ctx, repoDir, "-c", "advice.detachedHead=false", "worktree", "add", "--detach", dest, hash)
}

// RemoveWorktree deletes the worktree at dest and forgets it in repoDir.
// The directory is removed even when git no longer recognizes it (e.g. the
// main clone was already deleted), so callers can rely on dest being gone.
func RemoveWorktree(ctx context.Context, repoDir, dest string) error {
	if HasGit() {
		if _, err := os.Stat(repoDir); err == nil {
			_ = runGit(ctx, repoDir, "worktree", "remove", "--force", dest)
			_ = runGit(ctx, repoDir, "worktree", "prune")
		}
	}
	return os.RemoveAll(dest)
}

// CheckoutDetached moves dir's working tree to hash with a detached HEAD.
// Used to advance a versioned checkout after its ref was re-resolved.
func CheckoutDetached(ctx context.Context, dir, hash string) error {
	if !HasGit() {
		return errors.New("vcs.CheckoutDetached: requires git CLI on $PATH")
	}
	return runGit(ctx, dir, "-c", "advice.detachedHead=false", "checkout", "--detach", hash)
}

// HeadKind classifies what dir's HEAD is: RefBranch when on a local
// branch, RefTag when detached at a commit some tag points to, RefCommit
// otherwise. Pairs with CurrentRef to describe a fresh clone.
func HeadKind(dir string) RefKind {
	if _, ok := OnBranch(dir); ok {
		return RefBranch
	}
	if ref, err := CurrentRef(dir); err == nil {
		if h, herr := HeadHash(dir); herr == nil && !strings.HasPrefix(h, ref) {
			return RefTag
		}
	}
	return RefCommit
}

// OnBranch reports the local branch dir's HEAD is on, or false when HEAD
// is detached (a tag or commit checkout).
func OnBranch(dir string) (string, bool) {
	repo, err := openRepo(dir)
	if err != nil {
		return "", false
	}
	head, err := repo.Head()
	if err != nil || !head.Name().IsBranch() {
		return "", false
	}
	return head.Name().Short(), true
}

// Pull brings repoDir's checked-out branch to origin's tip. crocs clones
// never carry local commits, so this is a mirror, not a merge: fetch the
// branch (deepening from the existing shallow boundary) and hard-reset to
// it. That sidesteps merge-base lookups, which fail once a sibling
// checkout's depth-1 fetch has recorded the same tip as a shallow root.
func Pull(ctx context.Context, repoDir, branch string) error {
	if branch == "" {
		return errors.New("vcs.Pull: branch is required")
	}
	if HasGit() {
		remote := "refs/remotes/origin/" + branch
		if err := runGit(ctx, repoDir, "fetch", "--no-tags", "origin", "+refs/heads/"+branch+":"+remote); err != nil {
			return err
		}
		return runGit(ctx, repoDir, "reset", "--hard", "--quiet", remote)
	}
	repo, err := openRepo(repoDir)
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

// Unshallow fetches a shallow clone's full history, every remote branch,
// and all tags. The blob filter stays in place: a blobless clone keeps
// lazily fetching file contents from the promisor remote as checkouts and
// diffs need them, which is exactly what makes deep history affordable.
// Dropping the promisor config without refetching would leave the object
// store unable to materialize any tree it hasn't seen yet. Requires the
// git CLI; go-git lacks a clean unshallow primitive.
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
	// --force: a tag that was re-pointed upstream (and already fetched by a
	// pinned checkout's update) must not make unshallow fail forever.
	if err := runGit(ctx, repoDir, "fetch", "--unshallow", "--force", "--tags", "origin"); err != nil {
		// "--unshallow on a complete repository does not make sense" is fine —
		// but still fetch, so the widened refspec takes effect on such repos.
		if !strings.Contains(err.Error(), "does not make sense") {
			return err
		}
		return runGit(ctx, repoDir, "fetch", "--force", "--tags", "origin")
	}
	return nil
}

// RemoteURL returns the URL of the "origin" remote in repoDir.
func RemoteURL(repoDir string) (string, error) {
	repo, err := openRepo(repoDir)
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
	_, err := gitOutput(ctx, repoDir, args...)
	return err
}

// gitOutput runs git in repoDir and returns its stdout; on failure the
// error carries stderr.
func gitOutput(ctx context.Context, repoDir string, args ...string) (string, error) {
	full := append([]string{"-C", repoDir}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	// Pin the message locale: callers (notably Unshallow) match on English
	// substrings of git's output.
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
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
