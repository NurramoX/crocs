package project

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestNameFromURL(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://github.com/foo/bar.git", "bar"},
		{"https://github.com/foo/bar", "bar"},
		{"git@github.com:foo/bar", "bar"},
		{"git@github.com:foo/bar.git", "bar"},
		{"https://example.com/x/y/z/", "z"},
		{"bar", "bar"},
		{"", ""},
		{"/", ""},
		{"https://example.com/", "example.com"},
		{"https://example.com/..", ""},     // must not yield a traversal name
		{"https://example.com/...git", ""}, // ".." after .git strip — traversal
	}
	for _, c := range cases {
		if got := NameFromURL(c.url); got != c.want {
			t.Errorf("NameFromURL(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

func TestValidateName(t *testing.T) {
	valid := []string{"bar", "my-repo", "repo.js", ".github", "UPPER_case"}
	for _, n := range valid {
		if err := ValidateName(n); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", n, err)
		}
	}
	invalid := []string{"", ".", "..", "a/b", `a\b`, "../evil", "a/../../b", "x\x00y"}
	for _, n := range invalid {
		if err := ValidateName(n); err == nil {
			t.Errorf("ValidateName(%q) = nil, want error", n)
		}
	}
}

func TestProjectPathStaysUnderRoot(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	root, err := ProjectsRoot()
	if err != nil {
		t.Fatal(err)
	}
	p, err := ProjectPath("bar")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(p) != root {
		t.Errorf("ProjectPath(bar) = %q, not a direct child of %q", p, root)
	}
	for _, n := range []string{"../evil", "a/b", ".."} {
		if _, err := ProjectPath(n); err == nil {
			t.Errorf("ProjectPath(%q) succeeded, want error", n)
		}
	}
	if !strings.HasPrefix(p, root) {
		t.Errorf("ProjectPath escaped root: %q", p)
	}
}

func TestValidateRef(t *testing.T) {
	valid := []string{"main", "v1.8.0", "release/1.x", "feature-x_y", "a0a6ae020bb3899ff0276067863e50523f897370", "v1@2"}
	for _, r := range valid {
		if err := ValidateRef(r); err != nil {
			t.Errorf("ValidateRef(%q) = %v, want nil", r, err)
		}
	}
	invalid := []string{"", "-x", "--depth=1", "..", "a..b", "/main", "main/", "a//b", "a/../b", "a b", "a\tb", "x\x00y", `a\b`, "a~1", "a^2", "a:b", "a?", "a*", "a[b",
		"HEAD", "@", "main@{1}", "refs/heads/main", "origin/main"}
	for _, r := range invalid {
		if err := ValidateRef(r); err == nil {
			t.Errorf("ValidateRef(%q) = nil, want error", r)
		}
	}
}

func TestValidateNameRejectsAt(t *testing.T) {
	if err := ValidateName("cobra@v1"); err == nil {
		t.Error("ValidateName must reject '@' (reserved for checkout handles)")
	}
}

func TestSplitIDAndCheckoutID(t *testing.T) {
	cases := []struct{ id, repo, ref string }{
		{"cobra", "cobra", ""},
		{"cobra@v1.8.0", "cobra", "v1.8.0"},
		{"cobra@release/1.x", "cobra", "release/1.x"},
		{"cobra@v1@2", "cobra", "v1@2"},
	}
	for _, c := range cases {
		repo, ref := SplitID(c.id)
		if repo != c.repo || ref != c.ref {
			t.Errorf("SplitID(%q) = %q, %q; want %q, %q", c.id, repo, ref, c.repo, c.ref)
		}
		if ref != "" && CheckoutID(repo, ref) != c.id {
			t.Errorf("CheckoutID(%q, %q) = %q, want %q (handles must round-trip)", repo, ref, CheckoutID(repo, ref), c.id)
		}
	}
}

func TestCheckoutPathStaysUnderRoot(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	root, err := ProjectsRoot()
	if err != nil {
		t.Fatal(err)
	}
	p, err := CheckoutPath("cobra", "release/1.x")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(p) != root {
		t.Errorf("CheckoutPath(cobra, release/1.x) = %q, not a direct child of %q", p, root)
	}
	if filepath.Base(p) != "cobra@release-1.x" {
		t.Errorf("CheckoutPath flattened name = %q", filepath.Base(p))
	}
	for _, c := range [][2]string{{"../evil", "v1"}, {"cobra", "../evil"}, {"cobra", ""}, {"cobra", "-x"}} {
		if _, err := CheckoutPath(c[0], c[1]); err == nil {
			t.Errorf("CheckoutPath(%q, %q) succeeded, want error", c[0], c[1])
		}
	}
}

func TestResolveInRootHidesGit(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{".git", ".git/config", "sub/../.git"} {
		if _, err := ResolveInRoot(root, rel); err == nil {
			t.Errorf("ResolveInRoot(%q) succeeded, want error", rel)
		}
	}
	if _, err := ResolveInRoot(root, ".github/workflows/ci.yml"); err != nil {
		t.Errorf("dotfiles other than .git must resolve: %v", err)
	}
}
