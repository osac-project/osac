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

	for _, from := range states {
		for _, to := range states[1:] {
			_, err := resolveTransition(volumeTransitions, from, to)
			invalid := (from == VolumeStateCreating && to == VolumeStateUnspecified) ||
				(from == VolumeStateAvailable && (to == VolumeStateUnspecified || to == VolumeStateCreating)) ||
				(from == VolumeStateFailed && (to == VolumeStateUnspecified || to == VolumeStateCreating || to == VolumeStateAvailable)) ||
				(from == VolumeStateDeleting && (to == VolumeStateUnspecified || to == VolumeStateCreating || to == VolumeStateAvailable || to == VolumeStateFailed)) ||
				(from == VolumeStateDeleted && to != VolumeStateDeleted)
			if invalid {
				if err == nil || errors.Is(err, ErrSkipTransition) {
					t.Fatalf("expected invalid transition for %s -> %s, got %v", from, to, err)
				}
				continue
			}
			if err != nil && !errors.Is(err, ErrSkipTransition) {
				t.Fatalf("unexpected transition result for %s -> %s: %v", from, to, err)
			}
		}
	}
}
