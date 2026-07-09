package registry

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenMigrateInsertGet(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "registry.db")

	db, err := OpenAt(ctx, dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	p := Project{
		Name:       "demo",
		URL:        "https://example.com/demo.git",
		Path:       "/tmp/demo",
		DefaultRef: "main",
		Shallow:    true,
		FetchedAt:  time.Now().UTC().Truncate(time.Second),
	}
	if err := db.InsertProject(ctx, p); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := db.GetProject(ctx, "demo")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != p.Name || got.URL != p.URL || got.DefaultRef != p.DefaultRef || got.Shallow != p.Shallow {
		t.Errorf("round-trip mismatch:\nwant %+v\ngot  %+v", p, got)
	}
	if !got.FetchedAt.Equal(p.FetchedAt) {
		t.Errorf("FetchedAt mismatch: want %v got %v", p.FetchedAt, got.FetchedAt)
	}
}

func TestGetMissing(t *testing.T) {
	ctx := context.Background()
	db, err := OpenAt(ctx, filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	if _, err := db.GetProject(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetProject(missing) = %v, want ErrNotFound", err)
	}
}

func TestList(t *testing.T) {
	ctx := context.Background()
	db, err := OpenAt(ctx, filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	for _, n := range []string{"c", "a", "b"} {
		if err := db.InsertProject(ctx, Project{
			Name: n, URL: "u", Path: "p", Shallow: false, FetchedAt: time.Now(),
		}); err != nil {
			t.Fatalf("insert %s: %v", n, err)
		}
	}
	all, err := db.ListProjects(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(all))
	}
	if all[0].Name != "a" || all[1].Name != "b" || all[2].Name != "c" {
		t.Errorf("not sorted: %v %v %v", all[0].Name, all[1].Name, all[2].Name)
	}
}

func TestDeleteCascades(t *testing.T) {
	ctx := context.Background()
	db, err := OpenAt(ctx, filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	if err := db.InsertProject(ctx, Project{Name: "x", URL: "u", Path: "p", FetchedAt: time.Now()}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// Drop a symbol row referencing the project so the FK cascade is tested.
	if _, err := db.sql.ExecContext(ctx,
		`INSERT INTO symbols(project, path, name, kind, line, lang) VALUES(?,?,?,?,?,?)`,
		"x", "f.go", "foo", "function", 1, "go"); err != nil {
		t.Fatalf("insert symbol: %v", err)
	}

	if err := db.DeleteProject(ctx, "x"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	var n int
	if err := db.sql.QueryRowContext(ctx, `SELECT count(*) FROM symbols WHERE project = ?`, "x").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("symbol rows not cascaded; %d remaining", n)
	}
}

func TestForeignSchemaRefused(t *testing.T) {
	// Simulate a pre-v2 install: create a `projects` table by hand and leave
	// user_version=0, then OpenAt should refuse rather than corrupt it.
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "foreign.db")

	// Create a real v2 registry, then reset user_version to 0 below — from
	// migrate()'s perspective that is indistinguishable from a foreign
	// (pre-v2) db that has our table names but never set a version.
	db, err := OpenAt(ctx, dbPath)
	if err != nil {
		t.Fatalf("open fresh: %v", err)
	}
	db.Close()
	// Now corrupt: drop the user_version back to 0 and leave the tables.
	db2, err := OpenAt(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, err := db2.sql.ExecContext(ctx, "PRAGMA user_version = 0"); err != nil {
		t.Fatalf("reset user_version: %v", err)
	}
	db2.Close()

	if _, err := OpenAt(ctx, dbPath); !errors.Is(err, ErrIncompatibleSchema) {
		t.Errorf("OpenAt on unversioned tables: got err=%v, want ErrIncompatibleSchema", err)
	}
}
