/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"testing"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type concurrentComputeInstanceStatusClient struct {
	privatev1.ComputeInstancesClient
	object         *privatev1.ComputeInstance
	getCalls       int
	updateRequests []*privatev1.ComputeInstancesUpdateRequest
}

func (c *concurrentComputeInstanceStatusClient) Get(
	_ context.Context, _ *privatev1.ComputeInstancesGetRequest, _ ...grpc.CallOption,
) (*privatev1.ComputeInstancesGetResponse, error) {
	c.getCalls++
	return privatev1.ComputeInstancesGetResponse_builder{
		Object: proto.Clone(c.object).(*privatev1.ComputeInstance),
	}.Build(), nil
}

func (c *concurrentComputeInstanceStatusClient) Update(
	_ context.Context, request *privatev1.ComputeInstancesUpdateRequest, _ ...grpc.CallOption,
) (*privatev1.ComputeInstancesUpdateResponse, error) {
	c.updateRequests = append(c.updateRequests, proto.Clone(request).(*privatev1.ComputeInstancesUpdateRequest))
	if len(c.updateRequests) == 1 {
		// A controller commits another status field after the fixture's first Get.
		c.object.GetStatus().SetExternalIpAddress("198.51.100.7")
		c.object.GetMetadata().SetVersion(c.object.GetMetadata().GetVersion() + 1)
	}
	if request.GetObject().GetMetadata().GetVersion() != c.object.GetMetadata().GetVersion() {
		return nil, status.Error(codes.Aborted, "object has been modified")
	}
	c.object.SetStatus(proto.Clone(request.GetObject().GetStatus()).(*privatev1.ComputeInstanceStatus))
	c.object.GetMetadata().SetVersion(c.object.GetMetadata().GetVersion() + 1)
	return privatev1.ComputeInstancesUpdateResponse_builder{
		Object: proto.Clone(c.object).(*privatev1.ComputeInstance),
	}.Build(), nil
}

func TestComputeInstanceWholeStatusUpdateRetriesConcurrentWriter(t *testing.T) {
	client := &concurrentComputeInstanceStatusClient{object: privatev1.ComputeInstance_builder{
		Id:       "instance-123",
		Metadata: privatev1.Metadata_builder{Version: 1}.Build(),
		Status:   privatev1.ComputeInstanceStatus_builder{}.Build(),
	}.Build()}

	err := updateComputeInstanceTestStatus(context.Background(), client, "instance-123", "status", "192.0.2.42")
	if err != nil {
		t.Fatalf("update status: %v", err)
	}
	if client.getCalls != 2 || len(client.updateRequests) != 2 {
		t.Fatalf("expected a fresh Get and Update after conflict, got %d Gets and %d Updates",
			client.getCalls, len(client.updateRequests))
	}
	for i, request := range client.updateRequests {
		if !request.GetLock() || len(request.GetUpdateMask().GetPaths()) != 1 || request.GetUpdateMask().GetPaths()[0] != "status" {
			t.Fatalf("update %d must lock the full status mask: %v", i, request)
		}
		if got := request.GetObject().GetMetadata().GetVersion(); got != int32(i+1) {
			t.Fatalf("update %d used version %d, want %d", i, got, i+1)
		}
	}
	if got := client.object.GetStatus().GetInternalIpAddress(); got != "192.0.2.42" {
		t.Fatalf("internal IP = %q, want fixture feedback", got)
	}
	if got := client.object.GetStatus().GetExternalIpAddress(); got != "198.51.100.7" {
		t.Fatalf("concurrent status field = %q, want preserved value", got)
	}
}
