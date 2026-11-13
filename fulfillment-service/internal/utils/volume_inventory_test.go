package utils

import (
	"testing"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func TestIsComputeInstanceVolumeInventory(testContext *testing.T) {
	for _, test := range []struct {
		name      string
		volume    *privatev1.Volume
		inventory bool
	}{
		{name: "nil"},
		{name: "missing spec", volume: &privatev1.Volume{}},
		{name: "missing topology", volume: privatev1.Volume_builder{Spec: &privatev1.VolumeSpec{}}.Build()},
	} {
		testContext.Run(test.name, func(testContext *testing.T) {
			if IsComputeInstanceVolumeInventory(test.volume) != test.inventory {
				testContext.Fatal("unexpected inventory classification")
			}
		})
	}
	for _, test := range []struct {
		name      string
		segments  map[string]string
		inventory bool
	}{
		{name: "nil segments"},
		{name: "empty segments", segments: map[string]string{}},
		{name: "exact", segments: map[string]string{"osac.io/node": "osac-volume-registration-pending"}, inventory: true},
		{name: "wrong key", segments: map[string]string{"node": "osac-volume-registration-pending"}},
		{name: "empty value", segments: map[string]string{"osac.io/node": ""}},
		{name: "ordinary", segments: map[string]string{"osac.io/node": "worker-1"}},
		{name: "near match", segments: map[string]string{"osac.io/node": "osac-volume-registration-pending-other"}},
		{name: "extra key", segments: map[string]string{"osac.io/node": "osac-volume-registration-pending", "zone": "a"}},
	} {
		testContext.Run(test.name, func(testContext *testing.T) {
			volume := privatev1.Volume_builder{Spec: privatev1.VolumeSpec_builder{
				Topology: privatev1.VolumeTopology_builder{Segments: test.segments}.Build(),
			}.Build()}.Build()
			if IsComputeInstanceVolumeInventory(volume) != test.inventory {
				testContext.Fatal("unexpected inventory classification")
			}
		})
	}
}
