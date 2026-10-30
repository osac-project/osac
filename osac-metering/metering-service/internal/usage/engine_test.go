/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package usage_test

import (
	"fmt"
	"math"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/osac-project/osac-metering/internal/database"
	"github.com/osac-project/osac-metering/internal/rules"
	"github.com/osac-project/osac-metering/internal/testenv"
	"github.com/osac-project/osac-metering/internal/usage"
)

var base = time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)

func defaultCalculator(t *testing.T) *usage.Calculator {
	t.Helper()
	revision := testenv.DefaultRevision(t)
	owners, err := usage.RegistryOwners()
	if err != nil {
		t.Fatalf("reading owners: %v", err)
	}
	calculator, err := usage.NewCalculator([]rules.Revision{revision}, "us-east", owners)
	if err != nil {
		t.Fatalf("creating calculator: %v", err)
	}
	return calculator
}

func fact(resource, id, field string, since time.Time, value string) database.Fact {
	return database.Fact{
		ResourceType: resource, ResourceID: id, Field: field, Since: since, Value: value,
	}
}

func created(resource, id string, at time.Time) database.Fact {
	return fact(resource, id, "created", at, "true")
}

func state(resource, id string, since time.Time, value string) database.Fact {
	return fact(resource, id, "status.state", since, value)
}

// summarize renders pieces as comparable text.
func summarize(pieces []usage.Piece) []string {
	var lines []string
	for _, piece := range pieces {
		quantity, err := piece.QuantityMicro()
		if err != nil {
			lines = append(lines, fmt.Sprintf(
				"%s %s %s..%s rate=%d micro=ERROR(%v) pending=%v dims=%s",
				piece.Key.ResourceType, piece.Key.Meter,
				piece.StartTime.Format("15:04:05.000000"), piece.EndTime.Format("15:04:05.000000"),
				piece.RateMicro, err, piece.Pending, piece.Key.Dimensions,
			))
			continue
		}
		lines = append(lines, fmt.Sprintf(
			"%s %s %s..%s rate=%d micro=%d pending=%v dims=%s",
			piece.Key.ResourceType, piece.Key.Meter,
			piece.StartTime.Format("15:04:05.000000"), piece.EndTime.Format("15:04:05.000000"),
			piece.RateMicro, quantity, piece.Pending, piece.Key.Dimensions,
		))
	}
	return lines
}

func expectPieces(
	t *testing.T, calculator *usage.Calculator, facts []database.Fact, upTo time.Time, want []string,
) {
	t.Helper()
	pieces, err := calculator.Expected("acme", facts[0].ResourceType, facts[0].ResourceID, facts, nil, upTo)
	if err != nil {
		t.Fatalf("computing usage: %v", err)
	}
	got := summarize(pieces)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("pieces mismatch:\ngot:\n%s\nwant:\n%s", join(got), join(want))
	}
}

// TestRationalToMicroLeavesItsArgumentAlone checks that negation for rounding works on a
// copy: a caller that reuses the quantity must not see its sign flip.
func TestRationalToMicroLeavesItsArgumentAlone(t *testing.T) {
	quantity := big.NewRat(-3, 2)
	got, err := usage.RationalToMicro(quantity)
	if err != nil {
		t.Fatalf("rounding -1.5 micro units: %v", err)
	}
	if got != -2 {
		t.Errorf("-1.5 micro units must round to -2, got %d", got)
	}
	if quantity.Cmp(big.NewRat(-3, 2)) != 0 {
		t.Errorf("RationalToMicro mutated its argument to %s", quantity.RatString())
	}
}

// TestRationalToMicroRejectsUnrepresentableQuantities checks the overflow guard: a value
// beyond int64 micro units must return an error, because big.Int.Int64 is undefined there
// and converting anyway wraps, which bills negative usage for a positive allocation.
func TestRationalToMicroRejectsUnrepresentableQuantities(t *testing.T) {
	// 200,000,000 GiB-seconds over one day, in micro units.
	for _, text := range []string{
		"10080000000000000000", // 1.008e19, past math.MaxInt64
		"-10080000000000000000",
		"9223372036854775808", // math.MaxInt64 + 1
	} {
		quantity, ok := new(big.Rat).SetString(text)
		if !ok {
			t.Fatalf("%q is not a number", text)
		}
		if got, err := usage.RationalToMicro(quantity); err == nil {
			t.Errorf("RationalToMicro(%s) = %d, want an error: the value overflows micro units",
				text, got)
		}
	}
	// The largest representable magnitude still rounds.
	quantity, _ := new(big.Rat).SetString("9223372036854775807")
	if got, err := usage.RationalToMicro(quantity); err != nil || got != math.MaxInt64 {
		t.Errorf("RationalToMicro(MaxInt64 micro units) = %d, %v; want %d, no error",
			got, err, int64(math.MaxInt64))
	}
}

// TestVolumeCapacityRejectsBadMultipliers checks that a negative or out-of-range multiplier
// fails the evaluation instead of wrapping into a wrong rate.
func TestVolumeCapacityRejectsBadMultipliers(t *testing.T) {
	calculator := defaultCalculator(t)
	volume := []database.Fact{
		created("Volume", "vol-1", at(0)),
		state("Volume", "vol-1", at(0), "AVAILABLE"),
		fact("Volume", "vol-1", "status.protocol", at(0), "BLOCK"),
		fact("Volume", "vol-1", "status.vendor_volume_id", at(0), "vend-9"),
	}
	for _, value := range []string{"-5", "9223372036856"} {
		facts := append(append([]database.Fact(nil), volume...),
			fact("Volume", "vol-1", "status.provisioned_size_gib", at(0), value),
		)
		if _, err := calculator.Expected("acme", "Volume", "vol-1", facts, nil, at(time.Minute)); err == nil {
			t.Errorf("a multiplier of %s must fail the evaluation", value)
		}
	}
}

func join(lines []string) string {
	out := ""
	for _, line := range lines {
		out += line + "\n"
	}
	return out
}

// quantityMicro reads a piece's quantity, failing the test on an unrepresentable value.
func quantityMicro(t *testing.T, piece usage.Piece) int64 {
	t.Helper()
	quantity, err := piece.QuantityMicro()
	if err != nil {
		t.Fatalf("reading quantity: %v", err)
	}
	return quantity
}

func at(offset time.Duration) time.Time { return base.Add(offset) }

func TestComputeInstanceBillsOnlyRunning(t *testing.T) {
	calculator := defaultCalculator(t)
	facts := []database.Fact{
		created("ComputeInstance", "vm-7", at(0)),
		state("ComputeInstance", "vm-7", at(0), "STARTING"),
		state("ComputeInstance", "vm-7", at(10*time.Second), "RUNNING"),
		fact("ComputeInstance", "vm-7", "status.instance_type", at(0), "small"),
		fact("ComputeInstance", "vm-7", "metadata.project", at(0), "web"),
	}
	expectPieces(t, calculator, facts, at(70*time.Second), []string{
		"ComputeInstance compute 10:00:10.000000..10:01:10.000000 rate=1000000 micro=60000000 " +
			"pending=false dims={\"deployment\":\"us-east\",\"instance_type\":\"small\"," +
			"\"meter_type\":\"second\",\"project\":\"web\",\"tenant\":\"acme\"}",
	})
}

func TestComputeInstanceStopsAtNonRunningStates(t *testing.T) {
	calculator := defaultCalculator(t)
	for _, terminal := range []string{"STOPPED", "PAUSED", "FAILED", "DELETING"} {
		facts := []database.Fact{
			created("ComputeInstance", "vm-7", at(0)),
			state("ComputeInstance", "vm-7", at(0), "RUNNING"),
			state("ComputeInstance", "vm-7", at(30*time.Second), terminal),
		}
		expectPieces(t, calculator, facts, at(time.Minute), []string{
			"ComputeInstance compute 10:00:00.000000..10:00:30.000000 rate=1000000 micro=30000000 " +
				"pending=false dims={\"deployment\":\"us-east\",\"meter_type\":\"second\"," +
				"\"tenant\":\"acme\"}",
		})
	}
}

func TestRunUnderOneSecondBillsNothing(t *testing.T) {
	calculator := defaultCalculator(t)
	facts := []database.Fact{
		created("ComputeInstance", "vm-7", at(0)),
		state("ComputeInstance", "vm-7", at(200*time.Millisecond), "RUNNING"),
		state("ComputeInstance", "vm-7", at(600*time.Millisecond), "STOPPED"),
	}
	expectPieces(t, calculator, facts, at(time.Minute), nil)
}

func TestOpenRunUnderOneSecondStaysPending(t *testing.T) {
	calculator := defaultCalculator(t)
	facts := []database.Fact{
		created("ComputeInstance", "vm-7", at(0)),
		state("ComputeInstance", "vm-7", at(200*time.Millisecond), "RUNNING"),
	}
	// The run is open and 400 ms long at the round time: nothing is billable yet, and the
	// piece is pending so the next round keeps watching it.
	expectPieces(t, calculator, facts, at(600*time.Millisecond), []string{
		"ComputeInstance compute 10:00:00.200000..10:00:00.600000 rate=1000000 micro=0 " +
			"pending=true dims={\"deployment\":\"us-east\",\"meter_type\":\"second\"," +
			"\"tenant\":\"acme\"}",
	})
	// Once the run crosses one second it bills from its start.
	expectPieces(t, calculator, facts, at(2*time.Second), []string{
		"ComputeInstance compute 10:00:00.200000..10:00:02.000000 rate=1000000 micro=1800000 " +
			"pending=false dims={\"deployment\":\"us-east\",\"meter_type\":\"second\"," +
			"\"tenant\":\"acme\"}",
	})
}

func TestDurationIsExactToTheMicrosecond(t *testing.T) {
	calculator := defaultCalculator(t)
	facts := []database.Fact{
		created("ComputeInstance", "vm-7", at(0)),
		state("ComputeInstance", "vm-7", at(200*time.Millisecond), "RUNNING"),
		state("ComputeInstance", "vm-7", at(2800*time.Millisecond), "STOPPED"),
	}
	expectPieces(t, calculator, facts, at(time.Minute), []string{
		"ComputeInstance compute 10:00:00.200000..10:00:02.800000 rate=1000000 micro=2600000 " +
			"pending=false dims={\"deployment\":\"us-east\",\"meter_type\":\"second\"," +
			"\"tenant\":\"acme\"}",
	})
}

func TestDimensionChangeSplitsTheRun(t *testing.T) {
	calculator := defaultCalculator(t)
	facts := []database.Fact{
		created("ComputeInstance", "vm-7", at(0)),
		state("ComputeInstance", "vm-7", at(200*time.Millisecond), "RUNNING"),
		state("ComputeInstance", "vm-7", at(2800*time.Millisecond), "STOPPED"),
		fact("ComputeInstance", "vm-7", "status.instance_type", at(200*time.Millisecond), "small"),
		fact("ComputeInstance", "vm-7", "status.instance_type", at(1500*time.Millisecond), "large"),
	}
	expectPieces(t, calculator, facts, at(time.Minute), []string{
		"ComputeInstance compute 10:00:00.200000..10:00:01.500000 rate=1000000 micro=1300000 " +
			"pending=false dims={\"deployment\":\"us-east\",\"instance_type\":\"small\"," +
			"\"meter_type\":\"second\",\"tenant\":\"acme\"}",
		"ComputeInstance compute 10:00:01.500000..10:00:02.800000 rate=1000000 micro=1300000 " +
			"pending=false dims={\"deployment\":\"us-east\",\"instance_type\":\"large\"," +
			"\"meter_type\":\"second\",\"tenant\":\"acme\"}",
	})
}

func TestMidnightSplitsThePiece(t *testing.T) {
	calculator := defaultCalculator(t)
	midnight := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	facts := []database.Fact{
		created("ComputeInstance", "vm-7", midnight.Add(-time.Minute)),
		state("ComputeInstance", "vm-7", midnight.Add(-time.Minute), "RUNNING"),
	}
	expectPieces(t, calculator, facts, midnight.Add(time.Minute), []string{
		"ComputeInstance compute 23:59:00.000000..00:00:00.000000 rate=1000000 micro=60000000 " +
			"pending=false dims={\"deployment\":\"us-east\",\"meter_type\":\"second\"," +
			"\"tenant\":\"acme\"}",
		"ComputeInstance compute 00:00:00.000000..00:01:00.000000 rate=1000000 micro=60000000 " +
			"pending=false dims={\"deployment\":\"us-east\",\"meter_type\":\"second\"," +
			"\"tenant\":\"acme\"}",
	})
}

func TestNothingBillsAfterDeletion(t *testing.T) {
	calculator := defaultCalculator(t)
	facts := []database.Fact{
		created("ComputeInstance", "vm-7", at(0)),
		state("ComputeInstance", "vm-7", at(0), "RUNNING"),
		// A late state stamp after the deletion must not extend billing.
		state("ComputeInstance", "vm-7", at(2*time.Minute), "RUNNING"),
		fact("ComputeInstance", "vm-7", "deleted", at(time.Minute), "true"),
	}
	expectPieces(t, calculator, facts, at(5*time.Minute), []string{
		"ComputeInstance compute 10:00:00.000000..10:01:00.000000 rate=1000000 micro=60000000 " +
			"pending=false dims={\"deployment\":\"us-east\",\"meter_type\":\"second\"," +
			"\"tenant\":\"acme\"}",
	})
}

func TestClusterBillsInstallationRetroactivelyOnReady(t *testing.T) {
	calculator := defaultCalculator(t)
	facts := []database.Fact{
		created("Cluster", "c-1", at(0)),
		state("Cluster", "c-1", at(0), "PROGRESSING"),
		state("Cluster", "c-1", at(5*time.Minute), "READY"),
		fact("Cluster", "c-1", "spec.version", at(0), "4.18"),
	}
	expectPieces(t, calculator, facts, at(10*time.Minute), []string{
		"Cluster control_plane 10:00:00.000000..10:10:00.000000 rate=1000000 micro=600000000 " +
			"pending=false dims={\"deployment\":\"us-east\",\"meter_type\":\"second\"," +
			"\"tenant\":\"acme\",\"version\":\"4.18\"}",
	})
}

func TestClusterThatNeverReachesReadyBillsNothing(t *testing.T) {
	calculator := defaultCalculator(t)
	for _, terminal := range []string{"FAILED", "DELETING"} {
		facts := []database.Fact{
			created("Cluster", "c-1", at(0)),
			state("Cluster", "c-1", at(0), "PROGRESSING"),
			state("Cluster", "c-1", at(5*time.Minute), terminal),
		}
		expectPieces(t, calculator, facts, at(10*time.Minute), nil)
	}
	// Deletion during provisioning also bills nothing.
	facts := []database.Fact{
		created("Cluster", "c-1", at(0)),
		state("Cluster", "c-1", at(0), "PROGRESSING"),
		fact("Cluster", "c-1", "deleted", at(2*time.Minute), "true"),
	}
	expectPieces(t, calculator, facts, at(10*time.Minute), nil)
}

func TestClusterBillsAgainAfterFailure(t *testing.T) {
	calculator := defaultCalculator(t)
	facts := []database.Fact{
		created("Cluster", "c-1", at(0)),
		state("Cluster", "c-1", at(0), "PROGRESSING"),
		state("Cluster", "c-1", at(time.Minute), "FAILED"),
		state("Cluster", "c-1", at(2*time.Minute), "PROGRESSING"),
		state("Cluster", "c-1", at(3*time.Minute), "READY"),
	}
	expectPieces(t, calculator, facts, at(4*time.Minute), []string{
		// The first PROGRESSING minute is never billed; the second span is, retroactively.
		"Cluster control_plane 10:02:00.000000..10:04:00.000000 rate=1000000 micro=120000000 " +
			"pending=false dims={\"deployment\":\"us-east\",\"meter_type\":\"second\"," +
			"\"tenant\":\"acme\"}",
	})
}

func TestClusterKeepsBillingThroughAnUpgrade(t *testing.T) {
	// An upgrade takes a READY cluster back into PROGRESSING. The gate opened at the
	// first READY and never closes, so the whole timeline bills with no gap: an
	// upgrade must not stop the meter.
	calculator := defaultCalculator(t)
	facts := []database.Fact{
		created("Cluster", "c-1", at(0)),
		state("Cluster", "c-1", at(0), "PROGRESSING"),
		state("Cluster", "c-1", at(time.Minute), "READY"),
		state("Cluster", "c-1", at(2*time.Minute), "PROGRESSING"),
		state("Cluster", "c-1", at(3*time.Minute), "READY"),
	}
	expectPieces(t, calculator, facts, at(4*time.Minute), []string{
		"Cluster control_plane 10:00:00.000000..10:04:00.000000 rate=1000000 micro=240000000 " +
			"pending=false dims={\"deployment\":\"us-east\",\"meter_type\":\"second\"," +
			"\"tenant\":\"acme\"}",
	})
}

func TestBareMetalAllocationAndConsumption(t *testing.T) {
	calculator := defaultCalculator(t)
	facts := []database.Fact{
		created("BareMetalInstance", "bm-1", at(0)),
		state("BareMetalInstance", "bm-1", at(0), "PROVISIONING"),
		state("BareMetalInstance", "bm-1", at(time.Minute), "STARTING"),
		state("BareMetalInstance", "bm-1", at(2*time.Minute), "RUNNING"),
		state("BareMetalInstance", "bm-1", at(3*time.Minute), "STOPPED"),
		state("BareMetalInstance", "bm-1", at(4*time.Minute), "DELETING"),
		fact("BareMetalInstance", "bm-1", "spec.instance_type", at(0), "m7a"),
	}
	expectPieces(t, calculator, facts, at(5*time.Minute), []string{
		"BareMetalInstance allocation 10:01:00.000000..10:05:00.000000 rate=1000000 " +
			"micro=240000000 pending=false dims={\"deployment\":\"us-east\"," +
			"\"instance_type\":\"m7a\",\"meter_type\":\"second\",\"tenant\":\"acme\"}",
		"BareMetalInstance consumption 10:02:00.000000..10:03:00.000000 rate=1000000 " +
			"micro=60000000 pending=false dims={\"deployment\":\"us-east\"," +
			"\"instance_type\":\"m7a\",\"meter_type\":\"second\",\"tenant\":\"acme\"}",
	})
}

func TestVolumeCapacityMultipliesByProvisionedGib(t *testing.T) {
	calculator := defaultCalculator(t)
	facts := []database.Fact{
		created("Volume", "vol-1", at(0)),
		state("Volume", "vol-1", at(0), "CREATING"),
		state("Volume", "vol-1", at(time.Minute), "AVAILABLE"),
		state("Volume", "vol-1", at(3*time.Minute), "DELETING"),
		fact("Volume", "vol-1", "status.vendor_volume_id", at(0), "vend-9"),
		fact("Volume", "vol-1", "status.protocol", at(0), "BLOCK"),
		fact("Volume", "vol-1", "status.provisioned_size_gib", at(0), "100"),
		fact("Volume", "vol-1", "spec.storage_tier", at(0), "gold"),
	}
	expectPieces(t, calculator, facts, at(4*time.Minute), []string{
		"Volume capacity 10:01:00.000000..10:04:00.000000 rate=100000000 micro=18000000000 " +
			"pending=false dims={\"deployment\":\"us-east\",\"meter_type\":\"gibibyte_second\"," +
			"\"storage_tier\":\"gold\",\"tenant\":\"acme\"}",
	})
}

func TestVolumeWithoutVendorIDOrBlockProtocolBillsNothing(t *testing.T) {
	calculator := defaultCalculator(t)
	base := []database.Fact{
		created("Volume", "vol-1", at(0)),
		state("Volume", "vol-1", at(time.Minute), "AVAILABLE"),
		fact("Volume", "vol-1", "status.provisioned_size_gib", at(0), "100"),
	}
	noVendor := append(append([]database.Fact{}, base...),
		fact("Volume", "vol-1", "status.protocol", at(0), "BLOCK"))
	expectPieces(t, calculator, noVendor, at(3*time.Minute), nil)
	nfs := append(append([]database.Fact{}, base...),
		fact("Volume", "vol-1", "status.vendor_volume_id", at(0), "vend-9"),
		fact("Volume", "vol-1", "status.protocol", at(0), "NFS"))
	expectPieces(t, calculator, nfs, at(3*time.Minute), nil)
}

func TestVolumeDeletedBeforeAvailableBillsNothing(t *testing.T) {
	calculator := defaultCalculator(t)
	facts := []database.Fact{
		created("Volume", "vol-1", at(0)),
		state("Volume", "vol-1", at(0), "PENDING"),
		state("Volume", "vol-1", at(time.Minute), "DELETING"),
		fact("Volume", "vol-1", "status.vendor_volume_id", at(0), "vend-9"),
		fact("Volume", "vol-1", "status.protocol", at(0), "BLOCK"),
		fact("Volume", "vol-1", "status.provisioned_size_gib", at(0), "100"),
	}
	expectPieces(t, calculator, facts, at(3*time.Minute), nil)
}

func TestExternalIPAllocationDimensions(t *testing.T) {
	calculator := defaultCalculator(t)
	facts := []database.Fact{
		created("ExternalIP", "ip-1", at(0)),
		state("ExternalIP", "ip-1", at(0), "PENDING"),
		state("ExternalIP", "ip-1", at(time.Minute), "ALLOCATED"),
		fact("ExternalIP", "ip-1", "spec.pool", at(0), "pool-a"),
		// The tracked address, from which the family derives.
		fact("ExternalIP", "ip-1", "status.address", at(time.Minute), "2001:db8::1"),
		fact("ExternalIP", "ip-1", "status.attached", at(time.Minute), "true"),
	}
	expectPieces(t, calculator, facts, at(2*time.Minute), []string{
		"ExternalIP allocation 10:01:00.000000..10:02:00.000000 rate=1000000 micro=60000000 " +
			"pending=false dims={\"attached\":\"true\",\"deployment\":\"us-east\"," +
			"\"ip_family\":\"ipv6\",\"meter_type\":\"second\",\"pool\":\"pool-a\"," +
			"\"tenant\":\"acme\"}",
	})
}

// TestExternalIPsAttachedToSameNamedTargetsBillStandalone checks the decision that attached
// resources bill under their own identity: two external IPs attached to two different
// instances that share a name bill as two ordinary resources, each with its own dimensions,
// and no attribution dimension records the attachment.
func TestExternalIPsAttachedToSameNamedTargetsBillStandalone(t *testing.T) {
	calculator := defaultCalculator(t)
	ip := func(id, pool, address string) []database.Fact {
		return []database.Fact{
			created("ExternalIP", id, at(0)),
			state("ExternalIP", id, at(0), "PENDING"),
			state("ExternalIP", id, at(time.Minute), "ALLOCATED"),
			fact("ExternalIP", id, "spec.pool", at(0), pool),
			fact("ExternalIP", id, "status.address", at(time.Minute), address),
			fact("ExternalIP", id, "status.attached", at(time.Minute), "true"),
		}
	}
	// Both IPs are attached to instances named web; the attribution values differ only in
	// the target's id, which the old dimension collapsed into one name-first string.
	ip1 := append(ip("ip-1", "pool-a", "192.0.2.10"),
		fact("ExternalIP", "ip-1", "status.attribution", at(time.Minute), "compute_instance/vm-original/API"))
	ip2 := append(ip("ip-2", "pool-b", "2001:db8::1"),
		fact("ExternalIP", "ip-2", "status.attribution", at(time.Minute), "compute_instance/vm-replacement/API"))

	expectPieces(t, calculator, ip1, at(2*time.Minute), []string{
		"ExternalIP allocation 10:01:00.000000..10:02:00.000000 rate=1000000 micro=60000000 " +
			"pending=false dims={\"attached\":\"true\",\"deployment\":\"us-east\"," +
			"\"ip_family\":\"ipv4\",\"meter_type\":\"second\",\"pool\":\"pool-a\"," +
			"\"tenant\":\"acme\"}",
	})
	expectPieces(t, calculator, ip2, at(2*time.Minute), []string{
		"ExternalIP allocation 10:01:00.000000..10:02:00.000000 rate=1000000 micro=60000000 " +
			"pending=false dims={\"attached\":\"true\",\"deployment\":\"us-east\"," +
			"\"ip_family\":\"ipv6\",\"meter_type\":\"second\",\"pool\":\"pool-b\"," +
			"\"tenant\":\"acme\"}",
	})
}

func TestNATGatewayAllocation(t *testing.T) {
	calculator := defaultCalculator(t)
	facts := []database.Fact{
		created("NATGateway", "nat-1", at(0)),
		state("NATGateway", "nat-1", at(0), "PENDING"),
		state("NATGateway", "nat-1", at(time.Minute), "READY"),
		state("NATGateway", "nat-1", at(2*time.Minute), "DELETING"),
		fact("NATGateway", "nat-1", "spec.virtual_network", at(0), "vn-1"),
		fact("NATGateway", "nat-1", "spec.external_ip", at(0), "ip-1"),
	}
	expectPieces(t, calculator, facts, at(3*time.Minute), []string{
		"NATGateway allocation 10:01:00.000000..10:03:00.000000 rate=1000000 micro=120000000 " +
			"pending=false dims={\"deployment\":\"us-east\",\"external_ip\":\"ip-1\"," +
			"\"meter_type\":\"second\",\"tenant\":\"acme\",\"virtual_network\":\"vn-1\"}",
	})
}

// revisionsCalculator builds a calculator over the default revision plus the given later
// revisions, in effect order.
func revisionsCalculator(t *testing.T, revisions ...rules.Revision) *usage.Calculator {
	t.Helper()
	all := append([]rules.Revision{testenv.DefaultRevision(t)}, revisions...)
	owners, err := usage.RegistryOwners()
	if err != nil {
		t.Fatalf("reading owners: %v", err)
	}
	calculator, err := usage.NewCalculator(all, "us-east", owners)
	if err != nil {
		t.Fatalf("creating calculator: %v", err)
	}
	return calculator
}

// laterRevision parses a revision numbered 0002 effective at the given time.
func laterRevision(t *testing.T, effective time.Time, meters string) rules.Revision {
	t.Helper()
	revision, err := rules.Parse(
		rules.NamePrefix+"0002", effective.Format(time.RFC3339), meters,
	)
	if err != nil {
		t.Fatalf("parsing revision: %v", err)
	}
	return revision
}

// TestRuleActivationDoesNotCountPreviouslyNonBillableTime checks the threshold against
// the quantity a revision actually emits: a revision that starts billing PAUSED must not
// count the PAUSED time that passed before it took effect. The newly billable run is 400 ms,
// and it must bill nothing even though the whole run under the new rules is over a second.
func TestRuleActivationDoesNotCountPreviouslyNonBillableTime(t *testing.T) {
	calculator := revisionsCalculator(t, laterRevision(t, at(time.Minute), `- resource: ComputeInstance
  meter: compute
  unit: second
  billable_when:
    states: [RUNNING, PAUSED]
  dimensions: [instance_type]
`))
	facts := []database.Fact{
		created("ComputeInstance", "vm-1", at(-2*time.Minute)),
		state("ComputeInstance", "vm-1", at(-2*time.Minute), "STARTING"),
		state("ComputeInstance", "vm-1", at(-time.Minute), "RUNNING"),
		state("ComputeInstance", "vm-1", at(0), "PAUSED"),
		state("ComputeInstance", "vm-1", at(time.Minute+400*time.Millisecond), "STOPPED"),
	}
	pieces, err := calculator.Expected("acme", "ComputeInstance", "vm-1", facts, nil, at(2*time.Minute))
	if err != nil {
		t.Fatalf("computing usage: %v", err)
	}
	for _, piece := range pieces {
		if piece.Revision == "0002" && quantityMicro(t, piece) != 0 {
			t.Fatalf(
				"the newly billable run is 400 ms after the revision, want zero under 0002; got %s",
				join(summarize(pieces)),
			)
		}
	}
}

// TestRuleRemovalEndsTheBillableRun checks the threshold when a revision removes a
// meter mid-run: the meter existed for only 400 ms of RUNNING, so it must bill nothing even
// though the physical run continues past the removal.
func TestRuleRemovalEndsTheBillableRun(t *testing.T) {
	calculator := revisionsCalculator(t, laterRevision(t, at(time.Minute), `- resource: Cluster
  meter: control_plane
  unit: second
  billable_when:
    states: [PROGRESSING, READY]
    turns_billable_once: [READY]
`))
	facts := []database.Fact{
		created("ComputeInstance", "vm-1", at(0)),
		state("ComputeInstance", "vm-1", at(0), "STARTING"),
		state("ComputeInstance", "vm-1", at(time.Minute-400*time.Millisecond), "RUNNING"),
	}
	pieces, err := calculator.Expected("acme", "ComputeInstance", "vm-1", facts, nil, at(2*time.Minute))
	if err != nil {
		t.Fatalf("computing usage: %v", err)
	}
	for _, piece := range pieces {
		if quantityMicro(t, piece) != 0 {
			t.Fatalf(
				"the compute meter existed for only 400 ms of RUNNING, want zero; got %s",
				join(summarize(pieces)),
			)
		}
	}
}

// TestBillableRunCrossingRevisionBoundaryBillsItsFullSum checks the continuity the threshold
// must keep: a run billable under both revisions sums its pieces on both sides of the
// boundary, and the sum passes the threshold once, so the sub-second head before the boundary
// bills together with the tail after it.
func TestBillableRunCrossingRevisionBoundaryBillsItsFullSum(t *testing.T) {
	calculator := revisionsCalculator(t, laterRevision(t, at(time.Minute), `- resource: ComputeInstance
  meter: compute
  unit: second
  billable_when:
    states: [RUNNING, PAUSED]
  dimensions: [instance_type]
`))
	facts := []database.Fact{
		created("ComputeInstance", "vm-1", at(0)),
		state("ComputeInstance", "vm-1", at(0), "STARTING"),
		state("ComputeInstance", "vm-1", at(time.Minute-400*time.Millisecond), "RUNNING"),
	}
	expectPieces(t, calculator, facts, at(2*time.Minute), []string{
		"ComputeInstance compute 10:00:59.600000..10:01:00.000000 rate=1000000 micro=400000 " +
			"pending=false dims={\"deployment\":\"us-east\",\"meter_type\":\"second\"," +
			"\"tenant\":\"acme\"}",
		"ComputeInstance compute 10:01:00.000000..10:02:00.000000 rate=1000000 micro=60000000 " +
			"pending=false dims={\"deployment\":\"us-east\",\"meter_type\":\"second\"," +
			"\"tenant\":\"acme\"}",
	})
}

// customCalculator builds a calculator over one revision parsed from meters text.
func customCalculator(t *testing.T, meters string) *usage.Calculator {
	t.Helper()
	revision, err := rules.Parse(
		rules.NamePrefix+"0001", time.Unix(0, 0).UTC().Format(time.RFC3339), meters,
	)
	if err != nil {
		t.Fatalf("parsing revision: %v", err)
	}
	owners, err := usage.RegistryOwners()
	if err != nil {
		t.Fatalf("reading owners: %v", err)
	}
	calculator, err := usage.NewCalculator([]rules.Revision{revision}, "us-east", owners)
	if err != nil {
		t.Fatalf("creating calculator: %v", err)
	}
	return calculator
}

// TestBillingFieldChangesSplitTheMeter is the split-point matrix: a change mid-run in any
// billing-relevant field kind, the state, a condition field, a dimension, or the multiplier,
// closes the meter's piece at the change and opens a new one, so no time bills under a stale
// rate or identity. The multiplier case: a multiplier of 1 then 2
// over a minute each bills 60 + 120 = 180 seconds, not 120 at the starting rate.
func TestBillingFieldChangesSplitTheMeter(t *testing.T) {
	baseDims := `{"deployment":"us-east","meter_type":"second","tenant":"acme"}`
	t.Run("state change", func(t *testing.T) {
		calculator := customCalculator(t, `- resource: ComputeInstance
  meter: compute
  unit: second
  billable_when:
    states: [RUNNING]
`)
		facts := []database.Fact{
			created("ComputeInstance", "vm-1", at(0)),
			state("ComputeInstance", "vm-1", at(0), "RUNNING"),
			state("ComputeInstance", "vm-1", at(30*time.Second), "PAUSED"),
			state("ComputeInstance", "vm-1", at(time.Minute), "RUNNING"),
		}
		expectPieces(t, calculator, facts, at(90*time.Second), []string{
			"ComputeInstance compute 10:00:00.000000..10:00:30.000000 rate=1000000 micro=30000000 " +
				"pending=false dims=" + baseDims,
			"ComputeInstance compute 10:01:00.000000..10:01:30.000000 rate=1000000 micro=30000000 " +
				"pending=false dims=" + baseDims,
		})
	})
	t.Run("condition field change", func(t *testing.T) {
		calculator := customCalculator(t, `- resource: ExternalIP
  meter: attached_allocation
  unit: second
  billable_when:
    states: [ALLOCATED]
    fields: {attached: "true"}
`)
		facts := []database.Fact{
			created("ExternalIP", "ip-1", at(0)),
			state("ExternalIP", "ip-1", at(0), "ALLOCATED"),
			fact("ExternalIP", "ip-1", "status.attached", at(0), "true"),
			fact("ExternalIP", "ip-1", "status.attached", at(30*time.Second), "false"),
			fact("ExternalIP", "ip-1", "status.attached", at(time.Minute), "true"),
		}
		expectPieces(t, calculator, facts, at(90*time.Second), []string{
			"ExternalIP attached_allocation 10:00:00.000000..10:00:30.000000 rate=1000000 " +
				"micro=30000000 pending=false dims=" + baseDims,
			"ExternalIP attached_allocation 10:01:00.000000..10:01:30.000000 rate=1000000 " +
				"micro=30000000 pending=false dims=" + baseDims,
		})
	})
	t.Run("dimension change", func(t *testing.T) {
		calculator := customCalculator(t, `- resource: ComputeInstance
  meter: compute
  unit: second
  billable_when:
    states: [RUNNING]
  dimensions: [instance_type]
`)
		facts := []database.Fact{
			created("ComputeInstance", "vm-1", at(0)),
			state("ComputeInstance", "vm-1", at(0), "RUNNING"),
			fact("ComputeInstance", "vm-1", "status.instance_type", at(0), "small"),
			fact("ComputeInstance", "vm-1", "status.instance_type", at(time.Minute), "large"),
		}
		expectPieces(t, calculator, facts, at(2*time.Minute), []string{
			"ComputeInstance compute 10:00:00.000000..10:01:00.000000 rate=1000000 micro=60000000 " +
				"pending=false dims=" +
				`{"deployment":"us-east","instance_type":"small","meter_type":"second","tenant":"acme"}`,
			"ComputeInstance compute 10:01:00.000000..10:02:00.000000 rate=1000000 micro=60000000 " +
				"pending=false dims=" +
				`{"deployment":"us-east","instance_type":"large","meter_type":"second","tenant":"acme"}`,
		})
	})
	t.Run("multiplier change", func(t *testing.T) {
		calculator := customCalculator(t, `- resource: ComputeInstance
  meter: weighted
  unit: second
  billable_when:
    states: [RUNNING]
  multiplier: instance_type
`)
		facts := []database.Fact{
			created("ComputeInstance", "vm-1", at(0)),
			state("ComputeInstance", "vm-1", at(0), "RUNNING"),
			fact("ComputeInstance", "vm-1", "status.instance_type", at(0), "1"),
			fact("ComputeInstance", "vm-1", "status.instance_type", at(time.Minute), "2"),
		}
		expectPieces(t, calculator, facts, at(2*time.Minute), []string{
			"ComputeInstance weighted 10:00:00.000000..10:01:00.000000 rate=1000000 micro=60000000 " +
				"pending=false dims=" + baseDims,
			"ComputeInstance weighted 10:01:00.000000..10:02:00.000000 rate=2000000 micro=120000000 " +
				"pending=false dims=" + baseDims,
		})
	})
}

// TestVolumeResizeSplitsTheRate checks the shipped volume rule: a capacity resized from 100
// to 200 GiB mid-run splits the rate at the resize, so each stretch bills at its own size.
func TestVolumeResizeSplitsTheRate(t *testing.T) {
	calculator := defaultCalculator(t)
	facts := []database.Fact{
		created("Volume", "vol-1", at(0)),
		state("Volume", "vol-1", at(0), "AVAILABLE"),
		fact("Volume", "vol-1", "status.protocol", at(0), "BLOCK"),
		fact("Volume", "vol-1", "status.vendor_volume_id", at(0), "vend-9"),
		fact("Volume", "vol-1", "status.provisioned_size_gib", at(0), "100"),
		fact("Volume", "vol-1", "status.provisioned_size_gib", at(time.Minute), "200"),
		fact("Volume", "vol-1", "spec.storage_tier", at(0), "gold"),
	}
	expectPieces(t, calculator, facts, at(2*time.Minute), []string{
		"Volume capacity 10:00:00.000000..10:01:00.000000 rate=100000000 micro=6000000000 " +
			"pending=false dims={\"deployment\":\"us-east\",\"meter_type\":\"gibibyte_second\"," +
			"\"storage_tier\":\"gold\",\"tenant\":\"acme\"}",
		"Volume capacity 10:01:00.000000..10:02:00.000000 rate=200000000 micro=12000000000 " +
			"pending=false dims={\"deployment\":\"us-east\",\"meter_type\":\"gibibyte_second\"," +
			"\"storage_tier\":\"gold\",\"tenant\":\"acme\"}",
	})
}

// TestVolumeQuantityOverflowsLoudly checks the overflow under the default
// volume rule, 200,000,000 GiB for one day is a micro quantity beyond int64, and reading
// the piece's quantity must fail instead of wrapping into a negative bill.
func TestVolumeQuantityOverflowsLoudly(t *testing.T) {
	calculator := defaultCalculator(t)
	facts := []database.Fact{
		created("Volume", "vol-1", at(0)),
		state("Volume", "vol-1", at(0), "AVAILABLE"),
		fact("Volume", "vol-1", "status.protocol", at(0), "BLOCK"),
		fact("Volume", "vol-1", "status.vendor_volume_id", at(0), "vend-9"),
		fact("Volume", "vol-1", "status.provisioned_size_gib", at(0), "200000000"),
	}
	pieces, err := calculator.Expected("acme", "Volume", "vol-1", facts, nil, at(14*time.Hour))
	if err != nil {
		t.Fatalf("computing usage: %v", err)
	}
	for _, piece := range pieces {
		quantity, err := piece.QuantityMicro()
		if err == nil {
			t.Fatalf("200,000,000 GiB over 14 h overflows micro units, got %d", quantity)
		}
		if !strings.Contains(err.Error(), "vol-1") {
			t.Errorf("the failure must name the resource, got %q", err.Error())
		}
	}
}

// TestOnceReachedWhenRunStartsInOnceState checks that a run opened by a condition change,
// not a state change, bills when the resource was already in a once state: a meter whose
// condition is a changing field.
func TestOnceReachedWhenRunStartsInOnceState(t *testing.T) {
	meters := `- resource: ExternalIP
  meter: attached_allocation
  unit: second
  billable_when:
    states: [ALLOCATED]
    turns_billable_once: [ALLOCATED]
    fields: {attached: "true"}
  dimensions: [pool]
`
	revision, err := rules.Parse(
		rules.NamePrefix+"0001", time.Unix(0, 0).UTC().Format(time.RFC3339), meters,
	)
	if err != nil {
		t.Fatalf("parsing revision: %v", err)
	}
	owners, err := usage.RegistryOwners()
	if err != nil {
		t.Fatalf("reading owners: %v", err)
	}
	calculator, err := usage.NewCalculator([]rules.Revision{revision}, "us-east", owners)
	if err != nil {
		t.Fatalf("creating calculator: %v", err)
	}

	facts := []database.Fact{
		created("ExternalIP", "ip-1", at(0)),
		state("ExternalIP", "ip-1", at(0), "ALLOCATED"),
		fact("ExternalIP", "ip-1", "status.attached", at(0), "false"),
		// The attachment turns the meter billable at 10:02, while the resource has
		// been ALLOCATED since 10:00.
		fact("ExternalIP", "ip-1", "status.attached", at(2*time.Minute), "true"),
		fact("ExternalIP", "ip-1", "spec.pool", at(0), "pool-a"),
	}
	expectPieces(t, calculator, facts, at(3*time.Minute), []string{
		"ExternalIP attached_allocation 10:02:00.000000..10:03:00.000000 rate=1000000 " +
			"micro=60000000 pending=false dims={\"deployment\":\"us-east\"," +
			"\"meter_type\":\"second\",\"pool\":\"pool-a\",\"tenant\":\"acme\"}",
	})
}

// TestQuantityMicroRoundsToNearestMicro checks the rounding policy: a fractional multiplier
// must not re-bill its rounding remainder every round.
func TestQuantityMicroRoundsToNearestMicro(t *testing.T) {
	piece := usage.Piece{
		StartTime: at(0),
		EndTime:   at(1500 * time.Millisecond),
		// A rate of one micro unit per second over 1.5 s is 1.5 micro units.
		RateMicro: 1,
	}
	got, err := piece.QuantityMicro()
	if err != nil {
		t.Fatalf("rounding 1.5 micro units: %v", err)
	}
	if got != 2 {
		t.Errorf("1.5 micro units must round to 2, got %d", got)
	}
	piece.EndTime = at(1400 * time.Millisecond)
	got, err = piece.QuantityMicro()
	if err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Errorf("1.4 micro units must round to 1, got %d", got)
	}
	piece.EndTime = at(500 * time.Millisecond)
	got, err = piece.QuantityMicro()
	if err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Errorf("0.5 micro units must round to 1, got %d", got)
	}
	piece.RateMicro = 1_000_000
	piece.EndTime = at(time.Second)
	got, err = piece.QuantityMicro()
	if err != nil {
		t.Fatal(err)
	}
	if got != 1_000_000 {
		t.Errorf("60 whole micro units must stay exact, got %d", got)
	}
}

// TestEveryMeterSendsTheCatalogDimension checks the dimension name the design specifies.
func TestEveryMeterSendsTheCatalogDimension(t *testing.T) {
	calculator := defaultCalculator(t)
	facts := []database.Fact{
		created("ComputeInstance", "vm-7", at(0)),
		state("ComputeInstance", "vm-7", at(0), "RUNNING"),
		fact("ComputeInstance", "vm-7", "spec.catalog_item", at(0), "ci-rhel10-gpu"),
	}
	expectPieces(t, calculator, facts, at(time.Minute), []string{
		"ComputeInstance compute 10:00:00.000000..10:01:00.000000 rate=1000000 " +
			"micro=60000000 pending=false dims={\"catalog\":\"ci-rhel10-gpu\"," +
			"\"deployment\":\"us-east\",\"meter_type\":\"second\",\"tenant\":\"acme\"}",
	})
}

func TestRevisionChangeSplitsUsage(t *testing.T) {
	first := testenv.DefaultRevision(t)
	secondYAML := `- resource: ComputeInstance
  meter: compute
  unit: second
  billable_when:
    states: [RUNNING, PAUSED]
  dimensions: [instance_type]
`
	effective := at(2 * time.Minute)
	second, err := rules.Parse(
		rules.NamePrefix+"0002", effective.Format(time.RFC3339), secondYAML,
	)
	if err != nil {
		t.Fatalf("parsing second revision: %v", err)
	}
	owners, err := usage.RegistryOwners()
	if err != nil {
		t.Fatalf("reading owners: %v", err)
	}
	calculator, err := usage.NewCalculator([]rules.Revision{first, second}, "us-east", owners)
	if err != nil {
		t.Fatalf("creating calculator: %v", err)
	}

	facts := []database.Fact{
		created("ComputeInstance", "vm-7", at(0)),
		state("ComputeInstance", "vm-7", at(0), "RUNNING"),
		state("ComputeInstance", "vm-7", at(3*time.Minute), "PAUSED"),
	}
	expectPieces(t, calculator, facts, at(4*time.Minute), []string{
		// RUNNING bills under both revisions, PAUSED only under the second.
		"ComputeInstance compute 10:00:00.000000..10:02:00.000000 rate=1000000 micro=120000000 " +
			"pending=false dims={\"deployment\":\"us-east\",\"meter_type\":\"second\"," +
			"\"tenant\":\"acme\"}",
		"ComputeInstance compute 10:02:00.000000..10:04:00.000000 rate=1000000 micro=120000000 " +
			"pending=false dims={\"deployment\":\"us-east\",\"meter_type\":\"second\"," +
			"\"tenant\":\"acme\"}",
	})
}

// TestTailComputesOnlyTheLatestInterval checks that a tail extension materializes only the
// pieces that end after the previous round, plus a pending piece a zero-quantity ledger row
// watches: the whole history is never rebuilt just to move a tail forward.
func TestTailComputesOnlyTheLatestInterval(t *testing.T) {
	calculator := defaultCalculator(t)
	// A VM running across three UTC midnights.
	facts := []database.Fact{
		created("ComputeInstance", "vm-7", base),
		state("ComputeInstance", "vm-7", base, "RUNNING"),
	}
	from := base.Add(36 * time.Hour)
	upTo := base.Add(60 * time.Hour)

	all, err := calculator.Expected("acme", "ComputeInstance", "vm-7", facts, nil, upTo)
	if err != nil {
		t.Fatalf("computing usage: %v", err)
	}
	tail, err := calculator.Since("acme", "ComputeInstance", "vm-7", facts, nil, from, upTo, nil)
	if err != nil {
		t.Fatalf("computing tail: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected one piece per day, got %d", len(all))
	}
	// The piece that straddles the previous round and the one after it: the straddling
	// piece is what the ledger's tail row is compared against. Pieces split at UTC
	// midnight, so they start at the midnights after base.
	if len(tail) != 2 || !tail[0].StartTime.Equal(base.Add(14*time.Hour)) ||
		!tail[1].StartTime.Equal(base.Add(38*time.Hour)) {
		t.Fatalf(
			"the tail must hold only the pieces that end after the previous round, got %s",
			fmt.Sprint(summarize(tail)),
		)
	}
}

// TestTailKeepsPiecesAWatchedRowOverlaps checks that a piece predating the previous round is
// still computed when it overlaps a zero-quantity ledger row: a pending run that crosses the
// billable threshold between two rounds bills from its start.
func TestTailKeepsPiecesAWatchedRowOverlaps(t *testing.T) {
	calculator := defaultCalculator(t)
	// The run starts at 10:00:00.4 and is 0.4 s long at the previous round.
	runStart := base.Add(400 * time.Millisecond)
	facts := []database.Fact{
		created("ComputeInstance", "vm-7", runStart),
		state("ComputeInstance", "vm-7", runStart, "RUNNING"),
	}
	from := base.Add(800 * time.Millisecond)
	upTo := base.Add(2 * time.Second)

	pieces, err := calculator.Since("acme", "ComputeInstance", "vm-7", facts, nil, from, upTo, []database.LedgerPiece{{
		Key: database.LedgerKey{
			ResourceType: "ComputeInstance", ResourceID: "vm-7", Meter: "compute",
			Component:  "compute",
			Dimensions: "{\"deployment\":\"us-east\",\"meter_type\":\"second\",\"tenant\":\"acme\"}",
		},
		StartTime: runStart, EndTime: from, QuantityMicro: 0,
	}})
	if err != nil {
		t.Fatalf("computing tail: %v", err)
	}
	if len(pieces) != 1 || !pieces[0].StartTime.Equal(runStart) || quantityMicro(t, pieces[0]) == 0 {
		t.Fatalf(
			"the crossing run must bill from its start, got %s",
			fmt.Sprint(summarize(pieces)),
		)
	}
}

// TestMeterAtResolvesTheBillingRevision checks that the meter definition and the revision that
// billed an interval resolve from the rules, including for a meter a later revision dropped.
func TestMeterAtResolvesTheBillingRevision(t *testing.T) {
	first := testenv.DefaultRevision(t)
	droppedYAML := `- resource: Cluster
  meter: control_plane
  unit: second
  billable_when:
    states: [PROGRESSING, READY]
    turns_billable_once: [READY]
  dimensions: [template, version]
`
	second, err := rules.Parse(
		rules.NamePrefix+"0002", at(2*time.Minute).Format(time.RFC3339), droppedYAML,
	)
	if err != nil {
		t.Fatalf("parsing second revision: %v", err)
	}
	owners, err := usage.RegistryOwners()
	if err != nil {
		t.Fatalf("reading owners: %v", err)
	}
	calculator, err := usage.NewCalculator([]rules.Revision{first, second}, "us-east", owners)
	if err != nil {
		t.Fatalf("creating calculator: %v", err)
	}

	meter, revision, ok := calculator.MeterAt("Cluster", "control_plane", at(time.Minute))
	if !ok || meter.Unit != "second" || revision != "0001" {
		t.Fatalf("before the boundary: got unit %q revision %q ok %v", meter.Unit, revision, ok)
	}
	meter, revision, ok = calculator.MeterAt("Cluster", "control_plane", at(3*time.Minute))
	if !ok || meter.Unit != "second" || revision != "0002" {
		t.Fatalf("after the boundary: got unit %q revision %q ok %v", meter.Unit, revision, ok)
	}
	// The compute meter is gone from revision 0002, but its ledger rows were billed under
	// the revision in effect when they started.
	if _, _, ok := calculator.MeterAt("ComputeInstance", "compute", at(3*time.Minute)); !ok {
		t.Fatal("a dropped meter must still resolve to the revision that billed it")
	}
	if _, _, ok := calculator.MeterAt("ComputeInstance", "nonexistent", at(time.Minute)); ok {
		t.Fatal("an unknown meter must not resolve")
	}
}

// clusterRoot builds the ownership root of a cluster from its facts.
func clusterRoot(id string, facts ...database.Fact) *usage.Root {
	return &usage.Root{Type: "Cluster", ID: id, Facts: facts}
}

// workerFacts builds an allocated worker's facts: created, STARTING, its type, and its
// cluster and node set references.
func workerFacts(id, cluster, nodeSet string) []database.Fact {
	at := base
	return []database.Fact{
		created("BareMetalInstance", id, at),
		state("BareMetalInstance", id, at, "STARTING"),
		fact("BareMetalInstance", id, "spec.instance_type", at, "m7a"),
		fact("BareMetalInstance", id, "status.cluster", at, cluster),
		fact("BareMetalInstance", id, "status.node_set", at, nodeSet),
	}
}

// workerDims are the dimensions every worker heartbeat carries: the identity dimensions,
// the instance type, and the ownership references rendered by id.
func workerDims(cluster, nodeSet string) string {
	return fmt.Sprintf(
		`{"cluster":"%s","deployment":"us-east","instance_type":"m7a",`+
			`"meter_type":"second","node_set":"%s","tenant":"acme"}`,
		cluster, nodeSet,
	)
}

// expectWorkerPieces computes the worker's pieces under the given root and compares them
// with the wanted lines.
func expectWorkerPieces(
	t *testing.T, calculator *usage.Calculator, facts []database.Fact, root *usage.Root,
	upTo time.Time, want []string,
) {
	t.Helper()
	pieces, err := calculator.Expected(
		"acme", facts[0].ResourceType, facts[0].ResourceID, facts, root, upTo,
	)
	if err != nil {
		t.Fatalf("computing usage: %v", err)
	}
	got := summarize(pieces)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("pieces mismatch:\ngot:\n%s\nwant:\n%s", join(got), join(want))
	}
}

// TestWorkerBillsRetroactivelyWhenClusterReachesReady is the model's core promise: the
// worker's allocation is deferred while the cluster installs, and the round after READY
// bills the whole span from the worker's own start.
func TestWorkerBillsRetroactivelyWhenClusterReachesReady(t *testing.T) {
	calculator := defaultCalculator(t)
	root := clusterRoot("c-1",
		created("Cluster", "c-1", base),
		state("Cluster", "c-1", base, "PROGRESSING"),
		state("Cluster", "c-1", at(30*time.Minute), "READY"),
	)
	expectWorkerPieces(t, calculator, workerFacts("bm-1", "c-1", "workers"), root,
		at(40*time.Minute), []string{
			"BareMetalInstance allocation 10:00:00.000000..10:40:00.000000 rate=1000000 " +
				"micro=2400000000 pending=false dims=" + workerDims("c-1", "workers"),
		})
}

// TestWorkerOfFailedClusterBillsNothing checks the forgiveness the owner chose: a cluster
// that fails or is deleted before READY bills no control plane time and no worker
// hardware, including hardware that was physically allocated for the whole install.
func TestWorkerOfFailedClusterBillsNothing(t *testing.T) {
	calculator := defaultCalculator(t)
	for _, terminal := range []string{"FAILED", "DELETING"} {
		root := clusterRoot("c-1",
			created("Cluster", "c-1", base),
			state("Cluster", "c-1", base, "PROGRESSING"),
			state("Cluster", "c-1", at(20*time.Minute), terminal),
		)
		expectWorkerPieces(t, calculator, workerFacts("bm-1", "c-1", "workers"), root,
			at(40*time.Minute), nil)
	}
}

// TestWorkerAddedAfterClusterReadyBillsFromItsOwnStart checks scale-up: the gate is
// already open, so a worker created later bills from its own start with no special case.
func TestWorkerAddedAfterClusterReadyBillsFromItsOwnStart(t *testing.T) {
	calculator := defaultCalculator(t)
	root := clusterRoot("c-1",
		created("Cluster", "c-1", at(-10*time.Minute)),
		state("Cluster", "c-1", at(-10*time.Minute), "PROGRESSING"),
		state("Cluster", "c-1", base, "READY"),
	)
	expectWorkerPieces(t, calculator, workerFacts("bm-1", "c-1", "workers"), root,
		at(30*time.Minute), []string{
			"BareMetalInstance allocation 10:00:00.000000..10:30:00.000000 rate=1000000 " +
				"micro=1800000000 pending=false dims=" + workerDims("c-1", "workers"),
		})
}

// TestWorkerDeletedBeforeClusterReadyNeverBills checks the confirmed default: a span that
// closed before the gate opened stays unbilled forever, even after the cluster succeeds.
func TestWorkerDeletedBeforeClusterReadyNeverBills(t *testing.T) {
	calculator := defaultCalculator(t)
	root := clusterRoot("c-1",
		created("Cluster", "c-1", base),
		state("Cluster", "c-1", base, "PROGRESSING"),
		state("Cluster", "c-1", at(20*time.Minute), "READY"),
	)
	facts := workerFacts("bm-1", "c-1", "workers")
	facts = append(facts, fact("BareMetalInstance", "bm-1", "deleted", at(10*time.Minute), "true"))
	expectWorkerPieces(t, calculator, facts, root, at(40*time.Minute), nil)
}

// TestWorkerKeepsBillingThroughRootUpgrade checks the latch on the root's timeline: an
// upgrade takes the cluster out of READY and back, and the worker's billing never stops.
func TestWorkerKeepsBillingThroughRootUpgrade(t *testing.T) {
	calculator := defaultCalculator(t)
	root := clusterRoot("c-1",
		created("Cluster", "c-1", base),
		state("Cluster", "c-1", base, "PROGRESSING"),
		state("Cluster", "c-1", at(10*time.Minute), "READY"),
		state("Cluster", "c-1", at(20*time.Minute), "PROGRESSING"),
		state("Cluster", "c-1", at(30*time.Minute), "READY"),
	)
	expectWorkerPieces(t, calculator, workerFacts("bm-1", "c-1", "workers"), root,
		at(40*time.Minute), []string{
			"BareMetalInstance allocation 10:00:00.000000..10:40:00.000000 rate=1000000 " +
				"micro=2400000000 pending=false dims=" + workerDims("c-1", "workers"),
		})
}

// TestReadyStampedAfterRootDeletionDoesNotOpenGate checks the deletion clamp: a
// confirming state dated after the root's deletion is a fact about a dead resource and
// must not open the gate, by the same rule that nothing bills after deletion.
func TestReadyStampedAfterRootDeletionDoesNotOpenGate(t *testing.T) {
	calculator := defaultCalculator(t)
	root := clusterRoot("c-1",
		created("Cluster", "c-1", base),
		state("Cluster", "c-1", base, "PROGRESSING"),
		fact("Cluster", "c-1", "deleted", at(10*time.Minute), "true"),
		state("Cluster", "c-1", at(15*time.Minute), "READY"),
	)
	expectWorkerPieces(t, calculator, workerFacts("bm-1", "c-1", "workers"), root,
		at(40*time.Minute), nil)
}

// TestMissingRootHistoryHoldsWorkerBilling checks the loud case's billing half: a worker
// whose owner fact names a cluster with no history bills nothing, never standalone. The
// round logs the miss.
func TestMissingRootHistoryHoldsWorkerBilling(t *testing.T) {
	calculator := defaultCalculator(t)
	root := clusterRoot("c-1")
	expectWorkerPieces(t, calculator, workerFacts("bm-1", "c-1", "workers"), root,
		at(40*time.Minute), nil)
}

// TestFactsWithoutCreationRefuse checks the fail-shut rule: facts that carry no creation
// fact are corrupt, because the fold writes one with every resource's first fact.
// Guessing the start from the earliest surviving fact would under-bill, so both compute
// paths refuse instead.
func TestFactsWithoutCreationRefuse(t *testing.T) {
	calculator := defaultCalculator(t)
	facts := []database.Fact{
		state("BareMetalInstance", "bm-1", base, "STARTING"),
		fact("BareMetalInstance", "bm-1", "spec.instance_type", base, "m7a"),
	}
	_, err := calculator.Expected(
		"acme", "BareMetalInstance", "bm-1", facts, nil, at(30*time.Minute),
	)
	if err == nil || !strings.Contains(err.Error(), "BareMetalInstance bm-1 has no creation fact") {
		t.Fatalf("expected Expected to refuse facts without a creation fact, got %v", err)
	}
	_, err = calculator.Since(
		"acme", "BareMetalInstance", "bm-1", facts, nil, base, at(30*time.Minute), nil,
	)
	if err == nil || !strings.Contains(err.Error(), "BareMetalInstance bm-1 has no creation fact") {
		t.Fatalf("expected Since to refuse facts without a creation fact, got %v", err)
	}
}

// TestEmptyFactsBillNothing pins the hold: completely empty facts produce no pieces and no
// error, because the missing-ownership-root gate depends on empty facts shutting billing
// instead of failing the round.
func TestEmptyFactsBillNothing(t *testing.T) {
	calculator := defaultCalculator(t)
	pieces, err := calculator.Expected("acme", "BareMetalInstance", "bm-1", nil, nil, at(30*time.Minute))
	if err != nil {
		t.Fatalf("computing empty facts: %v", err)
	}
	if len(pieces) != 0 {
		t.Fatalf("empty facts must produce no pieces, got %s", summarize(pieces))
	}
	pieces, err = calculator.Since(
		"acme", "BareMetalInstance", "bm-1", nil, nil, base, at(30*time.Minute), nil,
	)
	if err != nil {
		t.Fatalf("computing empty facts since: %v", err)
	}
	if len(pieces) != 0 {
		t.Fatalf("empty facts must produce no pieces since, got %s", summarize(pieces))
	}
}

// TestStandaloneWorkerBillsOnItsOwnGate checks that a worker without an owner fact reads
// nothing but its own gate: the BMI meters' own confirming states apply.
func TestStandaloneWorkerBillsOnItsOwnGate(t *testing.T) {
	calculator := defaultCalculator(t)
	facts := []database.Fact{
		created("BareMetalInstance", "bm-1", base),
		state("BareMetalInstance", "bm-1", base, "STARTING"),
		fact("BareMetalInstance", "bm-1", "spec.instance_type", base, "m7a"),
	}
	expectWorkerPieces(t, calculator, facts, nil, at(30*time.Minute), []string{
		"BareMetalInstance allocation 10:00:00.000000..10:30:00.000000 rate=1000000 " +
			"micro=1800000000 pending=false dims=" +
			`{"deployment":"us-east","instance_type":"m7a","meter_type":"second","tenant":"acme"}`,
	})
}

// TestSelfGateOverrideBillsTheWorkerRegardlessOfCluster checks the CSP's escape hatch: a
// revision that turns the meter's gate to self bills the worker on its own confirming
// states, so allocated hardware bills even when the cluster fails completely.
func TestSelfGateOverrideBillsTheWorkerRegardlessOfCluster(t *testing.T) {
	revision, err := rules.Parse(
		rules.NamePrefix+"0002", at(0).Format(time.RFC3339), `- resource: BareMetalInstance
  meter: allocation
  unit: second
  billable_when:
    states: [STARTING, RUNNING, STOPPING, STOPPED, DELETING]
    turns_billable_once: [STARTING, RUNNING]
    turns_billable_once_from: self
  dimensions: [instance_type, cluster, node_set]
`)
	if err != nil {
		t.Fatalf("parsing revision: %v", err)
	}
	owners, err := usage.RegistryOwners()
	if err != nil {
		t.Fatalf("reading owners: %v", err)
	}
	calculator, err := usage.NewCalculator([]rules.Revision{revision}, "us-east", owners)
	if err != nil {
		t.Fatalf("creating calculator: %v", err)
	}
	root := clusterRoot("c-1",
		created("Cluster", "c-1", base),
		state("Cluster", "c-1", base, "PROGRESSING"),
		state("Cluster", "c-1", at(20*time.Minute), "FAILED"),
	)
	expectWorkerPieces(t, calculator, workerFacts("bm-1", "c-1", "workers"), root,
		at(40*time.Minute), []string{
			"BareMetalInstance allocation 10:00:00.000000..10:40:00.000000 rate=1000000 " +
				"micro=2400000000 pending=false dims=" + workerDims("c-1", "workers"),
		})
}

// TestNestedOwnersGateOnTheTopmostRoot checks the chain walk's engine half: the gate a
// resource inherits comes from the topmost owner, not the immediate one. The synthetic
// owners and the hand-rolled revision stand in for a future offering whose resources are
// components of components.
func TestNestedOwnersGateOnTheTopmostRoot(t *testing.T) {
	revision := rules.Revision{
		Name: "synthetic", Number: "0001", EffectiveAt: time.Unix(0, 0).UTC(),
		Meters: []rules.Meter{
			{
				Resource: "WaaS2", Meter: "platform", Unit: "second",
				Billable: rules.BillableWhen{
					States: []string{"INACTIVE", "ACTIVE"}, TurnsBillableOnce: []string{"ACTIVE"},
				},
			},
			{
				Resource: "WaaS", Meter: "service", Unit: "second",
				Billable: rules.BillableWhen{States: []string{"ON"}},
			},
			{
				Resource: "Worker", Meter: "allocation", Unit: "second",
				Billable: rules.BillableWhen{States: []string{"ON"}},
			},
		},
	}
	owners := map[string]usage.OwnerSpec{
		"WaaS":   {Field: "status.owner", Resource: "WaaS2"},
		"Worker": {Field: "status.owner", Resource: "WaaS"},
	}
	calculator, err := usage.NewCalculator([]rules.Revision{revision}, "us-east", owners)
	if err != nil {
		t.Fatalf("creating calculator: %v", err)
	}
	workerFacts := []database.Fact{
		created("Worker", "w-1", base),
		state("Worker", "w-1", base, "ON"),
		fact("Worker", "w-1", "status.owner", base, "waas-1"),
	}
	waasFacts := []database.Fact{
		created("WaaS", "waas-1", base),
		state("WaaS", "waas-1", base, "ON"),
		fact("WaaS", "waas-1", "status.owner", base, "top-1"),
	}
	waas2Facts := []database.Fact{
		created("WaaS2", "top-1", base),
		state("WaaS2", "top-1", base, "INACTIVE"),
		state("WaaS2", "top-1", at(30*time.Minute), "ACTIVE"),
	}

	// The topmost root gates the worker: WaaS2 reached ACTIVE at 10:30, so the worker's
	// span bills retroactively from its start.
	root := &usage.Root{Type: "WaaS2", ID: "top-1", Facts: waas2Facts}
	expectWorkerPieces(t, calculator, workerFacts, root, at(40*time.Minute), []string{
		"Worker allocation 10:00:00.000000..10:40:00.000000 rate=1000000 " +
			"micro=2400000000 pending=false dims=" +
			`{"deployment":"us-east","meter_type":"second","tenant":"acme"}`,
	})

	// A direct owner in between never re-gates: the walk must resolve the topmost root,
	// because the immediate owner's meters define no confirming states and would bill
	// without a gate.
	directRoot := &usage.Root{Type: "WaaS", ID: "waas-1", Facts: waasFacts}
	expectWorkerPieces(t, calculator, workerFacts, directRoot, at(5*time.Minute), []string{
		"Worker allocation 10:00:00.000000..10:05:00.000000 rate=1000000 " +
			"micro=300000000 pending=false dims=" +
			`{"deployment":"us-east","meter_type":"second","tenant":"acme"}`,
	})
}

// TestWorkerDeletedAfterGateOpenedBillsUntilDeletion checks the open-gate deletion: the
// worker bills up to its own deletion, nothing after it, and the deletion does not
// reverse what the open gate already billed.
func TestWorkerDeletedAfterGateOpenedBillsUntilDeletion(t *testing.T) {
	calculator := defaultCalculator(t)
	root := clusterRoot("c-1",
		created("Cluster", "c-1", at(-10*time.Minute)),
		state("Cluster", "c-1", at(-10*time.Minute), "PROGRESSING"),
		state("Cluster", "c-1", base, "READY"),
	)
	facts := workerFacts("bm-1", "c-1", "workers")
	facts = append(facts, fact("BareMetalInstance", "bm-1", "deleted", at(30*time.Minute), "true"))
	expectWorkerPieces(t, calculator, facts, root, at(40*time.Minute), []string{
		"BareMetalInstance allocation 10:00:00.000000..10:30:00.000000 rate=1000000 " +
			"micro=1800000000 pending=false dims=" + workerDims("c-1", "workers"),
	})
}
