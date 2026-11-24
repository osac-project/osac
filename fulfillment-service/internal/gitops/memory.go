package gitops

import (
	"bytes"
	"context"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// MemoryWriter is an in-memory Writer for tests. Revisions are "0" (the files
// it starts with), "1", "2", and so on.
type MemoryWriter struct {
	mu        sync.Mutex
	revisions []map[string][]byte
	messages  []string
}

var _ Writer = (*MemoryWriter)(nil)

// NewMemoryWriter returns a MemoryWriter whose first revision holds files.
func NewMemoryWriter(files map[string][]byte) *MemoryWriter {
	tree := make(map[string][]byte, len(files))
	for p, data := range files {
		tree[p] = bytes.Clone(data)
	}
	return &MemoryWriter{revisions: []map[string][]byte{tree}, messages: []string{""}}
}

// Head returns the snapshot at the latest revision.
func (w *MemoryWriter) Head(context.Context) (Snapshot, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	rev := len(w.revisions) - 1
	return memorySnapshot{rev: strconv.Itoa(rev), files: w.revisions[rev]}, nil
}

// FreshHead is Head: a MemoryWriter has no remote to lag behind.
func (w *MemoryWriter) FreshHead(ctx context.Context) (Snapshot, error) {
	return w.Head(ctx)
}

// Commit applies c on top of the latest revision.
func (w *MemoryWriter) Commit(_ context.Context, base string, c Change) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	baseRev, err := strconv.Atoi(base)
	if err != nil || baseRev < 0 || baseRev >= len(w.revisions) {
		return "", fmt.Errorf("gitops: unknown base revision %q", base)
	}
	head := w.revisions[len(w.revisions)-1]
	if baseRev != len(w.revisions)-1 {
		baseTree := w.revisions[baseRev]
		for _, p := range c.checked() {
			if !sameAt(baseTree, head, p) {
				return "", ErrConflict
			}
		}
	}
	for _, p := range c.Delete {
		if _, ok := head[p]; !ok {
			return "", fmt.Errorf("%w: %s", ErrNotFound, p)
		}
	}
	next := make(map[string][]byte, len(head)+len(c.Put))
	for p, data := range head {
		next[p] = data
	}
	for p, data := range c.Put {
		next[p] = bytes.Clone(data)
	}
	for _, p := range c.Delete {
		delete(next, p)
	}
	w.revisions = append(w.revisions, next)
	w.messages = append(w.messages, c.Message)
	return strconv.Itoa(len(w.revisions) - 1), nil
}

// sameAt reports whether the file p, or every file under the directory p, is
// the same in a and b.
func sameAt(a, b map[string][]byte, p string) bool {
	under := func(f string) bool { return f == p || strings.HasPrefix(f, p+"/") }
	for f, data := range a {
		if other, ok := b[f]; under(f) && (!ok || !bytes.Equal(data, other)) {
			return false
		}
	}
	for f := range b {
		if _, ok := a[f]; under(f) && !ok {
			return false
		}
	}
	return true
}

// Messages returns the commit messages after the first revision, oldest first.
func (w *MemoryWriter) Messages() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.messages[1:]...)
}

type memorySnapshot struct {
	rev   string
	files map[string][]byte
}

func (s memorySnapshot) Revision() string { return s.rev }

// Fresh is true: a MemoryWriter's head is never behind.
func (s memorySnapshot) Fresh() bool { return true }

func (s memorySnapshot) Read(p string) ([]byte, error) {
	data, ok := s.files[p]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, p)
	}
	return bytes.Clone(data), nil
}

func (s memorySnapshot) List(dir string) ([]string, error) {
	dir = path.Clean(dir)
	var out []string
	for p := range s.files {
		if path.Dir(p) == dir {
			out = append(out, path.Base(p))
		}
	}
	sort.Strings(out)
	return out, nil
}
