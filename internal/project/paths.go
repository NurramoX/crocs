// Package project defines on-disk layout: where the registry db lives, where
// clones live, and how project names are normalized. PLAN.md §1 Breaking
// Change #6: $XDG_DATA_HOME/crocs (or ~/.local/share/crocs).
package project

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// DataDir returns the on-disk root for crocs. Honors $XDG_DATA_HOME,
// falls back to ~/.local/share. Always returns an absolute path.
func DataDir() (string, error) {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "crocs"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home dir: %w", err)
	}
	return filepath.Join(home, ".local", "share", "crocs"), nil
}

// RegistryDB returns the absolute path of the SQLite registry file.
func RegistryDB() (string, error) {
	d, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "registry.db"), nil
}

// ProjectsRoot is the directory under which all clones live.
func ProjectsRoot() (string, error) {
	d, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "projects"), nil
}

// ProjectPath returns where a project of the given name should live on disk.
// Does not check existence. The name is validated so the result is always a
// direct child of ProjectsRoot — a name like "../evil" must never produce a
// path outside the managed tree (remove feeds this path to os.RemoveAll).
func ProjectPath(name string) (string, error) {
	if err := ValidateName(name); err != nil {
		return "", err
	}
	root, err := ProjectsRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, name), nil
}

// ValidateName rejects project names that cannot safely be used as a single
// path component under ProjectsRoot.
func ValidateName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("project name is empty")
	case name == "." || name == "..":
		return fmt.Errorf("invalid project name %q", name)
	case strings.ContainsAny(name, `/\`):
		return fmt.Errorf("invalid project name %q: must not contain path separators", name)
	case strings.ContainsRune(name, 0):
		return fmt.Errorf("invalid project name %q: must not contain NUL", name)
	}
	return nil
}

// NameFromURL infers a project name from a git URL. Returns the empty string
// if no sensible (and valid, per ValidateName) name can be derived. Strips a
// trailing ".git".
//
// Examples:
//
//	https://github.com/foo/bar.git    -> "bar"
//	git@github.com:foo/bar            -> "bar"
//	https://example.com/x/y/z/        -> "z"
func NameFromURL(url string) string {
	u := strings.TrimSpace(url)
	u = strings.TrimSuffix(u, "/")
	u = strings.TrimSuffix(u, ".git")
	if i := strings.LastIndexAny(u, "/:"); i >= 0 {
		u = u[i+1:]
	}
	if ValidateName(u) != nil {
		return ""
	}
	return u
}

// ResolveInRoot resolves rel against root and guarantees the result stays
// inside root, following symlinks. crocs commands take agent-supplied
// relative paths (read-files, symbols -p); without this check a path like
// "../../../../etc/passwd" — or an in-repo symlink pointing outside the
// clone — becomes a read primitive over the whole filesystem.
//
// The returned path is the symlink-resolved one, and callers must read
// through it — reading the unresolved join would reopen the window between
// this check and the read, where a component can be swapped for a symlink.
// Callers keep emitting rel, so output is unaffected. A nonexistent path is
// not an error here; the caller's stat/read reports it in its usual shape.
func ResolveInRoot(root, rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("%s: absolute paths are not allowed", rel)
	}
	abs := filepath.Join(root, rel) // Join cleans the result
	if !isWithin(root, abs) {
		return "", fmt.Errorf("%s: escapes the project root", rel)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve project root: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return abs, nil
		}
		return "", err
	}
	if !isWithin(resolvedRoot, resolved) {
		return "", fmt.Errorf("%s: resolves outside the project root", rel)
	}
	return resolved, nil
}

// UnderProjectsRoot reports whether path is a strict descendant of the
// managed projects root. remove uses this so a corrupted or hand-edited
// registry row can never turn a delete into an arbitrary `rm -rf`.
func UnderProjectsRoot(path string) (bool, error) {
	root, err := ProjectsRoot()
	if err != nil {
		return false, err
	}
	clean := filepath.Clean(path)
	return clean != root && isWithin(root, clean), nil
}

// isWithin reports whether path is root itself or a descendant of it. Both
// arguments must already be absolute and cleaned.
func isWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// EnsureDirs creates the data + projects directories with conservative perms.
// Idempotent.
func EnsureDirs() error {
	root, err := ProjectsRoot()
	if err != nil {
		return err
	}
	return os.MkdirAll(root, 0o755)
}
