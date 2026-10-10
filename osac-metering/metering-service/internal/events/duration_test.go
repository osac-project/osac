package events_test

import (
	"strings"
	"testing"
	"time"

	"github.com/osac-project/osac-metering/internal/events"
)

func TestDurationSeconds(t *testing.T) {
	start := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	activeSince := start
	lastHeartbeat := start.Add(30 * time.Minute)
	componentSince := start.Add(45 * time.Minute)

	for _, test := range []struct {
		name            string
		end             time.Time
		lastHeartbeatAt *time.Time
		activeSince     *time.Time
		want            *float64
		wantErr         string
	}{
		{
			name:        "first interval starts at the active start",
			end:         start.Add(time.Hour),
			activeSince: &activeSince,
			want:        floatPointer(3600),
		},
		{
			name:            "later interval starts at the last heartbeat",
			end:             start.Add(time.Hour),
			lastHeartbeatAt: &lastHeartbeat,
			activeSince:     &activeSince,
			want:            floatPointer(1800),
		},
		{
			name:            "component active start clamps a resource heartbeat",
			end:             start.Add(time.Hour),
			lastHeartbeatAt: &lastHeartbeat,
			activeSince:     &componentSince,
			want:            floatPointer(900),
		},
		{
			name:            "active start is required even when a heartbeat exists",
			end:             start.Add(time.Hour),
			lastHeartbeatAt: &lastHeartbeat,
			wantErr:         "active start timestamp is required",
		},
		{
			name:            "end before effective lower bound is an error",
			end:             start.Add(15 * time.Minute),
			lastHeartbeatAt: &lastHeartbeat,
			activeSince:     &activeSince,
			wantErr:         "before effective lower bound",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := events.DurationSeconds(test.end, test.lastHeartbeatAt, test.activeSince)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("DurationSeconds() error = %v, want it to contain %q", err, test.wantErr)
				}
				if got != nil {
					t.Fatalf("DurationSeconds() = %v, want nil after error", *got)
				}
				return
			}
			if err != nil {
				t.Fatalf("DurationSeconds() error = %v", err)
			}
			if test.want == nil {
				if got != nil {
					t.Fatalf("DurationSeconds() = %v, want nil", *got)
				}
				return
			}
			if got == nil || *got != *test.want {
				t.Fatalf("DurationSeconds() = %v, want %v", got, *test.want)
			}
		})
	}
}

func floatPointer(value float64) *float64 {
	return &value
}
