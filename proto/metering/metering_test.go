/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package metering

import (
	"reflect"
	"testing"
)

// The registry is the metering source contract, so the test pins the fields of every resource
// exactly. Adding or changing a tracked field is a contract change: update this table together
// with the annotations and the migration that installs the triggers.
func TestResources(t *testing.T) {
	resources, err := Resources()
	if err != nil {
		t.Fatalf("failed to load the tracked fields: %v", err)
	}

	expected := []Resource{
		{
			Message: "osac.private.v1.BareMetalInstance",
			Fields: []Field{
				{Path: "spec.instance_type", Kind: KindConstant},
				{Path: "status.cluster", Kind: KindFixedOnceSet},
				{Path: "status.node_set", Kind: KindFixedOnceSet},
				{Path: "status.state", Kind: KindVaries, TransitionTime: "status.state_transition_time"},
			},
		},
		{
			Message: "osac.private.v1.Cluster",
			Fields: []Field{
				{Path: "spec.template", Kind: KindConstant},
				{Path: "spec.version", Kind: KindVaries, TransitionTime: "status.version_transition_time"},
				{Path: "status.state", Kind: KindVaries, TransitionTime: "status.state_transition_time"},
			},
		},
		{
			Message: "osac.private.v1.ComputeInstance",
			Fields: []Field{
				{Path: "spec.boot_disk.size_gib", Kind: KindConstant},
				{Path: "spec.disk_image", Kind: KindConstant},
				{Path: "status.instance_type", Kind: KindVaries, TransitionTime: "status.instance_type_transition_time"},
				{Path: "status.state", Kind: KindVaries, TransitionTime: "status.state_transition_time"},
			},
		},
		{
			Message: "osac.private.v1.ExternalIP",
			Fields: []Field{
				{Path: "spec.pool", Kind: KindConstant},
				{Path: "status.address", Kind: KindFixedOnceSet},
				{Path: "status.attached", Kind: KindVaries, TransitionTime: "status.attachment_transition_time"},
				{Path: "status.attribution", Kind: KindVaries, TransitionTime: "status.attachment_transition_time"},
				{Path: "status.state", Kind: KindVaries, TransitionTime: "status.state_transition_time"},
			},
		},
		{
			Message: "osac.private.v1.NATGateway",
			Fields: []Field{
				{Path: "spec.external_ip", Kind: KindConstant},
				{Path: "spec.virtual_network", Kind: KindConstant},
				{Path: "status.state", Kind: KindVaries, TransitionTime: "status.state_transition_time"},
			},
		},
		{
			Message: "osac.private.v1.Volume",
			Fields: []Field{
				{Path: "spec.storage_tier", Kind: KindConstant},
				{Path: "status.protocol", Kind: KindConstant},
				{Path: "status.provisioned_size_gib", Kind: KindFixedOnceSet},
				{Path: "status.state", Kind: KindVaries, TransitionTime: "status.state_transition_time"},
				{Path: "status.vendor_volume_id", Kind: KindFixedOnceSet},
			},
		},
	}

	if !reflect.DeepEqual(resources, expected) {
		t.Fatalf("the tracked fields don't match the expected contract:\nexpected: %#v\nactual:   %#v", expected, resources)
	}
}
