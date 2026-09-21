package vcs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// initRepo creates a git repo with two commits, a lightweight tag on the
// first and an annotated tag on the second. Returns the repo dir and the
// two commit hashes.
func initRepo(t *testing.T) (dir, first, second string) {
	t.Helper()
	if !HasGit() {
		t.Skip("git CLI not available")
	}
	dir = t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
			"LC_ALL=C",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "first")
	first = git("rev-parse", "HEAD")
	git("tag", "v1-light")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("commit", "-am", "second")
	second = git("rev-parse", "HEAD")
	git("tag", "-a", "v2-annotated", "-m", "release two")
	return dir, first, second
}

func TestTagsPeelAnnotated(t *testing.T) {
	dir, first, second := initRepo(t)
	tags, err := Tags(dir)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]string{}
	for _, tag := range tags {
		byName[tag.Name] = tag.Hash
	}
	if byName["v1-light"] != first {
		t.Errorf("lightweight tag hash = %s, want commit %s", byName["v1-light"], first)
	}
	if byName["v2-annotated"] != second {
		t.Errorf("annotated tag hash = %s, want peeled commit %s", byName["v2-annotated"], second)
	}
}

func TestCurrentRefAnnotatedTag(t *testing.T) {
	dir, _, second := initRepo(t)
	if err := CheckoutDetached(context.Background(), dir, second); err != nil {
		t.Fatal(err)
	}
	ref, err := CurrentRef(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ref != "v2-annotated" {
		t.Errorf("CurrentRef on a detached HEAD at an annotated tag = %q, want tag name", ref)
	}
	if _, ok := OnBranch(dir); ok {
		t.Error("OnBranch must be false on a detached HEAD")
	}
}

func TestHeadHash(t *testing.T) {
	dir, _, second := initRepo(t)
	h, err := HeadHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	if h != second {
		t.Errorf("HeadHash = %s, want %s", h, second)
	}
}

func TestDiffFromOnly(t *testing.T) {
	dir, _, _ := initRepo(t)
	// --from alone must diff the ref against the working tree — this was
	// silently ignored before.
	d, err := Diff(t.Context(), dir, DiffOptions{From: "v1-light"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d, "+two") {
		t.Errorf("from-only diff missing the change:\n%s", d)
	}
	// from..to
	d, err = Diff(t.Context(), dir, DiffOptions{From: "v1-light", To: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d, "+two") {
		t.Errorf("from..to diff missing the change:\n%s", d)
	}
	// --stat
	d, err = Diff(t.Context(), dir, DiffOptions{From: "v1-light", Stat: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d, "1 file changed") {
		t.Errorf("stat diff missing summary:\n%s", d)
	}
	// path scoping to a file the change doesn't touch
	d, err = Diff(t.Context(), dir, DiffOptions{From: "v1-light", Paths: []string{"nope.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(d) != "" {
		t.Errorf("path-scoped diff should be empty:\n%s", d)
	}
}

func TestDiffBadRefSurfacesStderr(t *testing.T) {
	dir, _, _ := initRepo(t)
	_, err := Diff(t.Context(), dir, DiffOptions{From: "no-such-ref", To: "HEAD"})
	if err == nil {
		t.Fatal("expected error for unknown ref")
	}
	if !strings.Contains(err.Error(), "no-such-ref") && !strings.Contains(err.Error(), "bad revision") {
		t.Errorf("error should carry git's stderr, got: %v", err)
	}
}

func TestLogDatesAreUTCRFC3339(t *testing.T) {
	dir, _, _ := initRepo(t)
	entries, err := Log(dir, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d commits, want 2", len(entries))
	}
	for _, e := range entries {
		ts, err := time.Parse(time.RFC3339, e.Date)
		if err != nil {
			t.Errorf("date %q is not RFC3339: %v", e.Date, err)
			continue
		}
		if !strings.HasSuffix(e.Date, "Z") {
			t.Errorf("date %q not normalized to UTC", e.Date)
		}
		if ts.After(time.Now().Add(time.Hour)) {
			t.Errorf("date %q in the future", e.Date)
		}
	}
	if entries[0].Subject != "second" {
		t.Errorf("newest-first ordering broken: %q", entries[0].Subject)
	}
}

func TestNormalizeDate(t *testing.T) {
	if got := normalizeDate("2024-05-01T12:00:00+02:00"); got != "2024-05-01T10:00:00Z" {
		t.Errorf("normalizeDate = %q", got)
	}
	if got := normalizeDate("garbage"); got != "garbage" {
		t.Errorf("unparseable input should pass through, got %q", got)
	}
}

func TestBranchesListsLocal(t *testing.T) {
	dir, _, _ := initRepo(t)
	branches, err := Branches(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(branches) != 1 || branches[0].Name != "main" {
		t.Errorf("branches = %+v, want just main", branches)
	}
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(branches[0].Hash) {
		t.Errorf("branch hash %q not a full sha", branches[0].Hash)
	}
}

// shallowClone makes a depth-1 clone of remote (via file:// so --depth is
// honored) and returns its path.
func shallowClone(t *testing.T, remote string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "clone")
	cmd := exec.Command("git", "clone", "--quiet", "--depth=1", "file://"+remote, dir)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("shallow clone: %v\n%s", err, out)
	}
	return dir
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		"LC_ALL=C",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestResolveRefFetchesTagOnDemandAndWorktree(t *testing.T) {
	remote, first, second := initRepo(t)
	clone := shallowClone(t, remote)
	ctx := context.Background()

	// The shallow clone knows nothing about v1-light: it must be fetched.
	if _, ok := localCommit(ctx, clone, "refs/tags/v1-light"); ok {
		t.Fatal("fixture: tag should be absent from a depth-1 clone")
	}
	res, err := ResolveRef(ctx, clone, "v1-light", ResolveOptions{Shallow: true})
	if err != nil {
		t.Fatalf("ResolveRef tag: %v", err)
	}
	if res.Kind != RefTag || res.Hash != first {
		t.Errorf("ResolveRef(v1-light) = %+v, want tag %s", res, first)
	}
	// Still shallow: the on-demand fetch must not have pulled full history.
	if depth := gitIn(t, clone, "rev-list", "--count", "--all"); depth != "2" {
		t.Errorf("rev-list --count --all = %s, want 2 (two shallow tips)", depth)
	}
	// Second resolve hits the local tag (no network needed).
	again, err := ResolveRef(ctx, clone, "v1-light", ResolveOptions{Shallow: true})
	if err != nil || again.Hash != first {
		t.Errorf("second ResolveRef = %+v, %v", again, err)
	}
	// Annotated tags peel to the commit.
	ann, err := ResolveRef(ctx, clone, "v2-annotated", ResolveOptions{Shallow: true})
	if err != nil || ann.Kind != RefTag || ann.Hash != second {
		t.Errorf("ResolveRef(v2-annotated) = %+v, %v; want peeled %s", ann, err, second)
	}

	wt := filepath.Join(t.TempDir(), "clone@v1-light")
	if err := AddWorktree(ctx, clone, wt, first); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(wt, "a.txt")); err != nil || string(b) != "one\n" {
		t.Errorf("worktree content = %q, %v; want first commit's file", b, err)
	}
	// go-git must be able to read a linked worktree (HEAD in .git/worktrees,
	// refs in the common dir) — every inspection command relies on it.
	if h, err := HeadHash(wt); err != nil || h != first {
		t.Errorf("HeadHash(worktree) = %s, %v; want %s", h, err, first)
	}
	if ref, err := CurrentRef(wt); err != nil || ref != "v1-light" {
		t.Errorf("CurrentRef(worktree) = %s, %v; want v1-light", ref, err)
	}
	if _, ok := OnBranch(wt); ok {
		t.Error("worktree must be detached")
	}
	if b, ok := OnBranch(clone); !ok || b != "main" {
		t.Errorf("OnBranch(clone) = %q, %v; want main", b, ok)
	}
	tags, err := Tags(wt)
	if err != nil || len(tags) == 0 {
		t.Errorf("Tags(worktree) = %v, %v; want shared refs visible", tags, err)
	}
	if err := AddWorktree(ctx, clone, wt, first); err == nil {
		t.Error("AddWorktree onto an existing dir must fail")
	}

	// Advancing a versioned checkout: detached move to another commit.
	if err := CheckoutDetached(ctx, wt, second); err != nil {
		t.Fatalf("CheckoutDetached: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(wt, "a.txt")); string(b) != "one\ntwo\n" {
		t.Errorf("after CheckoutDetached content = %q", b)
	}

	if err := RemoveWorktree(ctx, clone, wt); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("worktree dir still present: %v", err)
	}
	if list := gitIn(t, clone, "worktree", "list"); strings.Contains(list, "clone@v1-light") {
		t.Errorf("worktree not forgotten:\n%s", list)
	}
}

func TestResolveRefBranchFollowsRemote(t *testing.T) {
	remote, _, _ := initRepo(t)
	clone := shallowClone(t, remote)
	ctx := context.Background()

	gitIn(t, remote, "checkout", "-q", "-b", "feature")
	if err := os.WriteFile(filepath.Join(remote, "b.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, remote, "add", ".")
	gitIn(t, remote, "commit", "-q", "-m", "third")
	third := gitIn(t, remote, "rev-parse", "HEAD")

	res, err := ResolveRef(ctx, clone, "feature", ResolveOptions{Shallow: true})
	if err != nil {
		t.Fatalf("ResolveRef branch: %v", err)
	}
	if res.Kind != RefBranch || res.Hash != third {
		t.Errorf("ResolveRef(feature) = %+v, want branch %s", res, third)
	}

	// The branch moves on the remote: even without Refresh a branch name
	// is re-asked from origin, so the new tip is returned.
	gitIn(t, remote, "commit", "-q", "--allow-empty", "-m", "fourth")
	fourth := gitIn(t, remote, "rev-parse", "HEAD")
	res, err = ResolveRef(ctx, clone, "feature", ResolveOptions{Shallow: true})
	if err != nil || res.Hash != fourth {
		t.Errorf("ResolveRef(feature) after remote move = %+v, %v; want %s", res, err, fourth)
	}
}

func TestResolveRefTagRefresh(t *testing.T) {
	remote, first, second := initRepo(t)
	clone := shallowClone(t, remote)
	ctx := context.Background()

	if res, err := ResolveRef(ctx, clone, "v1-light", ResolveOptions{Shallow: true}); err != nil || res.Hash != first {
		t.Fatalf("initial resolve = %+v, %v", res, err)
	}
	// Re-point the tag upstream. Without Refresh the local tag wins; with
	// Refresh the moved tag is picked up.
	gitIn(t, remote, "tag", "-f", "v1-light", second)
	if res, _ := ResolveRef(ctx, clone, "v1-light", ResolveOptions{Shallow: true}); res.Hash != first {
		t.Errorf("without Refresh = %s, want cached %s", res.Hash, first)
	}
	res, err := ResolveRef(ctx, clone, "v1-light", ResolveOptions{Shallow: true, Refresh: true})
	if err != nil || res.Hash != second {
		t.Errorf("with Refresh = %+v, %v; want %s", res, err, second)
	}
}

func TestResolveRefCommitAndUnknown(t *testing.T) {
	remote, first, second := initRepo(t)
	gitIn(t, remote, "config", "uploadpack.allowAnySHA1InWant", "true")
	clone := shallowClone(t, remote)
	ctx := context.Background()

	// Abbreviated hash of a local commit resolves without network.
	res, err := ResolveRef(ctx, clone, second[:8], ResolveOptions{Shallow: true})
	if err != nil || res.Kind != RefCommit || res.Hash != second {
		t.Errorf("abbreviated local hash = %+v, %v", res, err)
	}
	// Full hash of a commit the shallow clone lacks is fetched by id.
	res, err = ResolveRef(ctx, clone, first, ResolveOptions{Shallow: true})
	if err != nil || res.Kind != RefCommit || res.Hash != first {
		t.Errorf("full remote hash = %+v, %v", res, err)
	}
	// Local-only names (HEAD, origin/main) resolve too.
	res, err = ResolveRef(ctx, clone, "origin/main", ResolveOptions{Shallow: true})
	if err != nil || res.Hash != second {
		t.Errorf("origin/main = %+v, %v", res, err)
	}
	_, err = ResolveRef(ctx, clone, "no-such-ref", ResolveOptions{Shallow: true})
	if err == nil || !strings.Contains(err.Error(), "no-such-ref") {
		t.Errorf("unknown ref error = %v", err)
	}
	_, err = ResolveRef(ctx, clone, "abcdef1", ResolveOptions{Shallow: true})
	if err == nil || !strings.Contains(err.Error(), "abbreviated") {
		t.Errorf("unknown abbreviated hash error should explain itself, got %v", err)
	}
}

func TestUnshallowKeepsBlobsFetchable(t *testing.T) {
	remote, first, _ := initRepo(t)
	gitIn(t, remote, "config", "uploadpack.allowFilter", "true")
	clone := filepath.Join(t.TempDir(), "clone")
	cmd := exec.Command("git", "clone", "--quiet", "--depth=1", "--filter=blob:none", "file://"+remote, clone)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("blobless clone: %v\n%s", err, out)
	}
	if gitIn(t, clone, "config", "remote.origin.promisor") != "true" {
		t.Skip("local transport did not produce a promisor clone")
	}
	ctx := context.Background()
	if err := Unshallow(ctx, clone); err != nil {
		t.Fatalf("Unshallow: %v", err)
	}
	if n := gitIn(t, clone, "rev-list", "--count", "HEAD"); n != "2" {
		t.Errorf("history depth after unshallow = %s, want 2", n)
	}
	// The first commit's blob was never fetched; materializing its tree in
	// a worktree must still work — i.e. the promisor config must survive.
	wt := filepath.Join(t.TempDir(), "wt")
	if err := AddWorktree(ctx, clone, wt, first); err != nil {
		t.Fatalf("AddWorktree after unshallow: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(wt, "a.txt")); err != nil || string(b) != "one\n" {
		t.Errorf("worktree after unshallow has content %q, %v; blobs no longer fetchable", b, err)
	}
}

// TestPullSurvivesSiblingDepthOneFetch reproduces the sequence that broke
// the old pull --ff-only: a pinned checkout fetches main's future tip at
// depth 1 (recording it as a shallow root), then the default checkout is
// updated. Mirror semantics must still land on the new tip.
func TestPullSurvivesSiblingDepthOneFetch(t *testing.T) {
	remote, _, second := initRepo(t)
	clone := shallowClone(t, remote)
	ctx := context.Background()

	gitIn(t, remote, "commit", "-q", "--allow-empty", "-m", "third")
	third := gitIn(t, remote, "rev-parse", "HEAD")
	gitIn(t, remote, "tag", "-f", "v1-light", third)

	if res, err := ResolveRef(ctx, clone, "v1-light", ResolveOptions{Shallow: true, Refresh: true}); err != nil || res.Hash != third {
		t.Fatalf("refresh tag = %+v, %v", res, err)
	}
	if h, _ := HeadHash(clone); h != second {
		t.Fatalf("fixture: clone HEAD moved unexpectedly to %s", h)
	}
	if err := Pull(ctx, clone, "main"); err != nil {
		t.Fatalf("Pull after sibling depth-1 fetch: %v", err)
	}
	if h, _ := HeadHash(clone); h != third {
		t.Errorf("HEAD after Pull = %s, want %s", h, third)
	}
	if b, ok := OnBranch(clone); !ok || b != "main" {
		t.Errorf("Pull must keep the branch checked out, got %q %v", b, ok)
	}
}

// TestResolveRefReusesLocalCommit: a tag pointing at a commit the clone
// already has must not be fetched again at depth 1 (which would record a
// second shallow root for the same tip).
func TestResolveRefReusesLocalCommit(t *testing.T) {
	remote, _, second := initRepo(t)
	clone := shallowClone(t, remote)
	ctx := context.Background()
	gitIn(t, remote, "tag", "v-tip", second)
	before := gitIn(t, clone, "rev-list", "--count", "--all")
	res, err := ResolveRef(ctx, clone, "v-tip", ResolveOptions{Shallow: true})
	if err != nil || res.Kind != RefTag || res.Hash != second {
		t.Fatalf("ResolveRef(v-tip) = %+v, %v", res, err)
	}
	if after := gitIn(t, clone, "rev-list", "--count", "--all"); after != before {
		t.Errorf("commit count changed %s → %s: the tip was re-fetched", before, after)
	}
	if h, ok := localCommit(ctx, clone, "refs/tags/v-tip"); !ok || h != second {
		t.Errorf("local tag ref not created: %s %v", h, ok)
	}
}

func TestUnshallowForcesMovedTags(t *testing.T) {
	remote, first, second := initRepo(t)
	clone := shallowClone(t, remote)
	ctx := context.Background()
	if _, err := ResolveRef(ctx, clone, "v1-light", ResolveOptions{Shallow: true}); err != nil {
		t.Fatal(err)
	}
	gitIn(t, remote, "tag", "-f", "v1-light", second)
	if err := Unshallow(ctx, clone); err != nil {
		t.Fatalf("Unshallow with a re-pointed tag: %v", err)
	}
	if h, _ := localCommit(ctx, clone, "refs/tags/v1-light"); h != second {
		t.Errorf("tag after unshallow = %s, want moved %s (was %s)", h, second, first)
	}
	if _, err := os.Stat(filepath.Join(clone, ".git", "shallow")); !os.IsNotExist(err) {
		t.Errorf("clone still shallow: %v", err)
	}
}

func TestLsRemotePeelsAnnotatedTags(t *testing.T) {
	remote, first, second := initRepo(t)
	clone := shallowClone(t, remote)
	ctx := context.Background()
	ann, err := lsRemote(ctx, clone, "v2-annotated")
	if err != nil || ann.Kind != RefTag || ann.Hash != second {
		t.Errorf("lsRemote(annotated) = %+v, %v; want peeled commit %s", ann, err, second)
	}
	light, err := lsRemote(ctx, clone, "v1-light")
	if err != nil || light.Kind != RefTag || light.Hash != first {
		t.Errorf("lsRemote(lightweight) = %+v, %v; want %s", light, err, first)
	}
	branch, err := lsRemote(ctx, clone, "main")
	if err != nil || branch.Kind != RefBranch || branch.Hash != second {
		t.Errorf("lsRemote(branch) = %+v, %v", branch, err)
	}
	none, err := lsRemote(ctx, clone, "nope")
	if err != nil || none.Kind != "" {
		t.Errorf("lsRemote(unknown) = %+v, %v; want empty kind", none, err)
	}
}
