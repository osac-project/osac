// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"testing"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func TestWorkerCapacityObservation(t *testing.T) {
	for _, scenario := range []string{"reuse", "list", "nil observation", "first observation only"} {
		t.Run(scenario, func(t *testing.T) {
			r, fc, co := workerReadHarness(t)
			bmi := ownedBMIFixture(co, "recorded-bmi", "recorded-id")
			fc.listed = []*privatev1.BareMetalInstance{bmi}
			existing := indexWorkerBMIs(fc.listed)
			var observations []*workerObservation
			wantLists := 1
			switch scenario {
			case "reuse":
				observations = []*workerObservation{existing}
				wantLists = 0
			case "nil observation":
				observations = []*workerObservation{nil}
			case "first observation only":
				observations = []*workerObservation{nil, existing}
			}
			got, res, err := r.workerCapacityObservation(context.Background(), co, "filter", observations...)
			if err != nil || !res.IsZero() {
				t.Fatalf("result=%+v error=%v", res, err)
			}
			if fc.lists != wantLists {
				t.Fatalf("lists=%d, want %d", fc.lists, wantLists)
			}
			if wantLists == 0 && got != existing {
				t.Fatal("did not reuse supplied observation")
			}
			if got.byID[bmi.GetId()] != bmi {
				t.Fatal("BMI was not indexed")
			}
		})
	}
}
