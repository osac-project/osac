package events

import (
	"errors"
	"testing"
)

func TestVolumeTransitionTableIsExplicit(t *testing.T) {
	states := []string{
		StateEmpty,
		VolumeStateUnspecified,
		VolumeStateCreating,
		VolumeStateAvailable,
		VolumeStateFailed,
		VolumeStateDeleting,
		VolumeStateDeleted,
	}
	expectedEvents := map[TransitionKey]string{
		{StateEmpty, VolumeStateAvailable}:             eventBillableStart,
		{VolumeStateUnspecified, VolumeStateAvailable}: eventBillableStart,
		{VolumeStateCreating, VolumeStateAvailable}:    eventBillableStart,
		{VolumeStateAvailable, VolumeStateFailed}:      EventSuspended,
		{VolumeStateAvailable, VolumeStateDeleting}:    EventSuspended,
		{VolumeStateAvailable, VolumeStateDeleted}:     EventSuspended,
	}

	for _, from := range states {
		for _, to := range states {
			got, err := resolveTransition(volumeTransitions, from, to)
			key := TransitionKey{from, to}
			if expectedEvent, ok := expectedEvents[key]; ok {
				if err != nil || got != expectedEvent {
					t.Fatalf("transition %s -> %s = %q, %v; want %q", from, to, got, err, expectedEvent)
				}
				continue
			}
			if _, exists := volumeTransitions[key]; exists {
				if !errors.Is(err, ErrSkipTransition) {
					t.Fatalf("transition %s -> %s = %v; want skip", from, to, err)
				}
				continue
			}
			if err == nil || errors.Is(err, ErrSkipTransition) {
				t.Fatalf("transition %s -> %s = %v; want error", from, to, err)
			}
		}
	}
}
