package gitops

import (
	"context"
	"errors"
	"log/slog"
)

// ConflictAttempts is how many times RetryOnConflict runs a write in all, as
// many as client-go's retry.DefaultRetry steps.
const ConflictAttempts = 5

// RetryOnConflict runs write, and runs it again while it fails with
// ErrConflict, at most ConflictAttempts times in all, like client-go's
// retry.RetryOnConflict. write must do the whole read-decide-write: take a
// new Head, make every check against it, and Commit on its revision. Then a
// rerun keeps every guarantee a single attempt gives, because a conflict on
// a file the caller depends on, such as the object it read at a version, is
// decided again on the new head and answered as it would have been without
// the race. Only a conflict on what the caller didn't depend on, such as an
// index file another object's write also changed, goes away.
//
// It returns write's last error: ErrConflict still, after the last attempt,
// or when ctx is done. Any other error ends it at once. It doesn't wait
// between attempts: a conflict means another write was committed, and a lost
// push drops the cached head, so the next attempt starts from a fetch.
//
// Each rerun is logged, and so is giving up, so that writes racing on the
// repository show in the log although the caller sees none of the reruns.
func RetryOnConflict(ctx context.Context, write func() error) error {
	return retryOnConflict(ctx, slog.Default(), write)
}

func retryOnConflict(ctx context.Context, logger *slog.Logger, write func() error) error {
	var err error
	for attempt := 1; attempt <= ConflictAttempts; attempt++ {
		if err = write(); !errors.Is(err, ErrConflict) || ctx.Err() != nil {
			return err
		}
		if attempt < ConflictAttempts {
			logger.Info("git write lost a race; retrying", "attempt", attempt, "error", err)
		}
	}
	logger.Warn("git write still lost a race; giving up", "attempts", ConflictAttempts, "error", err)
	return err
}
