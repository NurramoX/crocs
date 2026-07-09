package grepx

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMakeRGGlobs(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"src", []string{"src", "src/**"}},
		{"src/", []string{"src", "src/**"}},
		{"src/auth.py", []string{"src/auth.py", "src/auth.py/**"}},
		{"*.go", []string{"*.go"}},
		{"src/**/x?.py", []string{"src/**/x?.py"}},
		{"[ab].go", []string{"[ab].go"}},
	}
	for _, c := range cases {
		if got := makeRGGlobs(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("makeRGGlobs(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestMatchOne(t *testing.T) {
	cases := []struct {
		rel, pat string
		want     bool
	}{
		// Literal paths are segment-aware.
		{"src/a.py", "src", true},
		{"src/deep/b.py", "src", true},
		{"src2/c.py", "src", false},
		{"src/a.py", "src/a.py", true},
		{"src/a.pyc", "src/a.py", false},
		{"src/a.py", "src/", true},
		// Globs.
		{"src/a.py", "*.py", true},
		{"src/deep/b.py", "*.py", true},
		{"src/a.py", "*.go", false},
		{"src/a.py", "src/**", true},
		{"nested/src/d.py", "src/**", false},
		{"src/deep/b.py", "src/*/b.py", true},
		{"src/deep/very/c.py", "src/*/c.py", false},
		{"src/deep/very/c.py", "src/**/c.py", true},
		{"src/c.py", "src/**/c.py", true}, // **/ matches zero dirs
		{"src/a1.py", "src/a?.py", true},
	}
	for _, c := range cases {
		if got := matchOne(c.rel, c.pat); got != c.want {
			t.Errorf("matchOne(%q, %q) = %v, want %v", c.rel, c.pat, got, c.want)
		}
	}
}

func TestPathMatchesExcludeWins(t *testing.T) {
	if pathMatches("src/gen/a.py", []string{"src"}, []string{"src/gen"}) {
		t.Error("exclude should win over include")
	}
	if !pathMatches("src/a.py", nil, nil) {
		t.Error("no filters should match everything")
	}
}

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		abs := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestRunGoBasic(t *testing.T) {
	root := writeTree(t, map[string]string{
		"a.py":     "x = 1\nneedle here\ny = 2\n",
		"sub/b.py": "no match\n",
		"bin.dat":  "needle\x00binary",
	})
	res, err := runGo(context.Background(), root, Options{Pattern: "needle", MaxResults: 10, Context: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) != 1 {
		t.Fatalf("got %d matches, want 1 (binary must be skipped): %+v", len(res.Matches), res.Matches)
	}
	m := res.Matches[0]
	if m.Path != "a.py" || m.Line != 2 || m.Text != "needle here" {
		t.Errorf("unexpected match: %+v", m)
	}
	if len(m.ContextBefore) != 1 || m.ContextBefore[0] != "x = 1" {
		t.Errorf("context_before wrong: %v", m.ContextBefore)
	}
	if len(m.ContextAfter) != 1 || m.ContextAfter[0] != "y = 2" {
		t.Errorf("context_after wrong: %v", m.ContextAfter)
	}
}

func TestRunGoGlobInclude(t *testing.T) {
	root := writeTree(t, map[string]string{
		"a.go":      "needle\n",
		"b.py":      "needle\n",
		"deep/c.go": "needle\n",
	})
	res, err := runGo(context.Background(), root, Options{Pattern: "needle", Includes: []string{"*.go"}, MaxResults: 10})
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, m := range res.Matches {
		paths = append(paths, m.Path)
	}
	want := []string{"a.go", "deep/c.go"}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("glob include matched %v, want %v", paths, want)
	}
}

func TestRunGoMultiline(t *testing.T) {
	root := writeTree(t, map[string]string{
		"a.txt": "start\nmiddle\nfinish\n",
	})
	res, err := runGo(context.Background(), root, Options{Pattern: "start.middle", Multiline: true, MaxResults: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) != 1 {
		t.Fatalf("multiline dot should cross newlines; got %d matches", len(res.Matches))
	}
	m := res.Matches[0]
	if m.Line != 1 {
		t.Errorf("start line = %d, want 1", m.Line)
	}
	if m.Text != "start\nmiddle" {
		t.Errorf("multiline Text = %q, want both covered lines", m.Text)
	}
}

func TestRunGoTruncatedIsSorted(t *testing.T) {
	files := map[string]string{}
	for _, name := range []string{"z.txt", "a.txt", "m.txt"} {
		files[name] = strings.Repeat("needle\n", 3)
	}
	root := writeTree(t, files)
	res, err := runGo(context.Background(), root, Options{Pattern: "needle", MaxResults: 5})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated {
		t.Fatal("expected truncation at 5 of 9 matches")
	}
	for i := 1; i < len(res.Matches); i++ {
		prev, cur := res.Matches[i-1], res.Matches[i]
		if prev.Path > cur.Path || (prev.Path == cur.Path && prev.Line > cur.Line) {
			t.Fatalf("truncated results not sorted: %v before %v", prev, cur)
		}
	}
}

func TestParseRipgrepJSON(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"begin","data":{"path":{"text":"./a.py"}}}`,
		`{"type":"context","data":{"path":{"text":"./a.py"},"lines":{"text":"before\n"},"line_number":1}}`,
		`{"type":"match","data":{"path":{"text":"./a.py"},"lines":{"text":"needle\n"},"line_number":2,"submatches":[{"start":0}]}}`,
		`{"type":"context","data":{"path":{"text":"./a.py"},"lines":{"text":"after\n"},"line_number":3}}`,
		`{"type":"end","data":{}}`,
	}, "\n")
	matches, truncated, err := parseRipgrepJSON(strings.NewReader(stream), Options{MaxResults: 10, Context: 1})
	if err != nil {
		t.Fatal(err)
	}
	if truncated {
		t.Error("unexpected truncation")
	}
	if len(matches) != 1 {
		t.Fatalf("got %d matches, want 1", len(matches))
	}
	m := matches[0]
	if m.Path != "a.py" || m.Line != 2 || m.Text != "needle" || m.Column != 1 {
		t.Errorf("unexpected match: %+v", m)
	}
	if len(m.ContextBefore) != 1 || m.ContextBefore[0] != "before" {
		t.Errorf("context_before: %v", m.ContextBefore)
	}
	if len(m.ContextAfter) != 1 || m.ContextAfter[0] != "after" {
		t.Errorf("context_after: %v", m.ContextAfter)
	}
}

func TestIsProbablyText(t *testing.T) {
	if !IsProbablyText([]byte("hello\nworld")) {
		t.Error("plain text misdetected as binary")
	}
	if IsProbablyText([]byte("he\x00llo")) {
		t.Error("NUL byte not detected as binary")
	}
}
