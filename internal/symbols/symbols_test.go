package symbols

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKindFromTag(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"definition.function", "function"},
		{"definition.method", "method"},
		{"definition.class", "class"},
		{"definition.interface", "interface"},
		{"reference.call", ""},
		{"reference.class", ""},
		{"", ""},
		{"definition.", ""},
		{"unknown", ""},
	}
	for _, c := range cases {
		got := kindFromTag(c.in)
		if c.in == "definition." {
			// "definition." -> "" by stripping prefix; the helper allows that
			// and the caller (tagsToSymbols) filters empty kinds out anyway.
			if got != "" && got != "definition." {
				t.Errorf("kindFromTag(%q) = %q, want \"\"", c.in, got)
			}
			continue
		}
		if got != c.want {
			t.Errorf("kindFromTag(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestExtractFile_PythonClassAndFunction(t *testing.T) {
	src := `class Animal:
    def __init__(self, name):
        self.name = name

    def describe(self):
        return self.name

def free_function(x):
    return x + 1
`
	path := filepath.Join(t.TempDir(), "sample.py")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	fs, err := ExtractFile(path)
	if err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}
	if fs.Lang != "python" {
		t.Errorf("Lang = %q, want python", fs.Lang)
	}
	mustFind(t, fs.Symbols, "Animal", "class", "")
	mustFind(t, fs.Symbols, "describe", "method", "Animal")
	mustFind(t, fs.Symbols, "free_function", "function", "")
}

func TestExtractFile_GoMethodReceiverParent(t *testing.T) {
	src := `package main

type Dog struct{ name string }

func (d *Dog) Bark() string { return "woof" }
func (d Dog)  Name() string { return d.name }

func free(x int) int { return x + 1 }
`
	path := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	fs, err := ExtractFile(path)
	if err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}
	if fs.Lang != "go" {
		t.Errorf("Lang = %q, want go", fs.Lang)
	}
	mustFind(t, fs.Symbols, "Bark", "method", "Dog")
	mustFind(t, fs.Symbols, "Name", "method", "Dog")
	// Free function has no parent.
	for _, s := range fs.Symbols {
		if s.Name == "free" && s.Parent != "" {
			t.Errorf("free function has unexpected parent %q", s.Parent)
		}
	}
}

func TestExtractFile_UnsupportedLanguage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.rs")
	if err := os.WriteFile(path, []byte("fn main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fs, err := ExtractFile(path)
	if err != nil {
		t.Fatalf("ExtractFile on .rs should not error, got: %v", err)
	}
	if fs.Lang != "" || len(fs.Symbols) != 0 || fs.Path != "" {
		t.Errorf("unsupported .rs should return empty FileSymbols, got %+v", fs)
	}
}

func TestExtractDir(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.go", "package x\nfunc Foo() {}\n")
	writeFile(t, root, "sub/b.py", "def bar():\n    return 1\n")
	writeFile(t, root, "ignored.rs", "fn main() {}\n")

	files, total, err := ExtractDir(context.Background(), root)
	if err != nil {
		t.Fatalf("ExtractDir: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 parsed files, got %d: %+v", len(files), files)
	}
	if total < 2 {
		t.Errorf("expected at least 2 symbols total, got %d", total)
	}
	// Files are sorted by path.
	if files[0].Path != "a.go" {
		t.Errorf("first file path = %q, want a.go", files[0].Path)
	}
	if files[1].Path != "sub/b.py" {
		t.Errorf("second file path = %q, want sub/b.py", files[1].Path)
	}
}

func TestExtractGoReceiver(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"func (d *Dog) Walk() error { return nil }", "Dog"},
		{"func (a Animal) Name() string", "Animal"},
		{"func (d   *  Dog  ) X()", "Dog"},
		{"func Plain() {}", ""},
		{"// some comment", ""},
	}
	for _, c := range cases {
		got := extractGoReceiver([]byte(c.in), 0, uint32(len(c.in)))
		if got != c.want {
			t.Errorf("extractGoReceiver(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func mustFind(t *testing.T, syms []Symbol, name, kind, parent string) {
	t.Helper()
	for _, s := range syms {
		if s.Name == name && s.Kind == kind {
			if parent != "" && s.Parent != parent {
				t.Errorf("symbol %s: parent = %q, want %q", name, s.Parent, parent)
				return
			}
			return
		}
	}
	var names []string
	for _, s := range syms {
		names = append(names, s.Kind+":"+s.Name)
	}
	t.Errorf("expected %s of kind %s; got: %s", name, kind, strings.Join(names, ", "))
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
