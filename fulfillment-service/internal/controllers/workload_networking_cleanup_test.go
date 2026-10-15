/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type fakeExternalIPAttachmentsCleanupClient struct {
	items       []*privatev1.ExternalIPAttachment
	deleteCalls []string
}

func (f *fakeExternalIPAttachmentsCleanupClient) List(_ context.Context, request *privatev1.ExternalIPAttachmentsListRequest,
	_ ...grpc.CallOption) (*privatev1.ExternalIPAttachmentsListResponse, error) {
	if !strings.Contains(request.GetFilter(), "network-owner") {
		return nil, status.Error(codes.InvalidArgument, "owner filter missing")
	}
	return &privatev1.ExternalIPAttachmentsListResponse{Items: f.items}, nil
}

func (f *fakeExternalIPAttachmentsCleanupClient) Delete(_ context.Context, request *privatev1.ExternalIPAttachmentsDeleteRequest,
	_ ...grpc.CallOption) (*privatev1.ExternalIPAttachmentsDeleteResponse, error) {
	for i, item := range f.items {
		if item.GetId() != request.GetId() {
			continue
		}
		f.deleteCalls = append(f.deleteCalls, request.GetId())
		if len(item.GetMetadata().GetFinalizers()) > 0 {
			item.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
		} else {
			f.items = append(f.items[:i], f.items[i+1:]...)
		}
		return &privatev1.ExternalIPAttachmentsDeleteResponse{}, nil
	}
	return nil, status.Error(codes.NotFound, "attachment not found")
}

type fakeExternalIPsCleanupClient struct {
	items       []*privatev1.ExternalIP
	deleteCalls []string
}

func (f *fakeExternalIPsCleanupClient) List(_ context.Context, request *privatev1.ExternalIPsListRequest,
	_ ...grpc.CallOption) (*privatev1.ExternalIPsListResponse, error) {
	if !strings.Contains(request.GetFilter(), "network-owner") {
		return nil, status.Error(codes.InvalidArgument, "owner filter missing")
	}
	return &privatev1.ExternalIPsListResponse{Items: f.items}, nil
}

func (f *fakeExternalIPsCleanupClient) Delete(_ context.Context, request *privatev1.ExternalIPsDeleteRequest,
	_ ...grpc.CallOption) (*privatev1.ExternalIPsDeleteResponse, error) {
	for i, item := range f.items {
		if item.GetId() != request.GetId() {
			continue
		}
		f.deleteCalls = append(f.deleteCalls, request.GetId())
		if len(item.GetMetadata().GetFinalizers()) > 0 {
			item.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
		} else {
			f.items = append(f.items[:i], f.items[i+1:]...)
		}
		return &privatev1.ExternalIPsDeleteResponse{}, nil
	}
	return nil, status.Error(codes.NotFound, "ExternalIP not found")
}

func TestWorkloadNetworkingCleanupWaitsForFinalizersAndDoesNotRepeatDeletes(t *testing.T) {
	attachment := privatev1.ExternalIPAttachment_builder{
		Id: "attachment-1",
		Metadata: privatev1.Metadata_builder{
			Finalizers: []string{"test/hold"},
			Labels: map[string]string{
				workloadAutoCreatedLabel:    "true",
				workloadAutoCreatedForLabel: "network-owner",
			},
		}.Build(),
	}.Build()
	externalIP := privatev1.ExternalIP_builder{
		Id: "external-ip-1",
		Metadata: privatev1.Metadata_builder{
			Creator:    workloadSystemCreator,
			Finalizers: []string{"test/hold"},
			Labels: map[string]string{
				workloadAutoCreatedLabel:    "true",
				workloadAutoCreatedForLabel: "network-owner",
			},
		}.Build(),
	}.Build()
	attachments := &fakeExternalIPAttachmentsCleanupClient{items: []*privatev1.ExternalIPAttachment{attachment}}
	externalIPs := &fakeExternalIPsCleanupClient{items: []*privatev1.ExternalIP{externalIP}}
	cleanup := &workloadNetworkingCleanup{attachments: attachments, externalIPs: externalIPs}

	complete, err := cleanup.Cleanup(context.Background(), "network-owner")
	if err != nil {
		t.Fatalf("first cleanup returned error: %v", err)
	}
	if complete {
		t.Fatal("cleanup completed while the attachment finalizer was held")
	}
	if len(attachments.deleteCalls) != 1 || attachments.deleteCalls[0] != "attachment-1" {
		t.Fatalf("expected one attachment delete, got %v", attachments.deleteCalls)
	}
	if len(externalIPs.deleteCalls) != 0 {
		t.Fatalf("deleted ExternalIP before its attachment was gone: %v", externalIPs.deleteCalls)
	}

	complete, err = cleanup.Cleanup(context.Background(), "network-owner")
	if err != nil {
		t.Fatalf("second cleanup returned error: %v", err)
	}
	if complete || len(attachments.deleteCalls) != 1 {
		t.Fatalf("terminating attachment should remain pending without another delete: complete=%t deletes=%v",
			complete, attachments.deleteCalls)
	}

	attachments.items = nil
	complete, err = cleanup.Cleanup(context.Background(), "network-owner")
	if err != nil {
		t.Fatalf("third cleanup returned error: %v", err)
	}
	if complete || len(externalIPs.deleteCalls) != 1 || externalIPs.deleteCalls[0] != "external-ip-1" {
		t.Fatalf("expected ExternalIP deletion to start once after attachment removal: complete=%t deletes=%v",
			complete, externalIPs.deleteCalls)
	}

	complete, err = cleanup.Cleanup(context.Background(), "network-owner")
	if err != nil {
		t.Fatalf("fourth cleanup returned error: %v", err)
	}
	if complete || len(externalIPs.deleteCalls) != 1 {
		t.Fatalf("terminating ExternalIP should remain pending without another delete: complete=%t deletes=%v",
			complete, externalIPs.deleteCalls)
	}

	externalIPs.items = nil
	complete, err = cleanup.Cleanup(context.Background(), "network-owner")
	if err != nil {
		t.Fatalf("final cleanup returned error: %v", err)
	}
	if !complete {
		t.Fatal("cleanup did not complete after both resources disappeared")
	}
}
