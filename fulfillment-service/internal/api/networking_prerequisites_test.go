//
// Copyright (c) 2026 Red Hat Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
// the License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

package api_test

import (
	"testing"

	"buf.build/go/protovalidate"
	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestNetworkingPrerequisitePublicAPI(t *testing.T) {
	privateExternalIPStatus := (&privatev1.ExternalIPStatus{}).ProtoReflect().Descriptor()
	for _, field := range []protoreflect.Name{
		"attribution",
		"attachment_transition_time",
		"state_transition_time",
	} {
		if privateExternalIPStatus.Fields().ByName(field) == nil {
			t.Fatalf("private ExternalIPStatus is missing %q", field)
		}
	}

	privateNATGatewayStatus := (&privatev1.NATGatewayStatus{}).ProtoReflect().Descriptor()
	if privateNATGatewayStatus.Fields().ByName("state_transition_time") == nil {
		t.Fatal("private NATGatewayStatus is missing state_transition_time")
	}

	publicExternalIPStatus := (&publicv1.ExternalIPStatus{}).ProtoReflect().Descriptor()
	for _, field := range []protoreflect.Name{
		"attribution",
		"attachment_transition_time",
		"state_transition_time",
	} {
		if publicExternalIPStatus.Fields().ByName(field) != nil {
			t.Fatalf("public ExternalIPStatus exposes private field %q", field)
		}
	}

	publicNATGatewayStatus := (&publicv1.NATGatewayStatus{}).ProtoReflect().Descriptor()
	if publicNATGatewayStatus.Fields().ByName("state_transition_time") != nil {
		t.Fatal("public NATGatewayStatus exposes state_transition_time")
	}

	endpoint := (&publicv1.ExternalIPAttachmentSpec{}).ProtoReflect().Descriptor().Fields().ByName("target_endpoint")
	if endpoint == nil || endpoint.Enum().FullName() != "osac.public.v1.ExternalIPAttachmentEndpoint" {
		t.Fatal("target_endpoint no longer uses the public ExternalIPAttachmentEndpoint enum")
	}
	for number, name := range map[protoreflect.EnumNumber]string{
		0: "EXTERNAL_IP_ATTACHMENT_ENDPOINT_UNSPECIFIED",
		1: "EXTERNAL_IP_ATTACHMENT_ENDPOINT_API",
		2: "EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS",
	} {
		value := endpoint.Enum().Values().ByNumber(number)
		if value == nil || value.Name() != protoreflect.Name(name) {
			t.Fatalf("endpoint enum value %d changed", number)
		}
	}
}

func TestExternalIPAttributionValidation(t *testing.T) {
	validator, err := protovalidate.New()
	if err != nil {
		t.Fatal(err)
	}

	validCompute := privatev1.ExternalIPAttribution_builder{
		ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: "compute-1"}.Build(),
	}.Build()
	if err := validator.Validate(validCompute); err != nil {
		t.Fatalf("valid compute attribution rejected: %v", err)
	}

	invalidTarget := privatev1.ExternalIPAttribution_builder{}.Build()
	if err := validator.Validate(invalidTarget); err == nil {
		t.Fatal("attribution without a target was accepted")
	}

	invalidComputeID := privatev1.ExternalIPAttribution_builder{
		ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{}.Build(),
	}.Build()
	if err := validator.Validate(invalidComputeID); err == nil {
		t.Fatal("attribution with an empty compute instance ID was accepted")
	}

	invalidClusterID := privatev1.ExternalIPAttribution_builder{
		Cluster:  privatev1.ClusterLocalReference_builder{}.Build(),
		Endpoint: privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
	}.Build()
	if err := validator.Validate(invalidClusterID); err == nil {
		t.Fatal("attribution with an empty cluster ID was accepted")
	}

	invalidBareMetalID := privatev1.ExternalIPAttribution_builder{
		BaremetalInstance: privatev1.BareMetalInstanceLocalReference_builder{}.Build(),
	}.Build()
	if err := validator.Validate(invalidBareMetalID); err == nil {
		t.Fatal("attribution with an empty bare-metal instance ID was accepted")
	}

	invalidEndpoint := privatev1.ExternalIPAttribution_builder{
		ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: "compute-1"}.Build(),
		Endpoint:        privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
	}.Build()
	if err := validator.Validate(invalidEndpoint); err == nil {
		t.Fatal("endpoint on a non-cluster attribution was accepted")
	}

	invalidClusterEndpoint := privatev1.ExternalIPAttribution_builder{
		Cluster: privatev1.ClusterLocalReference_builder{Id: "cluster-1"}.Build(),
	}.Build()
	if err := validator.Validate(invalidClusterEndpoint); err == nil {
		t.Fatal("cluster attribution without an endpoint was accepted")
	}

	invalidUnknownEndpoint := privatev1.ExternalIPAttribution_builder{
		Cluster:  privatev1.ClusterLocalReference_builder{Id: "cluster-1"}.Build(),
		Endpoint: privatev1.ExternalIPAttachmentEndpoint(99),
	}.Build()
	if err := validator.Validate(invalidUnknownEndpoint); err == nil {
		t.Fatal("cluster attribution with an unknown endpoint was accepted")
	}

	validCluster := privatev1.ExternalIPAttribution_builder{
		Cluster:  privatev1.ClusterLocalReference_builder{Id: "cluster-1"}.Build(),
		Endpoint: privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
	}.Build()
	if err := validator.Validate(validCluster); err != nil {
		t.Fatalf("valid cluster attribution rejected: %v", err)
	}

	invalidAttachmentTarget := publicv1.ExternalIPAttachmentSpec_builder{
		ExternalIp: publicv1.ExternalIPLocalReference_builder{Id: "external-ip-1"}.Build(),
	}.Build()
	if err := validator.Validate(invalidAttachmentTarget); err == nil {
		t.Fatal("attachment without a target was accepted")
	}

	invalidPublicEndpoint := publicv1.ExternalIPAttachmentSpec_builder{
		ExternalIp:     publicv1.ExternalIPLocalReference_builder{Id: "external-ip-1"}.Build(),
		Cluster:        publicv1.ClusterLocalReference_builder{Id: "cluster-1"}.Build(),
		TargetEndpoint: publicv1.ExternalIPAttachmentEndpoint(99),
	}.Build()
	if err := validator.Validate(invalidPublicEndpoint); err == nil {
		t.Fatal("attachment with an unknown endpoint was accepted")
	}

	invalidPublicClusterEndpoint := publicv1.ExternalIPAttachmentSpec_builder{
		ExternalIp: publicv1.ExternalIPLocalReference_builder{Id: "external-ip-1"}.Build(),
		Cluster:    publicv1.ClusterLocalReference_builder{Id: "cluster-1"}.Build(),
	}.Build()
	if err := validator.Validate(invalidPublicClusterEndpoint); err == nil {
		t.Fatal("attachment with an unspecified cluster endpoint was accepted")
	}

	invalidPublicComputeEndpoint := publicv1.ExternalIPAttachmentSpec_builder{
		ExternalIp:      publicv1.ExternalIPLocalReference_builder{Id: "external-ip-1"}.Build(),
		ComputeInstance: publicv1.ComputeInstanceLocalReference_builder{Id: "compute-1"}.Build(),
		TargetEndpoint:  publicv1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
	}.Build()
	if err := validator.Validate(invalidPublicComputeEndpoint); err == nil {
		t.Fatal("attachment with an endpoint on a non-cluster target was accepted")
	}
}
