/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package rules

import (
	"testing"
	"time"
)

// TestSortRevisionsIsNumeric pins the comparator directly. Parse only produces
// zero-padded four-digit numbers, which order identically as text and as
// numbers, so only hand-built revisions show whether the sort compares numbers
// or strings: as text "10" would order before "2".
func TestSortRevisionsIsNumeric(t *testing.T) {
	revisions := []Revision{
		{Number: "10", EffectiveAt: time.Unix(30, 0)},
		{Number: "2", EffectiveAt: time.Unix(10, 0)},
	}
	sortRevisions(revisions)
	if revisions[0].Number != "2" || revisions[1].Number != "10" {
		t.Fatalf(
			"revisions must sort numerically, got %s before %s",
			revisions[0].Number, revisions[1].Number,
		)
	}
}
