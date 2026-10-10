package events

import (
	"fmt"
	"time"
)

// DurationSeconds returns the interval ending at end, bounded below by the
// later of the last heartbeat and the required active interval start. A missing
// last heartbeat denotes the first heartbeat, so the active start is used.
func DurationSeconds(end time.Time, lastHeartbeatAt, activeSince *time.Time) (*float64, error) {
	if activeSince == nil {
		return nil, fmt.Errorf("cannot calculate duration ending at %s: active start timestamp is required", end.Format(time.RFC3339Nano))
	}

	start := activeSince
	if lastHeartbeatAt != nil && lastHeartbeatAt.After(*start) {
		start = lastHeartbeatAt
	}

	if end.Before(*start) {
		return nil, fmt.Errorf(
			"duration end %s is before effective lower bound %s",
			end.Format(time.RFC3339Nano),
			start.Format(time.RFC3339Nano),
		)
	}

	seconds := end.Sub(*start).Seconds()
	return &seconds, nil
}
