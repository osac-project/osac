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
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type fakeExternalIPAttachmentsCleanupClient struct {
	items                 []*privatev1.ExternalIPAttachment
	deleteCalls           []string
	deleteErr             error
	deleteErrBeforeAccept bool
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
		deleteErr := f.deleteErr
		f.deleteErr = nil
		if deleteErr != nil && f.deleteErrBeforeAccept {
			f.deleteErrBeforeAccept = false
			return nil, deleteErr
		}
		if len(item.GetMetadata().GetFinalizers()) > 0 {
			item.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
		} else {
			f.items = append(f.items[:i], f.items[i+1:]...)
		}
		if deleteErr != nil {
			return nil, deleteErr
		}
		return &privatev1.ExternalIPAttachmentsDeleteResponse{}, nil
	}
	return nil, status.Error(codes.NotFound, "attachment not found")
}

type fakeExternalIPsCleanupClient struct {
	items                 []*privatev1.ExternalIP
	deleteCalls           []string
	deleteErr             error
	deleteErrBeforeAccept bool
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
		deleteErr := f.deleteErr
		f.deleteErr = nil
		if deleteErr != nil && f.deleteErrBeforeAccept {
			f.deleteErrBeforeAccept = false
			return nil, deleteErr
		}
		if len(item.GetMetadata().GetFinalizers()) > 0 {
			item.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
		} else {
			f.items = append(f.items[:i], f.items[i+1:]...)
		}
		if deleteErr != nil {
			return nil, deleteErr
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

func TestWorkloadNetworkingCleanupResumesAfterRestartWhenDeleteResponseIsLost(t *testing.T) {
	attachment := privatev1.ExternalIPAttachment_builder{
		Id: "attachment-retry",
		Metadata: privatev1.Metadata_builder{
			Finalizers: []string{"test/hold"},
			Labels: map[string]string{
				workloadAutoCreatedLabel:    "true",
				workloadAutoCreatedForLabel: "network-owner",
			},
		}.Build(),
	}.Build()
	externalIP := privatev1.ExternalIP_builder{
		Id: "external-ip-retry",
		Metadata: privatev1.Metadata_builder{
			Creator:    workloadSystemCreator,
			Finalizers: []string{"test/hold"},
			Labels: map[string]string{
				workloadAutoCreatedLabel:    "true",
				workloadAutoCreatedForLabel: "network-owner",
			},
		}.Build(),
	}.Build()
	attachments := &fakeExternalIPAttachmentsCleanupClient{
		items:     []*privatev1.ExternalIPAttachment{attachment},
		deleteErr: errors.New("connection lost after delete was accepted"),
	}
	externalIPs := &fakeExternalIPsCleanupClient{items: []*privatev1.ExternalIP{externalIP}}
	newCleanup := func() *workloadNetworkingCleanup {
		// The clients represent persisted API state; constructing a new cleanup
		// object models the controller restarting without in-memory progress.
		return &workloadNetworkingCleanup{attachments: attachments, externalIPs: externalIPs}
	}

	complete, err := newCleanup().Cleanup(context.Background(), "network-owner")
	if err == nil || complete {
		t.Fatalf("first cleanup = (%t, %v), want a retryable error", complete, err)
	}

	complete, err = newCleanup().Cleanup(context.Background(), "network-owner")
	if err != nil || complete {
		t.Fatalf("cleanup after attachment delete response loss = (%t, %v), want pending without error", complete, err)
	}
	if len(attachments.deleteCalls) != 1 {
		t.Fatalf("restarted cleanup repeated accepted attachment delete: %v", attachments.deleteCalls)
	}
	if len(externalIPs.deleteCalls) != 0 {
		t.Fatalf("released ExternalIP before attachment cleanup completed: %v", externalIPs.deleteCalls)
	}

	attachments.items = nil
	externalIPs.deleteErr = errors.New("connection lost after delete was accepted")
	complete, err = newCleanup().Cleanup(context.Background(), "network-owner")
	if err == nil || complete {
		t.Fatalf("ExternalIP delete cleanup = (%t, %v), want a retryable error", complete, err)
	}

	complete, err = newCleanup().Cleanup(context.Background(), "network-owner")
	if err != nil || complete {
		t.Fatalf("cleanup after ExternalIP delete response loss = (%t, %v), want pending without error", complete, err)
	}
	if len(externalIPs.deleteCalls) != 1 {
		t.Fatalf("restarted cleanup repeated accepted ExternalIP delete: %v", externalIPs.deleteCalls)
	}

	externalIPs.items = nil
	complete, err = newCleanup().Cleanup(context.Background(), "network-owner")
	if err != nil || !complete {
		t.Fatalf("cleanup after resources disappeared = (%t, %v), want complete", complete, err)
	}
}

func TestWorkloadNetworkingCleanupDoesNotDeleteNonSystemExternalIPWithCopiedOwnerLabels(t *testing.T) {
	externalIP := privatev1.ExternalIP_builder{
		Id: "tenant-external-ip",
		Metadata: privatev1.Metadata_builder{
			Creator: "tenant-user",
			Labels: map[string]string{
				workloadAutoCreatedLabel:    "true",
				workloadAutoCreatedForLabel: "network-owner",
			},
		}.Build(),
	}.Build()
	externalIPs := &fakeExternalIPsCleanupClient{items: []*privatev1.ExternalIP{externalIP}}
	cleanup := &workloadNetworkingCleanup{
		attachments: &fakeExternalIPAttachmentsCleanupClient{},
		externalIPs: externalIPs,
	}

	complete, err := cleanup.Cleanup(context.Background(), "network-owner")
	if err != nil {
		t.Fatalf("cleanup returned error: %v", err)
	}
	if !complete {
		t.Fatal("cleanup should ignore an ExternalIP not created by the system")
	}
	if len(externalIPs.deleteCalls) != 0 {
		t.Fatalf("cleanup deleted a tenant-created ExternalIP: %v", externalIPs.deleteCalls)
	}
}

func TestWorkloadNetworkingCleanupRetriesDeleteFailuresBeforeAcceptance(t *testing.T) {
	attachment := privatev1.ExternalIPAttachment_builder{
		Id: "attachment-transient-failure",
		Metadata: privatev1.Metadata_builder{
			Finalizers: []string{"test/hold"},
			Labels: map[string]string{
				workloadAutoCreatedLabel:    "true",
				workloadAutoCreatedForLabel: "network-owner",
			},
		}.Build(),
	}.Build()
	externalIP := privatev1.ExternalIP_builder{
		Id: "external-ip-transient-failure",
		Metadata: privatev1.Metadata_builder{
			Creator: workloadSystemCreator,
			Labels: map[string]string{
				workloadAutoCreatedLabel:    "true",
				workloadAutoCreatedForLabel: "network-owner",
			},
		}.Build(),
	}.Build()
	attachments := &fakeExternalIPAttachmentsCleanupClient{
		items:                 []*privatev1.ExternalIPAttachment{attachment},
		deleteErr:             errors.New("temporary detach failure"),
		deleteErrBeforeAccept: true,
	}
	externalIPs := &fakeExternalIPsCleanupClient{
		items:                 []*privatev1.ExternalIP{externalIP},
		deleteErr:             errors.New("temporary IP release failure"),
		deleteErrBeforeAccept: true,
	}
	cleanup := &workloadNetworkingCleanup{attachments: attachments, externalIPs: externalIPs}

	complete, err := cleanup.Cleanup(context.Background(), "network-owner")
	if err == nil || complete {
		t.Fatalf("first attachment cleanup = (%t, %v), want a retryable error", complete, err)
	}
	complete, err = cleanup.Cleanup(context.Background(), "network-owner")
	if err != nil || complete {
		t.Fatalf("retry after detach failure = (%t, %v), want pending finalizer", complete, err)
	}
	if len(attachments.deleteCalls) != 2 || !attachment.GetMetadata().HasDeletionTimestamp() {
		t.Fatalf("retry should accept one detach and hold on its finalizer: calls=%v deletion=%t",
			attachments.deleteCalls, attachment.GetMetadata().HasDeletionTimestamp())
	}
	complete, err = cleanup.Cleanup(context.Background(), "network-owner")
	if err != nil || complete || len(attachments.deleteCalls) != 2 {
		t.Fatalf("terminating attachment retry = (%t, %v), want no repeated delete: calls=%v",
			complete, err, attachments.deleteCalls)
	}

	attachments.items = nil
	complete, err = cleanup.Cleanup(context.Background(), "network-owner")
	if err == nil || complete {
		t.Fatalf("first IP release = (%t, %v), want a retryable error", complete, err)
	}
	complete, err = cleanup.Cleanup(context.Background(), "network-owner")
	if err != nil || !complete {
		t.Fatalf("retry after IP release failure = (%t, %v), want complete", complete, err)
	}
	if len(externalIPs.deleteCalls) != 2 {
		t.Fatalf("expected exactly one failed and one successful IP release, got %v", externalIPs.deleteCalls)
	}
}
