package gitops

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// ErrNotReady reports a call before the repository's first clone succeeded.
// The clone's error is logged, not returned: it can name the host and the
// credential failure, and the API passes this error's text to clients.
var ErrNotReady = errors.New("gitops: repository not cloned yet")

// maxCloneRetry caps the wait between clone attempts.
const maxCloneRetry = time.Minute

// BackgroundWriter is a Writer whose repository is cloned in the background,
// so the process starts, and serves its other APIs, while the Git host is
// unreachable or the token is wrong. Until a clone succeeds, Head and Commit
// fail with ErrNotReady.
type BackgroundWriter struct {
	mu     sync.RWMutex
	writer Writer
}

var _ Writer = (*BackgroundWriter)(nil)

// StartBackgroundWriter calls open until it succeeds or ctx is done, waiting
// retry after the first failure and doubling the wait up to a minute.
func StartBackgroundWriter(ctx context.Context, open func(context.Context) (Writer, error), retry time.Duration) *BackgroundWriter {
	w := &BackgroundWriter{}
	go func() {
		wait := retry
		for {
			writer, err := open(ctx)
			if err == nil {
				w.mu.Lock()
				w.writer = writer
				w.mu.Unlock()
				slog.Info("git repository cloned")
				return
			}
			slog.Warn("git clone failed; retrying", "error", err, "retry", wait)
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			wait = min(2*wait, maxCloneRetry)
		}
	}()
	return w
}

func (w *BackgroundWriter) ready() (Writer, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.writer == nil {
		return nil, ErrNotReady
	}
	return w.writer, nil
}

// Head returns the snapshot at the branch head, or ErrNotReady.
func (w *BackgroundWriter) Head(ctx context.Context) (Snapshot, error) {
	writer, err := w.ready()
	if err != nil {
		return nil, err
	}
	return writer.Head(ctx)
}

// FreshHead returns the snapshot at the remote's branch head, or
// ErrNotReady.
func (w *BackgroundWriter) FreshHead(ctx context.Context) (Snapshot, error) {
	writer, err := w.ready()
	if err != nil {
		return nil, err
	}
	return writer.FreshHead(ctx)
}

// Commit applies c on top of the branch head, or fails with ErrNotReady.
func (w *BackgroundWriter) Commit(ctx context.Context, base string, c Change) (string, error) {
	writer, err := w.ready()
	if err != nil {
		return "", err
	}
	return writer.Commit(ctx, base, c)
}
