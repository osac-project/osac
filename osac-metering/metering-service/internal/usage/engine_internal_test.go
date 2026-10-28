/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package usage

import (
	"strings"
	"testing"

	"github.com/osac-project/osac/proto/metering"
)

// TestOwnersOfRejectsTwoOwnerFields checks that a registry declaring a resource with two
// owner-annotated fields refuses to build: the map keys by owned resource, so the second
// field would silently overwrite the first.
func TestOwnersOfRejectsTwoOwnerFields(t *testing.T) {
	resources := []metering.Resource{{
		Message: "osac.private.v1.Gadget",
		Fields: []metering.Field{
			{Path: "status.cluster", Kind: metering.KindFixedOnceSet, OwnerResource: "Cluster"},
			{Path: "status.owner", Kind: metering.KindFixedOnceSet, OwnerResource: "Cluster"},
		},
	}}
	_, err := ownersOf(resources)
	if err == nil {
		t.Fatal("a resource with two owner fields must refuse to build")
	}
	for _, name := range []string{"Gadget", "status.cluster", "status.owner"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the rejection must name %s, got %q", name, err.Error())
		}
	}
}

// TestOwnersOfRejectsOneRootOwnedThroughTwoFields checks the second ambiguity: when one root
// type owns different resources through different fields, OwnedFieldOf cannot pick one
// deterministically, so the registry refuses to build.
func TestOwnersOfRejectsOneRootOwnedThroughTwoFields(t *testing.T) {
	resources := []metering.Resource{
		{
			Message: "osac.private.v1.Gadget",
			Fields: []metering.Field{
				{Path: "status.cluster", Kind: metering.KindFixedOnceSet, OwnerResource: "Cluster"},
			},
		},
		{
			Message: "osac.private.v1.Widget",
			Fields: []metering.Field{
				{Path: "status.root", Kind: metering.KindFixedOnceSet, OwnerResource: "Cluster"},
			},
		},
	}
	_, err := ownersOf(resources)
	if err == nil {
		t.Fatal("a root owned through two different fields must refuse to build")
	}
	for _, name := range []string{"Cluster", "status.cluster", "status.root"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the rejection must name %s, got %q", name, err.Error())
		}
	}
}

// TestOwnersOfAcceptsTheUnambiguousCase checks the single owner edge: one field per owned
// resource, and one field per root.
func TestOwnersOfAcceptsTheUnambiguousCase(t *testing.T) {
	resources := []metering.Resource{
		{
			Message: "osac.private.v1.BareMetalInstance",
			Fields: []metering.Field{
				{Path: "spec.instance_type", Kind: metering.KindConstant},
				{Path: "status.cluster", Kind: metering.KindFixedOnceSet, OwnerResource: "Cluster"},
			},
		},
		{
			Message: "osac.private.v1.Cluster",
			Fields: []metering.Field{
				{Path: "spec.template", Kind: metering.KindConstant},
			},
		},
	}
	owners, err := ownersOf(resources)
	if err != nil {
		t.Fatalf("an unambiguous registry must build: %v", err)
	}
	spec, known := owners["BareMetalInstance"]
	if !known || spec.Field != "status.cluster" || spec.Resource != "Cluster" {
		t.Fatalf("BareMetalInstance resolves to %+v (known=%v), want status.cluster of Cluster", spec, known)
	}
	if _, known := owners["Cluster"]; known {
		t.Fatal("Cluster has no owner field and must be absent")
	}
}
