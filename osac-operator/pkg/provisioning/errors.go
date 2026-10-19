package provisioning

import (
	"errors"
	"fmt"
	"time"
)

// RateLimitError indicates a request was rate-limited and should be retried.
type RateLimitError struct {
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("rate limit active, retry after %v", e.RetryAfter)
}

// ErrJobNotFound reports that a provisioning or deprovisioning job no longer
// exists on the provider side. For AAP this happens when a job was purged or
// moved past the retention window while it was still being polled. Providers
// that can lose job records wrap this error so the shared lifecycle can treat
// the job as terminal instead of requeueing at the poll interval forever.
var ErrJobNotFound = errors.New("provisioning job not found on provider")

// AsRateLimitError checks if err is a RateLimitError and returns it.
func AsRateLimitError(err error) (*RateLimitError, bool) {
	var rateLimitErr *RateLimitError
	if errors.As(err, &rateLimitErr) {
		return rateLimitErr, true
	}
	return nil, false
}
