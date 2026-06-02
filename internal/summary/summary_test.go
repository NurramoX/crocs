package summary

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportantFiles(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{
		"README.md", "CONTRIBUTING.md", "LICENSE",
		"go.mod", "package.json", "requirements.txt", "requirements-dev.txt",
		"Dockerfile", "Makefile", ".github", // dir, should not appear
		"random.txt", // unrelated
	} {
		if name == ".github" {
			if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := ImportantFiles(root)
	if err != nil {
		t.Fatalf("ImportantFiles: %v", err)
	}
	mustContain(t, got, "README.md", "CONTRIBUTING.md", "LICENSE",
		"go.mod", "package.json", "requirements.txt", "requirements-dev.txt",
		"Dockerfile", "Makefile")
	for _, n := range got {
		if n == "random.txt" {
			t.Errorf("unrelated file random.txt should not be in important files")
		}
		if n == ".github" {
			t.Errorf("directory .github should not be in important files (we only list files)")
		}
	}

	// Always-files (README/CONTRIBUTING/LICENSE) should come before manifests
	// which should come before Dockerfile/Makefile.
	pos := func(n string) int {
		for i, x := range got {
			if x == n {
				return i
			}
		}
		return -1
	}
	if pos("README.md") > pos("go.mod") {
		t.Errorf("README.md should appear before go.mod in important files; got %v", got)
	}
	if pos("go.mod") > pos("Dockerfile") {
		t.Errorf("go.mod should appear before Dockerfile in important files; got %v", got)
	}
}

func TestFindReadme(t *testing.T) {
	cases := []struct {
		files []string
		want  string
	}{
		{[]string{"README.md", "README.txt"}, "README.md"},
		{[]string{"README.rst", "README"}, "README.rst"},
		{[]string{"readme.md"}, "readme.md"},
		{[]string{"LICENSE"}, ""},
	}
	for _, c := range cases {
		dir := t.TempDir()
		for _, f := range c.files {
			if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		got, err := FindReadme(dir)
		if err != nil {
			t.Errorf("FindReadme(%v): %v", c.files, err)
			continue
		}
		if got != c.want {
			t.Errorf("FindReadme(%v) = %q, want %q", c.files, got, c.want)
		}
	}
}

func TestExcerpt_BadgeStripping(t *testing.T) {
	in := `# my-tool

[![Build](https://shields.io/badge/build-passing-green)](https://ci.example.com)
![GoDoc](https://godoc.org/example/repo?status.svg)
[![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

A tool for doing things.
`
	out := Excerpt(in)
	if strings.Contains(out, "shields.io") {
		t.Errorf("badge image URL leaked into excerpt:\n%s", out)
	}
	if strings.Contains(out, "godoc.org") {
		t.Errorf("godoc badge leaked into excerpt:\n%s", out)
	}
	if !strings.Contains(out, "A tool for doing things.") {
		t.Errorf("substantive line missing:\n%s", out)
	}
	if !strings.Contains(out, "# my-tool") {
		t.Errorf("heading missing:\n%s", out)
	}
}

func TestExcerpt_HTMLAlignBanner(t *testing.T) {
	in := `<div align="center">
<a href="https://example.com">
<img src="https://example.com/logo.png" alt="logo"/>
</a>
</div>

# my-tool

Cobra is a library for creating powerful modern CLI applications.
`
	out := Excerpt(in)
	if strings.Contains(out, "logo.png") || strings.Contains(out, "<img") {
		t.Errorf("banner image leaked into excerpt:\n%s", out)
	}
	if strings.Contains(out, `<div align="center">`) {
		t.Errorf("opening banner div leaked into excerpt:\n%s", out)
	}
	if !strings.Contains(out, "Cobra is a library") {
		t.Errorf("substantive prose missing:\n%s", out)
	}
}

func TestExcerpt_CodeBlockPassesThrough(t *testing.T) {
	in := "# example\n\n```go\nfunc main() { <img/> }\n```\n\nsome real prose."
	out := Excerpt(in)
	if !strings.Contains(out, "<img/>") {
		t.Errorf("code block content should pass through verbatim, got:\n%s", out)
	}
	if !strings.Contains(out, "func main()") {
		t.Errorf("code block missing function line, got:\n%s", out)
	}
}

func TestExcerpt_PrunesHRAndBR(t *testing.T) {
	in := `# t

<hr>

Real intro paragraph here.

<br>
`
	out := Excerpt(in)
	if strings.Contains(out, "<hr>") || strings.Contains(out, "<br>") {
		t.Errorf("structural HTML tags should be stripped:\n%s", out)
	}
	if !strings.Contains(out, "Real intro paragraph here.") {
		t.Errorf("intro missing:\n%s", out)
	}
}

func TestExcerpt_LineCap(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString("line ")
		b.WriteString("body\n")
	}
	out := Excerpt(b.String())
	if got := strings.Count(out, "\n") + 1; got > MaxExcerptLines+1 {
		t.Errorf("excerpt exceeded line cap: got %d lines, cap %d", got, MaxExcerptLines)
	}
}

func mustContain(t *testing.T, got []string, want ...string) {
	t.Helper()
	set := map[string]bool{}
	for _, g := range got {
		set[g] = true
	}
	for _, w := range want {
		if !set[w] {
			t.Errorf("missing %q in important files; got %v", w, got)
		}
	}
}
