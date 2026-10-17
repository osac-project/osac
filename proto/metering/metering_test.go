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
				{Path: "spec.instance_type", Kind: KindFixedAtCreation},
				{Path: "status.cluster", Kind: KindSetOnceLater},
				{Path: "status.node_set", Kind: KindSetOnceLater},
				{Path: "status.state", Kind: KindChanging, TransitionTime: "status.state_transition_time"},
			},
		},
		{
			Message: "osac.private.v1.Cluster",
			Fields: []Field{
				{Path: "spec.template", Kind: KindFixedAtCreation},
				{Path: "spec.version", Kind: KindChanging, TransitionTime: "status.version_transition_time"},
				{Path: "status.state", Kind: KindChanging, TransitionTime: "status.state_transition_time"},
			},
		},
		{
			Message: "osac.private.v1.ComputeInstance",
			Fields: []Field{
				{Path: "spec.boot_disk.size_gib", Kind: KindFixedAtCreation},
				{Path: "spec.disk_image", Kind: KindFixedAtCreation},
				{Path: "status.instance_type", Kind: KindChanging, TransitionTime: "status.instance_type_transition_time"},
				{Path: "status.state", Kind: KindChanging, TransitionTime: "status.state_transition_time"},
			},
		},
		{
			Message: "osac.private.v1.ExternalIP",
			Fields: []Field{
				{Path: "spec.pool", Kind: KindFixedAtCreation},
				{Path: "status.address", Kind: KindSetOnceLater},
				{Path: "status.attached", Kind: KindChanging, TransitionTime: "status.attachment_transition_time"},
				{Path: "status.attribution", Kind: KindChanging, TransitionTime: "status.attachment_transition_time"},
				{Path: "status.state", Kind: KindChanging, TransitionTime: "status.state_transition_time"},
			},
		},
		{
			Message: "osac.private.v1.NATGateway",
			Fields: []Field{
				{Path: "spec.external_ip", Kind: KindFixedAtCreation},
				{Path: "spec.virtual_network", Kind: KindFixedAtCreation},
				{Path: "status.state", Kind: KindChanging, TransitionTime: "status.state_transition_time"},
			},
		},
		{
			Message: "osac.private.v1.Volume",
			Fields: []Field{
				{Path: "spec.storage_tier", Kind: KindFixedAtCreation},
				{Path: "status.protocol", Kind: KindFixedAtCreation},
				{Path: "status.provisioned_size_gib", Kind: KindSetOnceLater},
				{Path: "status.state", Kind: KindChanging, TransitionTime: "status.state_transition_time"},
				{Path: "status.vendor_volume_id", Kind: KindSetOnceLater},
			},
		},
	}

	if !reflect.DeepEqual(resources, expected) {
		t.Fatalf("the tracked fields don't match the expected contract:\nexpected: %#v\nactual:   %#v", expected, resources)
	}
}
