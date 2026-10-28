/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

// Package usage computes expected usage from history and rules. It never reads a clock, another
// tenant's data, or anything besides the facts, the rules, and the round time it is given, so
// the same events and rules always produce the same usage.
package usage

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net/netip"
	"sort"
	"time"

	"github.com/osac-project/osac-metering/internal/database"
	"github.com/osac-project/osac-metering/internal/rules"
	"github.com/osac-project/osac/proto/metering"
)

// MinBillableRun is the shortest run that bills. A shorter run bills nothing.
const MinBillableRun = time.Second

// Piece is one stretch of expected usage: a rate in micro multipliers over a half-open
// interval. A pending piece marks an open run that is still too short to bill; the round keeps
// watching it instead of committing usage.
type Piece struct {
	Key       database.LedgerKey
	StartTime time.Time
	EndTime   time.Time
	RateMicro int64
	Pending   bool

	// Unit is the meter's unit, carried on heartbeats.
	Unit string

	// Revision is the rule revision that produced the piece.
	Revision string
}

// QuantityMicro returns the piece's quantity in micro units of the meter's unit: the rate
// multiplied by the duration, rounded to the nearest micro unit. A fractional multiplier
// would otherwise re-bill the rounding remainder every round under new IDs. It fails when
// the quantity does not fit in micro units rather than wrap: a wrapped negative bill is the
// worst silent failure.
func (p Piece) QuantityMicro() (int64, error) {
	if p.Pending {
		return 0, nil
	}
	durationMicro := p.EndTime.Sub(p.StartTime).Microseconds()
	quantity := new(big.Rat).Mul(
		big.NewRat(p.RateMicro, 1_000_000), big.NewRat(durationMicro, 1),
	)
	micro, err := RationalToMicro(quantity)
	if err != nil {
		return 0, fmt.Errorf(
			"meter %s of %s %s over %s..%s: %w",
			p.Key.Meter, p.Key.ResourceType, p.Key.ResourceID,
			p.StartTime.Format(time.RFC3339), p.EndTime.Format(time.RFC3339), err,
		)
	}
	return micro, nil
}

// RationalToMicro rounds a rational quantity in micro units to the nearest whole micro unit,
// halves away from zero. The rounds use the same policy for the differences they send. It
// fails when the rounded value does not fit in an int64: big.Int.Int64 is undefined for
// out-of-range values, so converting anyway would wrap. The argument is left untouched.
func RationalToMicro(quantity *big.Rat) (int64, error) {
	sign := int64(1)
	value := quantity
	if quantity.Sign() < 0 {
		sign = -1
		value = new(big.Rat).Neg(quantity)
	}
	num, den := value.Num(), value.Denom()
	// round(num/den) = floor((2*num + den) / (2*den)) for positive values.
	doubledNum := new(big.Int).Lsh(num, 1)
	doubledNum.Add(doubledNum, den)
	doubledDen := new(big.Int).Lsh(den, 1)
	quotient := new(big.Int).Quo(doubledNum, doubledDen)
	if !quotient.IsInt64() {
		return 0, fmt.Errorf("quantity %s does not fit in micro units", value.RatString())
	}
	return sign * quotient.Int64(), nil
}

// OwnerSpec names the field that carries a resource's ownership root, and the type of that
// root. The registry owns the definition; the alias keeps it addressable as usage.OwnerSpec.
type OwnerSpec = metering.OwnerSpec

// RegistryOwners returns the ownership specs of every tracked resource, from the annotations
// on the private protos. Resources without an owner reference are absent. It refuses an
// ambiguous registry: a resource with two owner fields, or a root that different resources
// reference through different fields, would resolve silently to one of them.
func RegistryOwners() (map[string]OwnerSpec, error) {
	resources, err := metering.Resources()
	if err != nil {
		return nil, fmt.Errorf("reading the field registry: %w", err)
	}
	return ownersOf(resources)
}

// ownersOf builds the ownership map over the registry. A resource with two owner fields would
// keep only the last one, because the map keys by owned resource; a root that owns through two
// different fields would make OwnedFieldOf pick one over map iteration. Both are refused.
func ownersOf(resources []metering.Resource) (map[string]OwnerSpec, error) {
	owners := map[string]OwnerSpec{}
	// rootFields remembers the field each root type is referenced through so far.
	rootFields := map[string]string{}
	for _, resource := range resources {
		name := metering.ShortName(string(resource.Message))
		var ownerFields []metering.Field
		for _, field := range resource.Fields {
			if field.OwnerResource != "" {
				ownerFields = append(ownerFields, field)
			}
		}
		if len(ownerFields) > 1 {
			return nil, fmt.Errorf(
				"%s has two owner fields, %s and %s; a resource has exactly one ownership root",
				name, ownerFields[0].Path, ownerFields[1].Path,
			)
		}
		for _, field := range ownerFields {
			if previous, seen := rootFields[field.OwnerResource]; seen && previous != field.Path {
				return nil, fmt.Errorf(
					"%s is owned through both %s and %s; every resource it owns must reference it "+
						"through one field",
					field.OwnerResource, previous, field.Path,
				)
			}
			rootFields[field.OwnerResource] = field.Path
		}
		if len(ownerFields) == 1 {
			owners[name] = OwnerSpec{Field: ownerFields[0].Path, Resource: ownerFields[0].OwnerResource}
		}
	}
	return owners, nil
}

// Root is the ownership root of a resource whose gate the root controls. Facts is empty when
// the root's history is missing: the gate then stays shut, because the data says the resource
// is owned and billing it as standalone would ignore the gate.
type Root struct {
	// Type is the root's resource type.
	Type string

	// ID is the root's resource identifier.
	ID string

	// Facts are the root's history rows.
	Facts []database.Fact
}

// Calculator evaluates rule revisions against history.
type Calculator struct {
	revisions    []rules.Revision
	deploymentID string
	owners       map[string]OwnerSpec
}

// NewCalculator creates a calculator over revisions sorted by effective time. The deployment
// ID is a dimension every meter sends; it is configured per installation and never changes.
// The owners map says which field of each resource carries its ownership root; pass
// RegistryOwners() for the real one.
func NewCalculator(
	revisions []rules.Revision, deploymentID string, owners map[string]OwnerSpec,
) (*Calculator, error) {
	sorted := append([]rules.Revision(nil), revisions...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].EffectiveAt.Before(sorted[j].EffectiveAt)
	})
	return &Calculator{revisions: sorted, deploymentID: deploymentID, owners: owners}, nil
}

// Revisions returns the revisions the calculator evaluates, in effect order.
func (c *Calculator) Revisions() []rules.Revision { return c.revisions }

// OwnerOf returns the resource's ownership root, read from the owner fact in its history.
// ok is false when the resource type has no owner field or the fact is unset.
func (c *Calculator) OwnerOf(resourceType string, facts []database.Fact) (string, string, bool) {
	spec, known := c.owners[resourceType]
	if !known {
		return "", "", false
	}
	for _, entry := range facts {
		if entry.Field == spec.Field && entry.Value != "" {
			return spec.Resource, entry.Value, true
		}
	}
	return "", "", false
}

// OwnedFieldOf returns the history field that carries the owner reference of the resources a
// root of the given type owns. The round uses it to find the resources a changed root owns.
func (c *Calculator) OwnedFieldOf(rootType string) (string, bool) {
	for _, spec := range c.owners {
		if spec.Resource == rootType {
			return spec.Field, true
		}
	}
	return "", false
}

// fact is a dated value of one field.
type fact struct {
	since time.Time
	value string
}

// timeline is one field's values in time order.
type timeline []fact

// valueAt returns the field's value at a moment, and whether it is known.
func (t timeline) valueAt(moment time.Time) (string, bool) {
	result := ""
	known := false
	for _, entry := range t {
		if entry.since.After(moment) {
			break
		}
		result = entry.value
		known = true
	}
	return result, known
}

// Expected computes the resource's expected usage pieces up to the round time. Usage splits
// wherever the state, a dimension, or the rules change, and at every UTC midnight. The root,
// when the resource has one, gates the resource's deferred spans on the root's states.
func (c *Calculator) Expected(
	tenant, resourceType, resourceID string, facts []database.Fact, root *Root, upTo time.Time,
) ([]Piece, error) {
	return c.compute(tenant, resourceType, resourceID, facts, root, upTo, nil)
}

// Since computes only the pieces a round needs to extend a resource's ledger: the pieces
// Expected would produce, restricted to those that end after `from`, plus every piece that
// overlaps a zero-quantity ledger row of the same meter, so a pending run that crosses the
// billable threshold between the two rounds is still billed from its start. A round whose
// resource has no new facts and no rule change needs nothing else: its earlier pieces equal
// the ledger rows the previous round wrote.
func (c *Calculator) Since(
	tenant, resourceType, resourceID string, facts []database.Fact, root *Root,
	from, upTo time.Time, ledger []database.LedgerPiece,
) ([]Piece, error) {
	keep := func(piece Piece) bool {
		if piece.EndTime.After(from) {
			return true
		}
		for _, row := range ledger {
			if row.QuantityMicro != 0 {
				continue
			}
			if row.Key == piece.Key &&
				piece.StartTime.Before(row.EndTime) && piece.EndTime.After(row.StartTime) {
				return true
			}
		}
		return false
	}
	return c.compute(tenant, resourceType, resourceID, facts, root, upTo, keep)
}

// MeterAt returns the meter definition for the resource type and meter name, and the number of
// the revision in effect at the moment: the revision that billed the pieces over an interval
// starting there. It searches back through the revisions that define the meter, so a meter a
// later revision dropped still resolves to the revision that billed it.
func (c *Calculator) MeterAt(resourceType, meter string, at time.Time) (rules.Meter, string, bool) {
	for index := len(c.revisions) - 1; index >= 0; index-- {
		revision := c.revisions[index]
		if revision.EffectiveAt.After(at) {
			continue
		}
		for _, entry := range revision.Meters {
			if entry.Resource == resourceType && entry.Meter == meter {
				return entry, revision.Number, true
			}
		}
	}
	return rules.Meter{}, "", false
}

// compute evaluates the resource's meters. keep, when set, filters the pieces to materialize,
// so a tail extension computes only the latest interval instead of the whole history.
func (c *Calculator) compute(
	tenant, resourceType, resourceID string, facts []database.Fact, root *Root,
	upTo time.Time, keep func(Piece) bool,
) ([]Piece, error) {
	timelines := make(map[string]timeline)
	var creation, deletion time.Time
	for _, entry := range facts {
		if entry.Field == "created" {
			creation = entry.Since
			continue
		}
		if entry.Field == "deleted" {
			deletion = entry.Since
			continue
		}
		timelines[entry.Field] = append(timelines[entry.Field], fact{entry.Since, entry.Value})
	}
	for field := range timelines {
		sort.Slice(timelines[field], func(i, j int) bool {
			return timelines[field][i].since.Before(timelines[field][j].since)
		})
	}
	var rootStates timeline
	var rootDeletion time.Time
	if root != nil {
		for _, entry := range root.Facts {
			if entry.Field == "deleted" {
				rootDeletion = entry.Since
				continue
			}
			if entry.Field == "status.state" {
				rootStates = append(rootStates, fact{entry.Since, entry.Value})
			}
		}
		sort.Slice(rootStates, func(i, j int) bool {
			return rootStates[i].since.Before(rootStates[j].since)
		})
	}
	if creation.IsZero() {
		if len(facts) == 0 {
			// Empty facts are a hold, not corrupt data: a resource whose ownership
			// root is missing stays unbilled until the root's history arrives.
			return nil, nil
		}
		// The fold writes a created fact with every resource's first fact, so facts
		// that survive without one are corrupt. Guessing the start from the earliest
		// survivor would under-bill; refuse instead.
		return nil, fmt.Errorf("%s %s has no creation fact", resourceType, resourceID)
	}
	end := upTo
	if !deletion.IsZero() && deletion.Before(end) {
		end = deletion
	}
	if !creation.Before(end) {
		return nil, nil
	}

	var pieces []Piece
	for index, revision := range c.revisions {
		wStart := revision.EffectiveAt
		if wStart.Before(creation) {
			wStart = creation
		}
		wEnd := end
		if index+1 < len(c.revisions) && c.revisions[index+1].EffectiveAt.Before(wEnd) {
			wEnd = c.revisions[index+1].EffectiveAt
		}
		if !wStart.Before(wEnd) {
			continue
		}
		for _, meter := range revision.Meters {
			if meter.Resource != resourceType {
				continue
			}
			runPieces, err := c.meterPieces(
				meter, tenant, resourceType, resourceID, revision.Number, timelines,
				rootStates, rootDeletion, root, wStart, wEnd, end, upTo, deletion,
			)
			if err != nil {
				return nil, err
			}
			pieces = append(pieces, runPieces...)
		}
	}
	// The loop above emits revision-major, meter-minor. applyRunThreshold stitches
	// contiguous pieces of one meter, so a meter's pieces must be adjacent: sort by
	// meter, then by start time.
	sort.SliceStable(pieces, func(i, j int) bool {
		if pieces[i].Key.Meter != pieces[j].Key.Meter {
			return pieces[i].Key.Meter < pieces[j].Key.Meter
		}
		return pieces[i].StartTime.Before(pieces[j].StartTime)
	})
	pieces = applyRunThreshold(pieces, upTo)
	if keep == nil {
		return pieces, nil
	}
	kept := pieces[:0]
	for _, piece := range pieces {
		if keep(piece) {
			kept = append(kept, piece)
		}
	}
	return kept, nil
}

// meterPieces computes one meter's pieces inside its revision's window. Runs are computed over
// the resource's whole life under the meter's own rules, so a run that spans a revision
// boundary bills its earlier part under the revision that was in effect then. Every piece of
// a run still open at upTo carries Pending as a carrier for applyRunThreshold, which applies
// the billable-run threshold: only the pieces clipped to each revision's window are ever
// emitted, so the run's unclipped length must not decide billability.
func (c *Calculator) meterPieces(
	meter rules.Meter,
	tenant, resourceType, resourceID, revisionNumber string,
	timelines map[string]timeline,
	rootStates timeline, rootDeletion time.Time, root *Root,
	wStart, wEnd, end, upTo time.Time,
	deletion time.Time,
) ([]Piece, error) {
	runs := billableRuns(meter, timelines, end, deletion)
	var pieces []Piece
	for _, r := range runs {
		if !c.gateOpen(meter, resourceType, revisionNumber, timelines, deletion,
			rootStates, rootDeletion, root, r) {
			continue
		}
		start := r.start
		if start.Before(wStart) {
			start = wStart
		}
		stop := r.end
		if stop.After(wEnd) {
			stop = wEnd
		}
		if !start.Before(stop) {
			continue
		}
		// A run that reaches upTo without closing can still grow in a later round.
		runOpen := !r.closed && r.end.Equal(upTo)
		for _, span := range splitByFieldChangesAndMidnights(meter, resourceType, timelines, start, stop) {
			piece, err := c.piece(
				meter, tenant, resourceType, resourceID, revisionNumber, timelines,
				span[0], span[1], runOpen,
			)
			if err != nil {
				return nil, err
			}
			pieces = append(pieces, piece)
		}
	}
	return pieces, nil
}

// applyRunThreshold enforces MinBillableRun on the quantity each meter will actually emit.
// The pieces of one meter stitch into a stretch wherever they are contiguous, including
// across a revision boundary: a run billable under consecutive revisions is one stretch, and
// its pieces on both sides of the boundary count once toward the threshold. A stretch
// shorter than the minimum bills nothing. A short stretch that is still open at the round
// time collapses into one pending piece, so the round keeps watching it and bills it from
// its start once it crosses the threshold.
// watching it and bills it from its start once it crosses the threshold.
func applyRunThreshold(pieces []Piece, upTo time.Time) []Piece {
	// Group the pieces into contiguous stretches of one meter.
	var stretches [][]Piece
	for _, piece := range pieces {
		last := len(stretches) - 1
		if last >= 0 && stretches[last][0].Key.Meter == piece.Key.Meter &&
			stretches[last][len(stretches[last])-1].EndTime.Equal(piece.StartTime) {
			stretches[last] = append(stretches[last], piece)
			continue
		}
		stretches = append(stretches, []Piece{piece})
	}

	var result []Piece
	for _, stretch := range stretches {
		var length time.Duration
		for _, piece := range stretch {
			length += piece.EndTime.Sub(piece.StartTime)
		}
		last := stretch[len(stretch)-1]
		switch {
		case length >= MinBillableRun:
			// The carrier flag only marked the run open; a committed piece of a
			// stretch that passed the threshold bills usage.
			for _, piece := range stretch {
				piece.Pending = false
				result = append(result, piece)
			}
		case last.Pending && last.EndTime.Equal(upTo):
			// Still open and still short: one pending piece keeps the run watched.
			first := stretch[0]
			result = append(result, Piece{
				Key: first.Key, StartTime: first.StartTime, EndTime: upTo,
				RateMicro: first.RateMicro, Pending: true,
				Unit: first.Unit, Revision: first.Revision,
			})
		}
	}
	return result
}

// run is a maximal stretch during which the meter is continuously billable.
type run struct {
	start  time.Time
	end    time.Time
	closed bool
}

// billableRuns builds the meter's runs over the resource's life. A run ends at a non-billable
// state or condition, at the resource's deletion, or at the end of what is known; only the
// last two leave it open.
func billableRuns(
	meter rules.Meter, timelines map[string]timeline, end, deletion time.Time,
) []run {
	states := timelines["status.state"]
	if len(states) == 0 || states[0].since.Compare(end) >= 0 {
		return nil
	}
	billableStates := make(map[string]bool, len(meter.Billable.States))
	for _, state := range meter.Billable.States {
		billableStates[state] = true
	}

	// A run breaks wherever the state changes or a condition field changes, because either
	// can end billability.
	var points []time.Time
	for _, state := range states {
		if state.since.Before(end) {
			points = append(points, state.since)
		}
	}
	for _, path := range conditionPaths(meter) {
		for _, entry := range timelines[path] {
			if entry.since.Before(end) {
				points = append(points, entry.since)
			}
		}
	}
	points = append(points, end)
	sort.Slice(points, func(i, j int) bool { return points[i].Before(points[j]) })
	unique := points[:1]
	for _, point := range points[1:] {
		if point.After(unique[len(unique)-1]) {
			unique = append(unique, point)
		}
	}
	points = unique

	var runs []run
	for i := 1; i < len(points); i++ {
		start := points[i-1]
		stop := points[i]
		state, known := states.valueAt(start)
		billable := known && billableStates[state] &&
			conditionsHold(meter, meter.Resource, timelines, start)
		if !billable {
			continue
		}
		if len(runs) > 0 && runs[len(runs)-1].end.Equal(start) {
			runs[len(runs)-1].end = stop
		} else {
			runs = append(runs, run{start: start, end: stop})
		}
		runs[len(runs)-1].closed = stop.Before(end) ||
			(!deletion.IsZero() && stop.Equal(deletion))
	}
	return runs
}

// gateOpen reports whether the run's gate is open: a state that confirms the run appeared on
// the gate's timeline strictly before the run ends. There is no lower bound, so a run that
// began before the gate opened bills retroactively once it opens, and a run that closed before
// the gate opened never bills. Whose timeline the gate reads depends on the meter: a resource
// with an ownership root inherits the root's gate, which the root's own meters define in the
// same revision, unless the meter says self. A root with an empty gate list, or a revision
// that defines no meters for the root, bills without a gate.
func (c *Calculator) gateOpen(
	meter rules.Meter, resourceType, revisionNumber string,
	timelines map[string]timeline, deletion time.Time,
	rootStates timeline, rootDeletion time.Time, root *Root,
	r run,
) bool {
	list := meter.Billable.TurnsBillableOnce
	states := timelines["status.state"]
	clamp := deletion
	if root != nil && meter.Billable.TurnsBillableOnceFrom != "self" {
		list = c.rootGate(revisionNumber, root.Type)
		states = rootStates
		clamp = rootDeletion
	}
	if len(list) == 0 {
		return true
	}
	for _, entry := range states {
		if !entry.since.Before(r.end) {
			break
		}
		if !clamp.IsZero() && !entry.since.Before(clamp) {
			break
		}
		for _, state := range list {
			if entry.value == state {
				return true
			}
		}
	}
	return false
}

// rootGate returns the gate the root passes to the resources it owns: the states the root's
// own meters confirm on, in the revision that bills the owned resource. The rules loader has
// already rejected a root whose meters disagree.
func (c *Calculator) rootGate(revisionNumber, rootType string) []string {
	for _, revision := range c.revisions {
		if revision.Number != revisionNumber {
			continue
		}
		for _, meter := range revision.Meters {
			if meter.Resource == rootType {
				return meter.Billable.TurnsBillableOnce
			}
		}
		return nil
	}
	return nil
}

// conditionsHold evaluates the meter's field conditions at a moment.
func conditionsHold(
	meter rules.Meter, resource string, timelines map[string]timeline, moment time.Time,
) bool {
	for name, expected := range meter.Billable.Fields {
		path, _ := rules.DimensionPath(resource, name)
		value, known := timelines[path].valueAt(moment)
		if expected == "set" {
			if !known || value == "" {
				return false
			}
			continue
		}
		if !known || value != expected {
			return false
		}
	}
	return true
}

// conditionPaths resolves the meter's condition names to the history fields they read.
func conditionPaths(meter rules.Meter) []string {
	var paths []string
	for name := range meter.Billable.Fields {
		path, _ := rules.DimensionPath(meter.Resource, name)
		paths = append(paths, path)
	}
	return paths
}

// splitByFieldChangesAndMidnights splits a billable interval wherever a dimension or
// multiplier field changes and at every UTC midnight, because the billing provider dates
// each heartbeat by a single timestamp, and a piece's identity and rate are read at its
// start: a change left unsplit would bill the rest of the run at the stale value.
func splitByFieldChangesAndMidnights(
	meter rules.Meter, resourceType string, timelines map[string]timeline, start, stop time.Time,
) [][2]time.Time {
	fields := make([]string, 0, len(meter.Dimensions)+1)
	fields = append(fields, meter.Dimensions...)
	if meter.Multiplier != "" {
		fields = append(fields, meter.Multiplier)
	}
	points := []time.Time{start, stop}
	for _, name := range fields {
		path, _ := rules.DimensionPath(resourceType, name)
		for _, entry := range timelines[path] {
			if entry.since.After(start) && entry.since.Before(stop) {
				points = append(points, entry.since)
			}
		}
	}
	for midnight := NextMidnight(start); midnight.Before(stop); midnight = midnight.AddDate(0, 0, 1) {
		points = append(points, midnight)
	}
	sort.Slice(points, func(i, j int) bool { return points[i].Before(points[j]) })
	var spans [][2]time.Time
	for i := 1; i < len(points); i++ {
		if points[i].After(points[i-1]) {
			spans = append(spans, [2]time.Time{points[i-1], points[i]})
		}
	}
	if len(spans) == 0 {
		spans = append(spans, [2]time.Time{start, stop})
	}
	return spans
}

// NextMidnight returns the first UTC midnight strictly after the moment.
func NextMidnight(moment time.Time) time.Time {
	utc := moment.UTC()
	return time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
}

// piece builds one usage piece with its dimensions and multiplier at the interval's start.
func (c *Calculator) piece(
	meter rules.Meter,
	tenant, resourceType, resourceID, revisionNumber string,
	timelines map[string]timeline,
	start, stop time.Time,
	pending bool,
) (Piece, error) {
	dimensions := map[string]string{
		"deployment": c.deploymentID,
		"tenant":     tenant,
		"meter_type": meter.Unit,
	}
	for _, field := range rules.IdentityFields {
		if value, known := timelines[field].valueAt(start); known && value != "" {
			dimensions[dimensionNameOf(field)] = value
		}
	}
	for _, dimension := range meter.Dimensions {
		path, _ := rules.DimensionPath(resourceType, dimension)
		if value, known := timelines[path].valueAt(start); known && value != "" {
			if dimension == ipFamilyDimension {
				// The IP family derives from the tracked address: the contract
				// records the address, the billing contract names the family.
				value = ipFamilyOf(value)
			}
			if value != "" {
				dimensions[dimension] = value
			}
		}
	}
	encoded, err := json.Marshal(dimensions)
	if err != nil {
		return Piece{}, fmt.Errorf("encoding dimensions: %w", err)
	}

	rate := int64(1_000_000)
	if meter.Multiplier != "" {
		path, _ := rules.DimensionPath(resourceType, meter.Multiplier)
		value, known := timelines[path].valueAt(start)
		if !known || value == "" {
			rate = 0
		} else {
			rate, err = parseMicro(value)
			if err != nil {
				return Piece{}, fmt.Errorf(
					"meter %s of %s multiplies by %q, which is not a number: %w",
					meter.Meter, meter.Resource, value, err,
				)
			}
		}
	}

	return Piece{
		Key: database.LedgerKey{
			ResourceType: resourceType,
			ResourceID:   resourceID,
			Meter:        meter.Meter,
			Component:    meter.EffectiveComponent(),
			Dimensions:   string(encoded),
		},
		StartTime: start,
		EndTime:   stop,
		RateMicro: rate,
		Pending:   pending,
		Unit:      meter.Unit,
		Revision:  revisionNumber,
	}, nil
}

// ipFamilyDimension names the dimension whose value derives from parsing an IP address.
const ipFamilyDimension = "ip_family"

// ipFamilyOf parses an address into its IP family, or an empty string when it is not an
// address.
func ipFamilyOf(address string) string {
	parsed, err := netip.ParseAddr(address)
	if err != nil {
		return ""
	}
	if parsed.Is6() {
		return "ipv6"
	}
	return "ipv4"
}

// dimensionNameOf maps an identity field to its dimension name.
func dimensionNameOf(field string) string {
	switch field {
	case "metadata.project":
		return "project"
	case "spec.catalog_item":
		return "catalog"
	case "spec.template":
		return "template"
	default:
		return field
	}
}

// parseMicro parses a decimal number into micro units. It fails when the number needs more
// than six decimals, is negative, or does not fit in micro units.
func parseMicro(text string) (int64, error) {
	rational, ok := new(big.Rat).SetString(text)
	if !ok {
		return 0, fmt.Errorf("%q is not a decimal number", text)
	}
	scaled := new(big.Rat).Mul(rational, big.NewRat(1_000_000, 1))
	if !scaled.IsInt() {
		return 0, fmt.Errorf("%q needs more than six decimals", text)
	}
	if scaled.Sign() < 0 {
		return 0, fmt.Errorf("%q is negative", text)
	}
	if !scaled.Num().IsInt64() {
		return 0, fmt.Errorf("%q does not fit in micro units", text)
	}
	return scaled.Num().Int64(), nil
}
