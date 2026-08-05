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

// AsRateLimitError checks if err is a RateLimitError and returns it.
func AsRateLimitError(err error) (*RateLimitError, bool) {
	var rateLimitErr *RateLimitError
	if errors.As(err, &rateLimitErr) {
		return rateLimitErr, true
	}
	return nil, false
}
