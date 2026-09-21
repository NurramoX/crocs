package registry

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func openTest(t *testing.T) *DB {
	t.Helper()
	db, err := OpenAt(context.Background(), filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func insertRepo(t *testing.T, db *DB, name string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	if err := db.InsertRepo(ctx, Repo{Name: name, URL: "https://example.com/" + name, Path: "/tmp/" + name, Shallow: true, FetchedAt: now}); err != nil {
		t.Fatalf("insert repo %s: %v", name, err)
	}
	if err := db.InsertCheckout(ctx, Checkout{ID: name, Repo: name, Ref: "main", Kind: "branch", Commit: "abc", Path: "/tmp/" + name, UpdatedAt: now}); err != nil {
		t.Fatalf("insert default checkout %s: %v", name, err)
	}
}

func TestInsertGetJoined(t *testing.T) {
	ctx := context.Background()
	db := openTest(t)
	insertRepo(t, db, "demo")

	got, err := db.GetProject(ctx, "demo")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "demo" || got.Repo != "demo" || got.URL != "https://example.com/demo" ||
		got.RepoPath != "/tmp/demo" || got.Path != "/tmp/demo" || !got.Shallow ||
		got.Ref != "main" || got.Kind != "branch" || got.Commit != "abc" || !got.IsDefault() {
		t.Errorf("joined project mismatch: %+v", got)
	}

	if err := db.InsertCheckout(ctx, Checkout{ID: "demo@v1", Repo: "demo", Ref: "v1", Kind: "tag", Commit: "def", Path: "/tmp/demo@v1", UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("insert checkout: %v", err)
	}
	v1, err := db.GetProject(ctx, "demo@v1")
	if err != nil {
		t.Fatalf("get v1: %v", err)
	}
	if v1.IsDefault() || v1.Repo != "demo" || v1.RepoPath != "/tmp/demo" || v1.Path != "/tmp/demo@v1" || !v1.Shallow || v1.Kind != "tag" {
		t.Errorf("versioned checkout must carry repo columns: %+v", v1)
	}
	if err := db.SetSnapshot(ctx, "demo@v1", "v1", "tag", "ghi", time.Now()); err != nil {
		t.Fatal(err)
	}
	if v1, _ = db.GetProject(ctx, "demo@v1"); v1.Commit != "ghi" {
		t.Errorf("SetSnapshot not applied: %+v", v1)
	}
}

func TestGetMissing(t *testing.T) {
	db := openTest(t)
	if _, err := db.GetProject(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetProject(missing) = %v, want ErrNotFound", err)
	}
}

func TestListOrdersDefaultFirst(t *testing.T) {
	ctx := context.Background()
	db := openTest(t)
	for _, n := range []string{"c", "a"} {
		insertRepo(t, db, n)
	}
	// "a@0" sorts before "a" lexically; the default checkout must still lead.
	if err := db.InsertCheckout(ctx, Checkout{ID: "a@0", Repo: "a", Ref: "0", Path: "/tmp/a@0", UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	all, err := db.ListProjects(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var ids []string
	for _, p := range all {
		ids = append(ids, p.Name)
	}
	want := []string{"a", "a@0", "c"}
	if len(ids) != len(want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("ids = %v, want %v", ids, want)
		}
	}
}

func countSymbols(t *testing.T, db *DB, id string) int {
	t.Helper()
	n, err := db.CountSymbols(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestDeleteCascades(t *testing.T) {
	ctx := context.Background()
	db := openTest(t)
	insertRepo(t, db, "x")
	if err := db.InsertCheckout(ctx, Checkout{ID: "x@v1", Repo: "x", Ref: "v1", Path: "/tmp/x@v1", UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	sym := []SymbolInput{{Path: "f.go", Name: "foo", Kind: "function", Line: 1, Lang: "go"}}
	for _, id := range []string{"x", "x@v1"} {
		if err := db.ReplaceSymbols(ctx, id, sym); err != nil {
			t.Fatalf("symbols %s: %v", id, err)
		}
	}
	p, err := db.GetProject(ctx, "x@v1")
	if err != nil || p.ParsedAt == nil {
		t.Fatalf("ReplaceSymbols must stamp parsed_at: %+v %v", p, err)
	}

	// Removing one versioned checkout drops only its symbols.
	if err := db.DeleteCheckout(ctx, "x@v1"); err != nil {
		t.Fatalf("delete checkout: %v", err)
	}
	if n := countSymbols(t, db, "x@v1"); n != 0 {
		t.Errorf("x@v1 symbols not cascaded: %d", n)
	}
	if n := countSymbols(t, db, "x"); n != 1 {
		t.Errorf("default checkout symbols must survive: %d", n)
	}
	// Removing the repo drops everything.
	if err := db.DeleteRepo(ctx, "x"); err != nil {
		t.Fatalf("delete repo: %v", err)
	}
	if _, err := db.GetProject(ctx, "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("default checkout should cascade with repo, got %v", err)
	}
	if n := countSymbols(t, db, "x"); n != 0 {
		t.Errorf("repo symbols not cascaded: %d", n)
	}
	if err := db.DeleteRepo(ctx, "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete = %v, want ErrNotFound", err)
	}
}

func TestSchemaMismatchResetsRegistry(t *testing.T) {
	// Any other user_version — older, newer, or a stray unversioned file with
	// our table names — is wiped and recreated, never migrated or refused.
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "stale.db")
	db := func() *DB {
		d, err := OpenAt(ctx, dbPath)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		return d
	}
	d := db()
	insertRepo(t, d, "old")
	for _, s := range []string{"CREATE TABLE projects(name TEXT)", "PRAGMA user_version = 2"} {
		if _, err := d.sql.ExecContext(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	d.Close()

	d = db()
	defer d.Close()
	if all, err := d.ListProjects(ctx); err != nil || len(all) != 0 {
		t.Errorf("stale rows survived the reset: %v %v", all, err)
	}
	var version, leftovers int
	if err := d.sql.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil || version != SchemaVersion {
		t.Errorf("user_version = %d (%v), want %d", version, err, SchemaVersion)
	}
	if err := d.sql.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='projects'`).Scan(&leftovers); err != nil || leftovers != 0 {
		t.Errorf("foreign table not dropped: %d %v", leftovers, err)
	}
	insertRepo(t, d, "fresh")
	if p, err := d.GetProject(ctx, "fresh"); err != nil || p.Repo != "fresh" {
		t.Errorf("registry unusable after reset: %+v %v", p, err)
	}
}
