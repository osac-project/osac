package gitops

import (
	"context"
	"errors"
)

// ErrNotConfigured reports that the process has no repository to read or
// commit to (AI_GRID_GIT_URL is unset). Like ErrNotReady, its text is safe to
// pass to clients.
var ErrNotConfigured = errors.New("gitops: no repository configured; set AI_GRID_GIT_URL")

// Unconfigured is a Writer for a process started without a repository: every
// Head and Commit fails with ErrNotConfigured, so the APIs that need Git
// answer an error while the others serve as usual.
type Unconfigured struct{}

var _ Writer = Unconfigured{}

// Head fails with ErrNotConfigured.
func (Unconfigured) Head(context.Context) (Snapshot, error) { return nil, ErrNotConfigured }

// FreshHead fails with ErrNotConfigured.
func (Unconfigured) FreshHead(context.Context) (Snapshot, error) { return nil, ErrNotConfigured }

// Commit fails with ErrNotConfigured.
func (Unconfigured) Commit(context.Context, string, Change) (string, error) {
	return "", ErrNotConfigured
}
