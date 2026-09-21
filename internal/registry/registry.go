// Package registry owns the SQLite database that tracks repos, their
// checkouts, and the per-checkout symbol index. Pure-Go driver via
// modernc.org/sqlite — no CGO.
//
// A repo is one git object store (the main clone). A checkout is one
// working tree of that repo: the main clone itself (the default checkout,
// addressed by the bare repo name) or a git worktree pinned to another ref
// (addressed as "<repo>@<ref>"). Every query command operates on a checkout.
package registry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"crocs/internal/project"

	_ "modernc.org/sqlite"
)

// SchemaVersion is the current registry schema version, stored via PRAGMA
// user_version. A registry at any other version is wiped and recreated on
// open — schema changes replace, they never migrate.
const SchemaVersion = 3

// ErrNotFound is returned when a query expects a row and finds none.
var ErrNotFound = errors.New("not found")

// Repo is a row in the repos table: one cloned git object store.
type Repo struct {
	Name      string
	URL       string
	Path      string // main clone; owns .git and is the default checkout's worktree
	Shallow   bool
	FetchedAt time.Time
}

// Checkout is a row in the checkouts table: one working tree of a repo.
type Checkout struct {
	ID        string // "<repo>" for the default checkout, "<repo>@<ref>" otherwise
	Repo      string
	Ref       string // branch, tag, or commit the checkout was resolved from
	Kind      string // "branch", "tag", or "commit"
	Commit    string // resolved commit hash
	Path      string
	UpdatedAt time.Time // last time the working tree moved
	ParsedAt  *time.Time
}

// Project is a checkout joined with its repo — the unit every command
// operates on. Name is the checkout id: the CLI handle and the symbol-index
// key. Path is the checkout's working tree; RepoPath is the main clone that
// git fetch / worktree operations run in.
type Project struct {
	Name      string
	Repo      string
	URL       string
	RepoPath  string
	Shallow   bool
	FetchedAt time.Time
	Ref       string
	Kind      string
	Commit    string
	Path      string
	UpdatedAt time.Time
	ParsedAt  *time.Time
}

// IsDefault reports whether this is the repo's default checkout (the main
// clone) rather than a versioned worktree.
func (p Project) IsDefault() bool { return p.Name == p.Repo }

// DB wraps a sql.DB with our schema helpers.
type DB struct {
	sql *sql.DB
}

// Open opens (creating if needed) the registry at the canonical XDG path.
func Open(ctx context.Context) (*DB, error) {
	if err := project.EnsureDirs(); err != nil {
		return nil, err
	}
	path, err := project.RegistryDB()
	if err != nil {
		return nil, err
	}
	return openAt(ctx, path)
}

// OpenAt opens (creating if needed) the registry at the given path. Useful
// for tests.
func OpenAt(ctx context.Context, path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return openAt(ctx, path)
}

func openAt(ctx context.Context, path string) (*DB, error) {
	// busy_timeout: concurrent crocs processes (parallel checkouts from
	// several subagents) queue on the write lock instead of failing with
	// "database is locked".
	sqldb, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(10000)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	sqldb.SetMaxOpenConns(1) // single writer
	if err := sqldb.PingContext(ctx); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	for _, p := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
	} {
		if _, err := sqldb.ExecContext(ctx, p); err != nil {
			sqldb.Close()
			return nil, fmt.Errorf("%s: %w", p, err)
		}
	}
	db := &DB{sql: sqldb}
	// Schema resets drop tables that reference each other; do that before
	// foreign keys are enforced.
	if err := db.ensureSchema(ctx); err != nil {
		sqldb.Close()
		return nil, err
	}
	if _, err := sqldb.ExecContext(ctx, "PRAGMA foreign_keys=ON"); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("PRAGMA foreign_keys=ON: %w", err)
	}
	return db, nil
}

// Close releases the underlying database handle.
func (db *DB) Close() error { return db.sql.Close() }

// ensureSchema makes the on-disk registry match this binary. Anything
// other than the current schema version — an older crocs, a newer one, an
// unversioned file — is dropped and recreated: there is no migration path,
// by design (see CLAUDE.md). Clones on disk are untouched; re-fetch to
// track them again.
func (db *DB) ensureSchema(ctx context.Context) error {
	var version int
	if err := db.sql.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}
	if version == SchemaVersion {
		return nil
	}
	rows, err := db.sql.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return fmt.Errorf("list tables: %w", err)
	}
	var drops []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		drops = append(drops, "DROP TABLE "+name)
	}
	rows.Close()
	if len(drops) > 0 {
		fmt.Fprintf(os.Stderr, "crocs: registry schema is v%d, this binary uses v%d; resetting the registry (clones on disk are kept — re-fetch to track them)\n", version, SchemaVersion)
	}
	return db.exec(ctx, "apply schema", append(drops, schema, setVersion)...)
}

// exec runs the statements in one transaction.
func (db *DB) exec(ctx context.Context, what string, stmts ...string) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("%s: %w", what, err)
		}
	}
	return tx.Commit()
}

var setVersion = fmt.Sprintf("PRAGMA user_version = %d", SchemaVersion)

const schema = `
CREATE TABLE repos (
  name        TEXT PRIMARY KEY,
  url         TEXT NOT NULL,
  path        TEXT NOT NULL,
  shallow     INTEGER NOT NULL,
  fetched_at  INTEGER NOT NULL
);

CREATE TABLE checkouts (
  id          TEXT PRIMARY KEY,
  repo        TEXT NOT NULL REFERENCES repos(name) ON DELETE CASCADE,
  ref         TEXT NOT NULL,
  kind        TEXT NOT NULL,
  commit_hash TEXT NOT NULL,
  path        TEXT NOT NULL,
  updated_at  INTEGER NOT NULL,
  parsed_at   INTEGER
);
CREATE INDEX idx_checkouts_repo ON checkouts(repo);

CREATE TABLE symbols (
  project   TEXT NOT NULL REFERENCES checkouts(id) ON DELETE CASCADE,
  path      TEXT NOT NULL,
  name      TEXT NOT NULL,
  kind      TEXT NOT NULL,
  line      INTEGER NOT NULL,
  end_line  INTEGER,
  parent    TEXT,
  lang      TEXT NOT NULL
);
CREATE INDEX idx_symbols_proj_path ON symbols(project, path);
CREATE INDEX idx_symbols_name_proj ON symbols(name, project);
CREATE INDEX idx_symbols_kind_proj ON symbols(kind, project);
`

// InsertRepo inserts a new repo. Returns an error wrapping sqlite's
// unique-constraint message if name is already taken.
func (db *DB) InsertRepo(ctx context.Context, r Repo) error {
	_, err := db.sql.ExecContext(ctx,
		`INSERT INTO repos(name, url, path, shallow, fetched_at) VALUES(?,?,?,?,?)`,
		r.Name, r.URL, r.Path, boolToInt(r.Shallow), r.FetchedAt.Unix(),
	)
	return err
}

// InsertCheckout inserts a new checkout. The repo must already exist.
func (db *DB) InsertCheckout(ctx context.Context, c Checkout) error {
	_, err := db.sql.ExecContext(ctx,
		`INSERT INTO checkouts(id, repo, ref, kind, commit_hash, path, updated_at, parsed_at)
		 VALUES(?,?,?,?,?,?,?,?)`,
		c.ID, c.Repo, c.Ref, c.Kind, c.Commit, c.Path, c.UpdatedAt.Unix(), nullTime(c.ParsedAt),
	)
	return err
}

const projectSelect = `
SELECT c.id, c.repo, r.url, r.path, r.shallow, r.fetched_at,
       c.ref, c.kind, c.commit_hash, c.path, c.updated_at, c.parsed_at
  FROM checkouts c JOIN repos r ON r.name = c.repo`

// GetProject returns the checkout with the given id, joined with its repo,
// or ErrNotFound.
func (db *DB) GetProject(ctx context.Context, id string) (Project, error) {
	return scanProject(db.sql.QueryRowContext(ctx, projectSelect+` WHERE c.id = ?`, id))
}

// ListProjects returns every checkout joined with its repo, grouped by repo
// with the default checkout first and the rest sorted by id.
func (db *DB) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := db.sql.QueryContext(ctx, projectSelect+` ORDER BY c.repo, (c.id = c.repo) DESC, c.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeleteRepo removes the repo row. Cascades to its checkouts and their
// symbols via FK.
func (db *DB) DeleteRepo(ctx context.Context, name string) error {
	return db.deleteOne(ctx, `DELETE FROM repos WHERE name = ?`, name)
}

// DeleteCheckout removes one checkout row. Cascades to its symbols via FK.
// Callers must not delete a repo's default checkout this way — remove the
// repo instead.
func (db *DB) DeleteCheckout(ctx context.Context, id string) error {
	return db.deleteOne(ctx, `DELETE FROM checkouts WHERE id = ?`, id)
}

func (db *DB) deleteOne(ctx context.Context, stmt, key string) error {
	res, err := db.sql.ExecContext(ctx, stmt, key)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetSnapshot records that a checkout's working tree now sits at ref
// (of the given kind) / commit; used by update.
func (db *DB) SetSnapshot(ctx context.Context, id, ref, kind, commit string, at time.Time) error {
	_, err := db.sql.ExecContext(ctx,
		`UPDATE checkouts SET ref = ?, kind = ?, commit_hash = ?, updated_at = ? WHERE id = ?`,
		ref, kind, commit, at.Unix(), id)
	return err
}

// SetShallow updates a repo's shallow flag; used by unshallow.
func (db *DB) SetShallow(ctx context.Context, repo string, shallow bool) error {
	_, err := db.sql.ExecContext(ctx,
		`UPDATE repos SET shallow = ? WHERE name = ?`,
		boolToInt(shallow), repo)
	return err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanProject(r rowScanner) (Project, error) {
	var (
		p         Project
		shallow   int
		fetchedAt int64
		updatedAt int64
		parsedAt  sql.NullInt64
	)
	if err := r.Scan(&p.Name, &p.Repo, &p.URL, &p.RepoPath, &shallow, &fetchedAt,
		&p.Ref, &p.Kind, &p.Commit, &p.Path, &updatedAt, &parsedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Project{}, ErrNotFound
		}
		return Project{}, err
	}
	p.Shallow = shallow != 0
	p.FetchedAt = time.Unix(fetchedAt, 0).UTC()
	p.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	if parsedAt.Valid {
		t := time.Unix(parsedAt.Int64, 0).UTC()
		p.ParsedAt = &t
	}
	return p, nil
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Unix()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
