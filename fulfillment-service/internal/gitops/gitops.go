// Package gitops writes the backend's desired state to a Git repository, one
// atomic commit per API write, for Argo CD to deliver to the clusters.
package gitops

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
)

var (
	// ErrNotFound reports a Read of a file that is not in the snapshot.
	ErrNotFound = errors.New("gitops: file not found")
	// ErrConflict reports a Commit whose base is stale: a file the change
	// touches or reads differs between the base revision and the branch head,
	// or the remote refused the push because its branch had moved past the
	// head the commit was made on. RetryOnConflict reruns a write that got it.
	ErrConflict = errors.New("gitops: a file in the change was modified since the base revision")
)

// Change is one atomic commit: files to write and files to delete, by
// repository path.
type Change struct {
	Put    map[string][]byte
	Delete []string
	// Read names the files or directories the change was decided on but
	// doesn't write, such as the files a reference check read. The commit
	// conflicts when any of them differs between base and the branch head,
	// as for the files it writes; a directory differs when a file under it
	// was added, changed or removed.
	Read    []string
	Message string
}

// Snapshot is the repository tree at one revision.
type Snapshot interface {
	Revision() string
	// Read returns the file at p, or ErrNotFound.
	Read(p string) ([]byte, error)
	// List returns the names of the files directly in dir, sorted. A missing
	// directory has no files.
	List(dir string) ([]string, error)
	// Fresh reports whether the snapshot was the remote's head at some
	// point after the call that returned it began: it came from a fetch
	// that started then, or from a Writer that has no remote to lag behind.
	// A snapshot Head serves from RepoConfig.ReadMaxAge's window is not.
	Fresh() bool
}

// Writer reads and commits to one branch.
type Writer interface {
	// Head returns the snapshot at the branch head. It may be a little
	// behind the remote (RepoConfig.ReadMaxAge), never behind the Writer's
	// own commits. Commit lands only on the remote's head either way.
	Head(ctx context.Context) (Snapshot, error)
	// FreshHead returns the snapshot at the remote's branch head as of the
	// call, a Fresh one, whatever RepoConfig.ReadMaxAge is. It costs a
	// fetch: a write uses it to make sure of a refusal it decided on a
	// snapshot that wasn't fresh.
	FreshHead(ctx context.Context) (Snapshot, error)
	// Commit applies c on top of the branch head and returns the new revision.
	// It fails with ErrConflict when a file c touches or reads changed since
	// base, or when another commit reached the branch first, which includes
	// one pushed since the head Commit built on was fetched, and with
	// ErrNotFound when c deletes a file the head doesn't have. ErrConflict
	// doesn't tell a change to the caller's object from one to a file it
	// shares with other objects, such as an index: callers rerun the whole
	// read-decide-write on the new head (RetryOnConflict), which tells them
	// apart.
	Commit(ctx context.Context, base string, c Change) (string, error)
}

// validate checks that c is non-empty and that every path is a clean
// relative file path that appears once.
func (c Change) validate() error {
	if len(c.Put) == 0 && len(c.Delete) == 0 {
		return errors.New("gitops: empty change")
	}
	if strings.TrimSpace(c.Message) == "" {
		return errors.New("gitops: commit message is required")
	}
	seen := make(map[string]bool, len(c.Put)+len(c.Delete))
	for p := range c.Put {
		if err := validPath(p); err != nil {
			return err
		}
		seen[p] = true
	}
	for _, p := range c.Delete {
		if err := validPath(p); err != nil {
			return err
		}
		if seen[p] {
			return fmt.Errorf("gitops: path %q appears twice in one change", p)
		}
		seen[p] = true
	}
	for _, p := range c.Read {
		if err := validPath(p); err != nil {
			return err
		}
	}
	return nil
}

// checked returns every path that must be unchanged since base: the paths c
// touches and those it read.
func (c Change) checked() []string {
	return append(c.paths(), c.Read...)
}

// paths returns every path c touches.
func (c Change) paths() []string {
	out := make([]string, 0, len(c.Put)+len(c.Delete))
	for p := range c.Put {
		out = append(out, p)
	}
	return append(out, c.Delete...)
}

func validPath(p string) error {
	if p == "" || p != path.Clean(p) || path.IsAbs(p) || p == "." || p == ".." || strings.HasPrefix(p, "../") {
		return fmt.Errorf("gitops: invalid path %q", p)
	}
	return nil
}
