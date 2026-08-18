// Package mediapath decides which local files an outbound send is allowed
// to read.
//
// The send gate authorises a recipient. It says nothing about the file a
// send names, so on its own it permits a manipulated model to attach any
// file this process can read — an SSH key, a password store, a database —
// to a message whose recipient the user has already trusted. Confining
// reads to a small set of directories closes that, and it is the only
// control that does: no amount of recipient checking constrains a path.
//
// Errors here are category-only, carrying no path, matching the invariant
// the rest of this program holds to.
package mediapath

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ErrOutsideRoots is returned for a path that resolves outside every
// configured root. It is deliberately generic: a caller must not be able to
// probe the filesystem by distinguishing "outside the roots" from "does not
// exist".
var ErrOutsideRoots = errors.New("file is outside the directories this server may send from")

// Roots is a set of directories outbound media may be read from. The zero
// value allows nothing, which is the safe direction to fail: a Roots that
// was never configured must not silently permit the whole filesystem.
type Roots struct {
	dirs []string
}

// New resolves dirs into a Roots. Each is made absolute and has its
// symlinks resolved, so that later containment checks compare canonical
// paths — without which a symlinked root would never match the resolved
// file paths checked against it.
//
// A directory that cannot be resolved (most often: it does not exist) is an
// error rather than a silent omission, since dropping it would quietly
// narrow what the user configured.
func New(dirs []string) (Roots, error) {
	if len(dirs) == 0 {
		return Roots{}, errors.New("no media directories configured")
	}

	resolved := make([]string, 0, len(dirs))
	for _, d := range dirs {
		abs, err := canonical(d)
		if err != nil {
			// Name the directory: this is the operator's own configuration
			// being reported back to them at startup, not a path derived
			// from a model-supplied argument.
			return Roots{}, fmt.Errorf("media directory %q: %w", d, err)
		}
		resolved = append(resolved, abs)
	}
	return Roots{dirs: resolved}, nil
}

// Dirs returns the resolved directories, for display in help text and
// diagnostics.
func (r Roots) Dirs() []string {
	out := make([]string, len(r.dirs))
	copy(out, r.dirs)
	return out
}

// Allows reports whether path may be read for an outbound send, returning
// nil when it may.
//
// path is resolved the same way the roots were, which is what makes this
// hold against symlinks: a link placed inside an allowed directory that
// points somewhere else resolves to its target before being compared, so it
// is judged by where it actually leads rather than where it sits.
func (r Roots) Allows(path string) error {
	if len(r.dirs) == 0 {
		return ErrOutsideRoots
	}

	target, err := canonical(path)
	if err != nil {
		return ErrOutsideRoots
	}

	for _, root := range r.dirs {
		if contains(root, target) {
			return nil
		}
	}
	return ErrOutsideRoots
}

// canonical returns path as an absolute, symlink-resolved path. Resolution
// requires the path to exist, which is acceptable for both uses here: a
// root is checked at startup, and a file is checked immediately before
// being read.
func canonical(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// contains reports whether target lies within root.
//
// The comparison is on path elements rather than string prefixes, because a
// prefix test would accept "/outbox-stolen/key" as being inside "/outbox".
func contains(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	// "." means target is the root directory itself, which is not a file.
	return rel != "."
}
