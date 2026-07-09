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
