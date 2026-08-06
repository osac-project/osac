/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package events

import (
	"errors"
	"fmt"
)

var (
	ErrTransientState = errors.New("transient state: update projection only, no CloudEvent")
	ErrSkipTransition = errors.New("no billing boundary: skip event, check for scaling")
)

// TransitionKey identifies a state transition by (previous, current) state.
// Use "*" as From to match any previous state (wildcard).
type TransitionKey struct {
	From string
	To   string
}

// TransitionResult defines what happens on a state transition.
type TransitionResult struct {
	EventType string // CloudEvent type to emit (empty when Transient or Skip)
	Transient bool   // projection-only update, no CloudEvent
	Skip      bool   // no projection update, no CloudEvent (non-billing transition)
}

// TransitionTable maps (previous, current) state pairs to their billing effect.
// Missing entries are invalid transitions — resolveTransition returns an error.
type TransitionTable map[TransitionKey]TransitionResult

// resolveTransition looks up the event type for a state transition.
// Exact (from, to) match takes priority over wildcard (*, to).
// Missing entry = error (fail fast on unknown transitions).
func resolveTransition(table TransitionTable, from, to string) (string, error) {
	if result, ok := table[TransitionKey{from, to}]; ok {
		return applyResult(result)
	}
	if result, ok := table[TransitionKey{"*", to}]; ok {
		return applyResult(result)
	}
	return "", fmt.Errorf("unexpected state transition: %s -> %s", from, to)
}

func applyResult(r TransitionResult) (string, error) {
	if r.Skip {
		return "", ErrSkipTransition
	}
	if r.Transient {
		return "", ErrTransientState
	}
	return r.EventType, nil
}
