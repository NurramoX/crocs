// Package project defines on-disk layout: where the registry db lives, where
// clones live, and how project names are normalized. The root is
// $XDG_DATA_HOME/crocs (or ~/.local/share/crocs).
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

// ProjectPath returns where a repo of the given name should live on disk:
// the main clone, which owns the git object store and doubles as the
// repo's default checkout. Does not check existence. The name is validated
// so the result is always a direct child of ProjectsRoot — a name like
// "../evil" must never produce a path outside the managed tree (remove
// feeds this path to os.RemoveAll).
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

// CheckoutPath returns where the worktree for ref of the given repo should
// live on disk: a sibling of the main clone named "<repo>@<ref>", with
// path separators in the ref flattened to '-' so the result stays a direct
// child of ProjectsRoot. The handle itself (CheckoutID) keeps the ref
// verbatim; only the directory name is lossy.
func CheckoutPath(repo, ref string) (string, error) {
	if err := ValidateName(repo); err != nil {
		return "", err
	}
	if err := ValidateRef(ref); err != nil {
		return "", err
	}
	root, err := ProjectsRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, repo+"@"+strings.ReplaceAll(ref, "/", "-")), nil
}

// CheckoutID is the handle for a versioned checkout: "<repo>@<ref>", the
// inverse of SplitID. The default checkout (the main clone) is addressed
// by the bare repo name.
func CheckoutID(repo, ref string) string {
	return repo + "@" + ref
}

// SplitID parses a checkout handle into its repo name and ref. A bare repo
// name yields an empty ref (the default checkout). Refs may themselves
// contain '@', so the split happens at the first one.
func SplitID(id string) (repo, ref string) {
	if i := strings.IndexByte(id, '@'); i >= 0 {
		return id[:i], id[i+1:]
	}
	return id, ""
}

// ValidateName rejects repo names that cannot safely be used as a single
// path component under ProjectsRoot, or that would be ambiguous as a
// checkout handle ('@' separates repo from ref).
func ValidateName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("project name is empty")
	case name == "." || name == "..":
		return fmt.Errorf("invalid project name %q", name)
	case strings.ContainsAny(name, `/\`):
		return fmt.Errorf("invalid project name %q: must not contain path separators", name)
	case strings.ContainsRune(name, '@'):
		return fmt.Errorf("invalid project name %q: '@' is reserved for checkout handles (<repo>@<ref>)", name)
	case strings.ContainsRune(name, 0):
		return fmt.Errorf("invalid project name %q: must not contain NUL", name)
	}
	return nil
}

// ValidateRef rejects refs that git would refuse or that could not be
// flattened into a safe path component by CheckoutPath. It is deliberately
// stricter than git's own rules: a ref is an agent-supplied string that
// ends up both on the git command line and on disk.
func ValidateRef(ref string) error {
	switch {
	case ref == "":
		return fmt.Errorf("ref is empty")
	case ref == "HEAD" || ref == "@" || strings.Contains(ref, "@{"):
		return fmt.Errorf("invalid ref %q: symbolic refs cannot be pinned; name a branch, tag, or commit", ref)
	case strings.HasPrefix(ref, "refs/") || strings.HasPrefix(ref, "origin/"):
		return fmt.Errorf("invalid ref %q: use the bare branch or tag name (no refs/ or origin/ prefix)", ref)
	case strings.HasPrefix(ref, "-"):
		return fmt.Errorf("invalid ref %q: must not start with '-'", ref)
	case strings.Contains(ref, ".."):
		return fmt.Errorf("invalid ref %q: must not contain '..'", ref)
	case strings.HasPrefix(ref, "/") || strings.HasSuffix(ref, "/") || strings.Contains(ref, "//"):
		return fmt.Errorf("invalid ref %q: malformed path component", ref)
	case strings.ContainsAny(ref, "\\ \t\n\x00~^:?*["):
		return fmt.Errorf("invalid ref %q: contains characters git refuses in ref names", ref)
	}
	for _, comp := range strings.Split(ref, "/") {
		if comp == "." || comp == ".." {
			return fmt.Errorf("invalid ref %q: malformed path component", ref)
		}
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
	// .git is git's own state, never project content: a directory in the
	// main clone, a pointer file in a linked worktree.
	if r, err := filepath.Rel(root, abs); err == nil && (r == ".git" || strings.HasPrefix(r, ".git"+string(filepath.Separator))) {
		return "", fmt.Errorf("%s: git metadata is not readable", rel)
	}
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
