/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package rules_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/osac-project/osac-metering/internal/database"
	"github.com/osac-project/osac-metering/internal/rules"
)

func revision(t *testing.T, number, effectiveAt, meters string) rules.Revision {
	t.Helper()
	parsed, err := rules.Parse(rules.NamePrefix+number, effectiveAt, meters)
	if err != nil {
		t.Fatalf("parsing revision %s: %v", number, err)
	}
	return parsed
}

func usedRow(revision rules.Revision) database.RuleRevisionRow {
	return database.RuleRevisionRow{
		Revision: revision.Number, EffectiveAt: revision.EffectiveAt, MetersYAML: revision.MetersYAML,
	}
}

var defaultMeters = func() string {
	revision, err := rules.DefaultRevision()
	if err != nil {
		panic(err)
	}
	return revision.MetersYAML
}()

func TestDefaultRevisionParses(t *testing.T) {
	revision, err := rules.DefaultRevision()
	if err != nil {
		t.Fatalf("default revision does not parse: %v", err)
	}
	if revision.Number != "0001" {
		t.Errorf("default revision is %s, want 0001", revision.Number)
	}
	resources := map[string]bool{}
	for _, meter := range revision.Meters {
		resources[meter.Resource+"/"+meter.Meter] = true
	}
	for _, expected := range []string{
		"ComputeInstance/compute",
		"Cluster/control_plane",
		"BareMetalInstance/allocation",
		"BareMetalInstance/consumption",
		"Volume/capacity",
		"ExternalIP/allocation",
		"NATGateway/allocation",
	} {
		if !resources[expected] {
			t.Errorf("default meters miss %s", expected)
		}
	}
}

func TestParseRejectsBadNamesAndTimes(t *testing.T) {
	if _, err := rules.Parse("wrong-name", time.Time{}.Format(time.RFC3339), defaultMeters); err == nil {
		t.Error("a ConfigMap without the revision prefix must not parse")
	}
	if _, err := rules.Parse(rules.NamePrefix+"0001", "not-a-time", defaultMeters); err == nil {
		t.Error("an invalid effective_at must not parse")
	}
	if _, err := rules.Parse(
		rules.NamePrefix+"0001", time.Unix(0, 0).Format(time.RFC3339), "[]",
	); err == nil {
		t.Error("an empty meters document must not parse")
	}
	if _, err := rules.Parse(
		rules.NamePrefix+"0001", time.Unix(0, 0).Format(time.RFC3339),
		strings.Replace(defaultMeters, "states: [RUNNING]", "states: [WARP9]", 1),
	); err == nil {
		t.Error("a revision naming a state the resource never has must not parse")
	}
	// A once state outside the billable states can never confirm a span, so the rule
	// would silently bill nothing.
	onceOutside := strings.Replace(
		strings.Replace(defaultMeters, "states: [PROGRESSING, READY]", "states: [PROGRESSING]", 1),
		"once: [READY]", "once: [READY, FAILED]", 1,
	)
	if _, err := rules.Parse(
		rules.NamePrefix+"0001", time.Unix(0, 0).Format(time.RFC3339), onceOutside,
	); err == nil {
		t.Error("a revision confirming a state it does not bill in must not parse")
	}
	// A revision number is exactly four digits: "2" and "10" are not zero-padded,
	// and Atoi would happily accept "+3".
	for _, number := range []string{"2", "+3", "10", "001", "00001"} {
		if _, err := rules.Parse(
			rules.NamePrefix+number, time.Unix(0, 0).Format(time.RFC3339), defaultMeters,
		); err == nil {
			t.Errorf("revision number %q must not parse: it is not exactly four digits", number)
		}
	}
}

func TestParseRejectsUnsupportedFields(t *testing.T) {
	bad := `- resource: ComputeInstance
  meter: compute
  unit: second
  billable_when:
    states: [RUNNING]
    fields: { SecretField: set }
  dimensions: [instance_type, MysteryDimension]
`
	if _, err := rules.Parse(
		rules.NamePrefix+"0002", time.Now().Add(time.Hour).UTC().Format(time.RFC3339), bad,
	); err == nil {
		t.Error("a revision using unsupported fields must not parse")
	}
}

func TestCheckStartup(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	future := now.Add(24 * time.Hour)
	past := now.Add(-24 * time.Hour)

	first := revision(t, "0001", time.Unix(0, 0).Format(time.RFC3339), defaultMeters)
	secondMeters := strings.Replace(defaultMeters, "states: [RUNNING]", "states: [RUNNING, PAUSED]", 1)
	second := revision(t, "0002", future.Format(time.RFC3339), secondMeters)

	t.Run("fresh database accepts the default revision", func(t *testing.T) {
		present, err := rules.CheckStartup(nil, []rules.Revision{first}, func() time.Time {
			return now
		})
		if err != nil {
			t.Fatalf("fresh startup refused: %v", err)
		}
		if len(present) != 1 || present[0].Number != "0001" {
			t.Fatalf("expected the default revision, got %+v", present)
		}
	})

	t.Run("accepts a future revision", func(t *testing.T) {
		_, err := rules.CheckStartup(
			[]database.RuleRevisionRow{usedRow(first)},
			[]rules.Revision{first, second},
			func() time.Time { return now },
		)
		if err != nil {
			t.Fatalf("future revision refused: %v", err)
		}
	})

	t.Run("refuses a revision first loaded after its effective time", func(t *testing.T) {
		late := revision(t, "0002", past.Format(time.RFC3339), secondMeters)
		_, err := rules.CheckStartup(
			[]database.RuleRevisionRow{usedRow(first)},
			[]rules.Revision{first, late},
			func() time.Time { return now },
		)
		if err == nil {
			t.Fatal("a new revision effective in the past must refuse to start")
		}
	})

	t.Run("refuses a missing used revision", func(t *testing.T) {
		_, err := rules.CheckStartup(
			[]database.RuleRevisionRow{usedRow(first), usedRow(second)},
			[]rules.Revision{first},
			func() time.Time { return now },
		)
		if err == nil {
			t.Fatal("a used revision that went missing must refuse to start")
		}
	})

	t.Run("refuses a changed used revision", func(t *testing.T) {
		changed := revision(t, "0001", time.Unix(0, 0).Format(time.RFC3339), secondMeters)
		_, err := rules.CheckStartup(
			[]database.RuleRevisionRow{usedRow(first)},
			[]rules.Revision{changed},
			func() time.Time { return now },
		)
		if err == nil {
			t.Fatal("a used revision whose content changed must refuse to start")
		}
	})

	t.Run("refuses a meter whose unit or component changed", func(t *testing.T) {
		changedUnit := strings.Replace(secondMeters, "unit: second", "unit: millisecond", 1)
		unitRevision := revision(t, "0002", future.Format(time.RFC3339), changedUnit)
		_, err := rules.CheckStartup(
			[]database.RuleRevisionRow{usedRow(first)},
			[]rules.Revision{first, unitRevision},
			func() time.Time { return now },
		)
		if err == nil {
			t.Fatal("a revision that changes a meter's unit must refuse to start")
		}

		changedComponent := strings.Replace(
			secondMeters, "meter: compute\n  unit: second", "meter: compute\n  unit: second\n  component: compute_v2", 1,
		)
		componentRevision := revision(t, "0002", future.Format(time.RFC3339), changedComponent)
		_, err = rules.CheckStartup(
			[]database.RuleRevisionRow{usedRow(first)},
			[]rules.Revision{first, componentRevision},
			func() time.Time { return now },
		)
		if err == nil {
			t.Fatal("a revision that changes a meter's component must refuse to start")
		}

		// An explicit component equal to the default keeps the shape.
		sameComponent := strings.Replace(
			secondMeters, "meter: compute\n  unit: second", "meter: compute\n  unit: second\n  component: compute", 1,
		)
		sameRevision := revision(t, "0002", future.Format(time.RFC3339), sameComponent)
		_, err = rules.CheckStartup(
			[]database.RuleRevisionRow{usedRow(first)},
			[]rules.Revision{first, sameRevision},
			func() time.Time { return now },
		)
		if err != nil {
			t.Fatalf("an unchanged shape must start: %v", err)
		}
	})

	t.Run("refuses an empty source", func(t *testing.T) {
		_, err := rules.CheckStartup(nil, nil, func() time.Time { return now })
		if err == nil {
			t.Fatal("no revisions at all must refuse to start")
		}
	})

	t.Run("refuses out-of-order revisions", func(t *testing.T) {
		lateFirst := revision(t, "0002", future.Format(time.RFC3339), secondMeters)
		earlySecond := revision(t, "0003", past.Format(time.RFC3339), secondMeters)
		_, err := rules.CheckStartup(
			nil, []rules.Revision{first, lateFirst, earlySecond}, func() time.Time { return now },
		)
		if err == nil {
			t.Fatal("revisions with out-of-order effective times must refuse to start")
		}
	})
}

func TestDirectorySource(t *testing.T) {
	dir := t.TempDir()
	manifest := `apiVersion: v1
kind: ConfigMap
metadata:
  name: osac-metering-rules-0001
immutable: true
data:
  effective_at: "1970-01-01T00:00:00Z"
  meters.yaml: |
` + indent(defaultMeters, "    ")
	if err := os.WriteFile(filepath.Join(dir, "0001.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("writing manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignored"), 0o600); err != nil {
		t.Fatalf("writing note: %v", err)
	}

	revisions, err := rules.NewDirectorySource(dir).List(context.Background())
	if err != nil {
		t.Fatalf("listing revisions: %v", err)
	}
	if len(revisions) != 1 || revisions[0].Number != "0001" {
		t.Fatalf("expected one revision 0001, got %+v", revisions)
	}
}

func TestDirectorySourceOrdersRevisionsNumerically(t *testing.T) {
	dir := t.TempDir()
	write := func(file, name string) {
		t.Helper()
		manifest := `apiVersion: v1
kind: ConfigMap
metadata:
  name: ` + name + `
immutable: true
data:
  effective_at: "1970-01-01T00:00:00Z"
  meters.yaml: |
` + indent(defaultMeters, "    ")
		if err := os.WriteFile(filepath.Join(dir, file), []byte(manifest), 0o600); err != nil {
			t.Fatalf("writing manifest %s: %v", file, err)
		}
	}
	// The directory lists 0010 before 0002, so only the sort can produce
	// numeric order.
	write("a.yaml", rules.NamePrefix+"0010")
	write("b.yaml", rules.NamePrefix+"0002")

	revisions, err := rules.NewDirectorySource(dir).List(context.Background())
	if err != nil {
		t.Fatalf("listing revisions: %v", err)
	}
	if len(revisions) != 2 || revisions[0].Number != "0002" || revisions[1].Number != "0010" {
		t.Fatalf("expected revisions 0002 then 0010, got %+v", revisions)
	}
}

func indent(text, prefix string) string {
	var out []string
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		out = append(out, prefix+line)
	}
	return strings.Join(out, "\n")
}

// TestRevisionRejectsAmbiguousRootGate checks the loader guard: a resource whose meters
// gate owned resources must confirm on the same states in every meter, because the
// calculator cannot pick one gate for the subtree. Deleting the guard silently picks the
// first meter's list, so this test pins it.
func TestRevisionRejectsAmbiguousRootGate(t *testing.T) {
	effective := time.Unix(0, 0).UTC().Format(time.RFC3339)
	conflicting := `- resource: Cluster
  meter: control_plane
  unit: second
  billable_when:
    states: [PROGRESSING, READY]
    turns_billable_once: [READY]
  dimensions: [template, version]
- resource: Cluster
  meter: installation
  unit: second
  billable_when:
    states: [PROGRESSING]
    turns_billable_once: [PROGRESSING]
  dimensions: [template, version]
`
	if _, err := rules.Parse(rules.NamePrefix+"0002", effective, conflicting); err == nil ||
		!strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected the ambiguous root gate to be rejected, got %v", err)
	}

	// Meters that agree on the gate are fine: the subtree inherits one unambiguous list.
	agreeing := `- resource: Cluster
  meter: control_plane
  unit: second
  billable_when:
    states: [PROGRESSING, READY]
    turns_billable_once: [READY]
  dimensions: [template, version]
- resource: Cluster
  meter: installation
  unit: second
  billable_when:
    states: [PROGRESSING, READY]
    turns_billable_once: [READY]
  dimensions: [template, version]
`
	if _, err := rules.Parse(rules.NamePrefix+"0002", effective, agreeing); err != nil {
		t.Fatalf("expected agreeing gates to parse: %v", err)
	}
}

// TestRevisionRejectsUnknownMeterKeys checks that an unknown key rejects the revision
// instead of loading with the field silently dropped. The gate key was renamed once:
// a rule still carrying once: [READY] would load with no gate at all, and
// failed-installation time would bill.
func TestRevisionRejectsUnknownMeterKeys(t *testing.T) {
	effective := time.Unix(0, 0).UTC().Format(time.RFC3339)
	_, err := rules.Parse(rules.NamePrefix+"0002", effective, `- resource: Cluster
  meter: control_plane
  unit: second
  billable_when:
    states: [PROGRESSING, READY]
    once: [READY]
  dimensions: [template, version]
`)
	if err == nil || !strings.Contains(err.Error(), "once") {
		t.Fatalf("expected the unknown once key to be rejected naming the field, got %v", err)
	}
}

// TestRevisionRejectsAmbiguousRootGateInAnyOrder checks the order fix: a root whose
// meters disagree on the gate must be rejected no matter which meter appears first.
// A nil gate list on the first meter used to skip the comparison entirely.
func TestRevisionRejectsAmbiguousRootGateInAnyOrder(t *testing.T) {
	effective := time.Unix(0, 0).UTC().Format(time.RFC3339)
	gated := `- resource: Cluster
  meter: control_plane
  unit: second
  billable_when:
    states: [PROGRESSING, READY]
    turns_billable_once: [READY]
  dimensions: [template, version]
`
	ungated := `- resource: Cluster
  meter: installation
  unit: second
  billable_when:
    states: [PROGRESSING]
  dimensions: [template, version]
`
	for _, yaml := range []string{gated + ungated, ungated + gated} {
		_, err := rules.Parse(rules.NamePrefix+"0002", effective, yaml)
		if err == nil || !strings.Contains(err.Error(), "ambiguous") {
			t.Fatalf("expected the ambiguous root gate to be rejected in both orders, got %v", err)
		}
	}
}

// TestRevisionRejectsRootWithoutMeters checks that a revision dropping the meters of a
// type that owns others is rejected: the owned resources would bill unconditionally,
// because their inherited gate would be undefined.
func TestRevisionRejectsRootWithoutMeters(t *testing.T) {
	effective := time.Unix(0, 0).UTC().Format(time.RFC3339)
	_, err := rules.Parse(rules.NamePrefix+"0002", effective, `- resource: BareMetalInstance
  meter: allocation
  unit: second
  billable_when:
    states: [STARTING, RUNNING, STOPPING, STOPPED, DELETING]
    turns_billable_once: [STARTING, RUNNING]
  dimensions: [instance_type]
`)
	if err == nil || !strings.Contains(err.Error(), "provide the inherited gate") {
		t.Fatalf("expected the rootless revision to be rejected, got %v", err)
	}
}
