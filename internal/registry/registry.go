// Package registry owns the SQLite database that tracks projects and
// (Phase 2+) symbols. Schema is PLAN.md §4 v2. Pure-Go driver via
// modernc.org/sqlite — no CGO.
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

// SchemaVersion is the current registry schema version. Stored via
// PRAGMA user_version. Breaking changes bump this and refuse to open.
const SchemaVersion = 2

// ErrNotFound is returned when a query expects a row and finds none.
var ErrNotFound = errors.New("not found")

// ErrIncompatibleSchema is returned when the registry on disk was written by
// an older or newer crocs whose schema this binary does not understand.
var ErrIncompatibleSchema = errors.New("incompatible registry schema")

// Project is a registry row in the projects table.
type Project struct {
	Name       string
	URL        string
	Path       string
	DefaultRef string // branch/tag checked out at fetch time; may be empty
	Shallow    bool
	FetchedAt  time.Time
	ParsedAt   *time.Time // nil until Phase 2 builds the symbol index
}

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
	sqldb, err := sql.Open("sqlite", path)
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
		"PRAGMA foreign_keys=ON",
	} {
		if _, err := sqldb.ExecContext(ctx, p); err != nil {
			sqldb.Close()
			return nil, fmt.Errorf("%s: %w", p, err)
		}
	}
	db := &DB{sql: sqldb}
	if err := db.migrate(ctx); err != nil {
		sqldb.Close()
		return nil, err
	}
	return db, nil
}

// Close releases the underlying database handle.
func (db *DB) Close() error { return db.sql.Close() }

func (db *DB) migrate(ctx context.Context) error {
	var version int
	if err := db.sql.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}
	switch {
	case version == 0:
		// Fresh db OR a foreign (e.g. Python-crocs) db at the same path that
		// never set user_version. If our expected tables already exist, refuse
		// rather than corrupt: PLAN.md §2 Breaking Change #5 says no importer,
		// the user re-fetches fresh.
		var anyTable int
		if err := db.sql.QueryRowContext(ctx,
			`SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('projects','symbols')`,
		).Scan(&anyTable); err != nil {
			return fmt.Errorf("probe existing tables: %w", err)
		}
		if anyTable > 0 {
			return fmt.Errorf("%w: registry has unversioned tables (likely a pre-v2 install); remove the registry file and re-fetch",
				ErrIncompatibleSchema)
		}
		if _, err := db.sql.ExecContext(ctx, schemaV2); err != nil {
			return fmt.Errorf("apply schema v%d: %w", SchemaVersion, err)
		}
		if _, err := db.sql.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", SchemaVersion)); err != nil {
			return fmt.Errorf("set user_version: %w", err)
		}
	case version == SchemaVersion:
		// Up to date.
	default:
		return fmt.Errorf("%w: db is v%d, this binary expects v%d",
			ErrIncompatibleSchema, version, SchemaVersion)
	}
	return nil
}

const schemaV2 = `
CREATE TABLE projects (
  name        TEXT PRIMARY KEY,
  url         TEXT NOT NULL,
  path        TEXT NOT NULL,
  default_ref TEXT,
  shallow     INTEGER NOT NULL,
  fetched_at  INTEGER NOT NULL,
  parsed_at   INTEGER
);

CREATE TABLE symbols (
  project   TEXT NOT NULL REFERENCES projects(name) ON DELETE CASCADE,
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

// InsertProject inserts a new project. Returns an error wrapping
// sqlite's unique-constraint message if name is already taken.
func (db *DB) InsertProject(ctx context.Context, p Project) error {
	_, err := db.sql.ExecContext(ctx,
		`INSERT INTO projects(name, url, path, default_ref, shallow, fetched_at, parsed_at)
		 VALUES(?,?,?,?,?,?,?)`,
		p.Name, p.URL, p.Path, nullStr(p.DefaultRef), boolToInt(p.Shallow),
		p.FetchedAt.Unix(), nullTime(p.ParsedAt),
	)
	return err
}

// GetProject returns the project with the given name or ErrNotFound.
func (db *DB) GetProject(ctx context.Context, name string) (Project, error) {
	row := db.sql.QueryRowContext(ctx,
		`SELECT name, url, path, default_ref, shallow, fetched_at, parsed_at
		 FROM projects WHERE name = ?`, name)
	return scanProject(row)
}

// ListProjects returns all projects sorted by name.
func (db *DB) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := db.sql.QueryContext(ctx,
		`SELECT name, url, path, default_ref, shallow, fetched_at, parsed_at
		 FROM projects ORDER BY name`)
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

// DeleteProject removes the project row. Cascades to symbols via FK.
func (db *DB) DeleteProject(ctx context.Context, name string) error {
	res, err := db.sql.ExecContext(ctx,
		`DELETE FROM projects WHERE name = ?`, name)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateProjectRef updates default_ref + fetched_at; used by checkout/update.
func (db *DB) UpdateProjectRef(ctx context.Context, name, ref string, fetchedAt time.Time) error {
	_, err := db.sql.ExecContext(ctx,
		`UPDATE projects SET default_ref = ?, fetched_at = ? WHERE name = ?`,
		nullStr(ref), fetchedAt.Unix(), name)
	return err
}

// SetShallow updates the shallow flag; used by unshallow.
func (db *DB) SetShallow(ctx context.Context, name string, shallow bool) error {
	_, err := db.sql.ExecContext(ctx,
		`UPDATE projects SET shallow = ? WHERE name = ?`,
		boolToInt(shallow), name)
	return err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanProject(r rowScanner) (Project, error) {
	var (
		p         Project
		ref       sql.NullString
		shallow   int
		fetchedAt int64
		parsedAt  sql.NullInt64
	)
	if err := r.Scan(&p.Name, &p.URL, &p.Path, &ref, &shallow, &fetchedAt, &parsedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Project{}, ErrNotFound
		}
		return Project{}, err
	}
	if ref.Valid {
		p.DefaultRef = ref.String
	}
	p.Shallow = shallow != 0
	p.FetchedAt = time.Unix(fetchedAt, 0).UTC()
	if parsedAt.Valid {
		t := time.Unix(parsedAt.Int64, 0).UTC()
		p.ParsedAt = &t
	}
	return p, nil
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
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
