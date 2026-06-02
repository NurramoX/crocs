// Package project defines on-disk layout: where the registry db lives, where
// clones live, and how project names are normalized. PLAN.md §1 Breaking
// Change #6: $XDG_DATA_HOME/crocs (or ~/.local/share/crocs).
package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DataDir returns the on-disk root for crocs/crocs. Honors $XDG_DATA_HOME,
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
// Does not check existence.
func ProjectPath(name string) (string, error) {
	root, err := ProjectsRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, name), nil
}

// NameFromURL infers a project name from a git URL. Returns the empty string
// if no sensible name can be derived. Strips a trailing ".git".
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
	return u
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
