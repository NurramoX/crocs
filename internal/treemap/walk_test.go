package treemap

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeTree(t *testing.T, files []string) string {
	t.Helper()
	root := t.TempDir()
	for _, rel := range files {
		abs := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestWalkBasicAndGitPrune(t *testing.T) {
	root := writeTree(t, []string{
		"a.go",
		"src/b.go",
		".git/config",
		"sub/.git", // gitlink file
	})
	got, err := Walk(root, Filters{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a.go", "src/b.go"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Walk = %v, want %v", got, want)
	}
}

func TestFiltersSegmentAware(t *testing.T) {
	root := writeTree(t, []string{
		"src/a.go",
		"src/deep/b.go",
		"src2/c.go",
		"top.go",
	})
	got, err := Walk(root, Filters{Includes: []string{"src"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"src/a.go", "src/deep/b.go"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("include src = %v, want %v (src2 must not match)", got, want)
	}

	got, err = Walk(root, Filters{Excludes: []string{"src/deep/"}})
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"src/a.go", "src2/c.go", "top.go"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("exclude src/deep/ = %v, want %v", got, want)
	}
}

func TestDirCounts(t *testing.T) {
	root := writeTree(t, []string{
		"a.go",
		"b.go",
		"src/c.go",
		"src/deep/d.go",
		"src/deep/e.go",
	})
	got, err := DirCounts(root, Filters{})
	if err != nil {
		t.Fatal(err)
	}
	// Counts are non-recursive; "." holds root files; sorted by count desc,
	// then path.
	want := []DirCount{
		{Path: ".", FileCount: 2},
		{Path: "src/deep", FileCount: 2},
		{Path: "src", FileCount: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DirCounts = %v, want %v", got, want)
	}
}

func TestWalkToleratesUnreadableDir(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores permissions")
	}
	root := writeTree(t, []string{
		"ok/a.go",
		"locked/secret.go",
	})
	if err := os.Chmod(filepath.Join(root, "locked"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, "locked"), 0o755) })

	got, err := Walk(root, Filters{})
	if err != nil {
		t.Fatalf("Walk should skip unreadable dirs, got error: %v", err)
	}
	want := []string{"ok/a.go"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Walk = %v, want %v", got, want)
	}
}
