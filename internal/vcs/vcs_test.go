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
	dir, _, _ := initRepo(t)
	if err := Checkout(context.Background(), dir, "v2-annotated"); err != nil {
		t.Fatal(err)
	}
	ref, err := CurrentRef(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ref != "v2-annotated" {
		t.Errorf("CurrentRef after annotated-tag checkout = %q, want tag name", ref)
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
	d, err := Diff(dir, DiffOptions{From: "v1-light"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d, "+two") {
		t.Errorf("from-only diff missing the change:\n%s", d)
	}
	// from..to
	d, err = Diff(dir, DiffOptions{From: "v1-light", To: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d, "+two") {
		t.Errorf("from..to diff missing the change:\n%s", d)
	}
	// --stat
	d, err = Diff(dir, DiffOptions{From: "v1-light", Stat: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d, "1 file changed") {
		t.Errorf("stat diff missing summary:\n%s", d)
	}
	// path scoping to a file the change doesn't touch
	d, err = Diff(dir, DiffOptions{From: "v1-light", Paths: []string{"nope.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(d) != "" {
		t.Errorf("path-scoped diff should be empty:\n%s", d)
	}
}

func TestDiffBadRefSurfacesStderr(t *testing.T) {
	dir, _, _ := initRepo(t)
	_, err := Diff(dir, DiffOptions{From: "no-such-ref", To: "HEAD"})
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

func TestCheckoutBadRefSurfacesStderr(t *testing.T) {
	dir, _, _ := initRepo(t)
	err := Checkout(context.Background(), dir, "does-not-exist")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Errorf("checkout error should carry git's stderr, got: %v", err)
	}
}
