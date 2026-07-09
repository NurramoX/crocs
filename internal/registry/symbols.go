package registry

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// SymbolInput is what callers hand the registry to persist. The Project +
// Path fields scope each row; the rest of the columns mirror the symbols
// table in schema v2.
type SymbolInput struct {
	Project string
	Path    string
	Name    string
	Kind    string
	Line    int
	EndLine int    // 0 stored as NULL
	Parent  string // "" stored as NULL
	Lang    string
}

// SymbolRow is what the registry returns from FindSymbols.
type SymbolRow struct {
	Project string
	Path    string
	Name    string
	Kind    string
	Line    int
	EndLine int    // 0 if NULL
	Parent  string // "" if NULL
	Lang    string
}

// ReplaceSymbols atomically replaces the entire symbol set for a project
// and stamps projects.parsed_at = now. Used by fetch and by snapshot-
// changing ops (update/checkout/unshallow) — PLAN.md §5 last paragraph.
//
// Bulk-inserts in a single transaction so even ~25k rows complete in well
// under a second on commodity hardware (Phase 0 measured 171k rows/s).
func (db *DB) ReplaceSymbols(ctx context.Context, project string, rows []SymbolInput) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	commit := false
	defer func() {
		if !commit {
			_ = tx.Rollback()
		}
	}()

	if _, err := tx.ExecContext(ctx, `DELETE FROM symbols WHERE project = ?`, project); err != nil {
		return fmt.Errorf("clear symbols: %w", err)
	}

	if len(rows) > 0 {
		stmt, err := tx.PrepareContext(ctx,
			`INSERT INTO symbols(project, path, name, kind, line, end_line, parent, lang)
			 VALUES(?,?,?,?,?,?,?,?)`)
		if err != nil {
			return fmt.Errorf("prepare insert: %w", err)
		}
		defer stmt.Close()
		for _, r := range rows {
			if _, err := stmt.ExecContext(ctx,
				project, r.Path, r.Name, r.Kind, r.Line,
				intOrNull(r.EndLine), strOrNull(r.Parent), r.Lang,
			); err != nil {
				return fmt.Errorf("insert symbol %s/%s: %w", r.Path, r.Name, err)
			}
		}
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE projects SET parsed_at = ? WHERE name = ?`,
		time.Now().UTC().Unix(), project,
	); err != nil {
		return fmt.Errorf("stamp parsed_at: %w", err)
	}

	commit = true
	return tx.Commit()
}

// SymbolQuery narrows a FindSymbols call. Empty slices mean "no filter on
// this dimension"; an empty Projects with NamePattern set is the
// cross-project mode from PLAN.md §2 #8.
type SymbolQuery struct {
	Projects    []string // restrict to these projects (cross-project subset)
	NamePattern string   // SQL LIKE pattern (callers escape % and _ if literal); see NormalizeGlob
	Kinds       []string // restrict to these kinds
	Langs       []string // restrict to these languages
	Limit       int      // max rows returned; 0 = no cap
}

// NormalizeGlob converts a user-facing pattern to a SQL LIKE expression.
// Heuristic:
//   - if the pattern already contains `%` or `_`, pass through (caller is
//     speaking LIKE directly);
//   - if it contains `*` or `?`, swap to LIKE wildcards;
//   - otherwise wrap with `%…%` so a bare "Repo" matches "UserRepository".
//
// This is intentionally generous — the skill drives this through agents
// who'd otherwise have to learn LIKE syntax to do casual lookups.
func NormalizeGlob(pat string) string {
	if pat == "" {
		return ""
	}
	if strings.ContainsAny(pat, "%_") {
		return pat
	}
	if strings.ContainsAny(pat, "*?") {
		r := strings.NewReplacer("*", "%", "?", "_")
		return r.Replace(pat)
	}
	return "%" + pat + "%"
}

// FindSymbols runs the query and returns matching rows. Sorted by
// (project, path, line) for determinism — agents quoting positions back to
// the main agent will pick the same row across runs.
func (db *DB) FindSymbols(ctx context.Context, q SymbolQuery) ([]SymbolRow, error) {
	var (
		where []string
		args  []any
	)
	if len(q.Projects) > 0 {
		ph := placeholders(len(q.Projects))
		where = append(where, "project IN ("+ph+")")
		for _, p := range q.Projects {
			args = append(args, p)
		}
	}
	if q.NamePattern != "" {
		where = append(where, "name LIKE ?")
		args = append(args, q.NamePattern)
	}
	if len(q.Kinds) > 0 {
		ph := placeholders(len(q.Kinds))
		where = append(where, "kind IN ("+ph+")")
		for _, k := range q.Kinds {
			args = append(args, k)
		}
	}
	if len(q.Langs) > 0 {
		ph := placeholders(len(q.Langs))
		where = append(where, "lang IN ("+ph+")")
		for _, l := range q.Langs {
			args = append(args, l)
		}
	}
	sqlStr := `SELECT project, path, name, kind, line, end_line, parent, lang FROM symbols`
	if len(where) > 0 {
		sqlStr += " WHERE " + strings.Join(where, " AND ")
	}
	sqlStr += " ORDER BY project, path, line, name"
	if q.Limit > 0 {
		sqlStr += " LIMIT ?"
		args = append(args, q.Limit)
	}

	rows, err := db.sql.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SymbolRow
	for rows.Next() {
		var (
			r       SymbolRow
			endLine sql.NullInt64
			parent  sql.NullString
		)
		if err := rows.Scan(&r.Project, &r.Path, &r.Name, &r.Kind, &r.Line, &endLine, &parent, &r.Lang); err != nil {
			return nil, err
		}
		if endLine.Valid {
			r.EndLine = int(endLine.Int64)
		}
		if parent.Valid {
			r.Parent = parent.String
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountSymbols returns the number of indexed symbols for a project. Used by
// commands that leave an existing index untouched (update with an unmoved
// HEAD, unshallow) but still report symbol_count.
func (db *DB) CountSymbols(ctx context.Context, project string) (int, error) {
	var n int
	err := db.sql.QueryRowContext(ctx, `SELECT count(*) FROM symbols WHERE project = ?`, project).Scan(&n)
	return n, err
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	out := make([]byte, 0, 2*n)
	for i := 0; i < n; i++ {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, '?')
	}
	return string(out)
}

func intOrNull(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

func strOrNull(s string) any {
	if s == "" {
		return nil
	}
	return s
}
