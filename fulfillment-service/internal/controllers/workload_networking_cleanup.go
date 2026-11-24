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
	"fmt"
	"strconv"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const (
	workloadAutoCreatedLabel        = "osac.openshift.io/auto-created"
	workloadAutoCreatedForLabel     = "osac.openshift.io/auto-created-for"
	workloadAutoCreatedKindLabel    = "osac.openshift.io/auto-created-kind"
	workloadAutoProvisionedLabel    = "osac.openshift.io/auto-provisioned"
	workloadAutoProvisionedForLabel = "osac.openshift.io/auto-provisioned-for"
	workloadSystemCreator           = "system"
)

// WorkloadNetworkingCleanup removes Fulfillment-owned automatic networking
// resources. External access is cleaned before deleting the workload's hub CR;
// for Clusters, the automatic SecurityGroup is cleaned after the ClusterOrder
// and its workers have completed teardown.
type WorkloadNetworkingCleanup interface {
	CleanupExternalAccess(ctx context.Context, workloadID string) (complete bool, err error)
	Cleanup(ctx context.Context, workloadID string) (complete bool, err error)
}

type externalIPAttachmentsCleanupClient interface {
	List(context.Context, *privatev1.ExternalIPAttachmentsListRequest, ...grpc.CallOption) (*privatev1.ExternalIPAttachmentsListResponse, error)
	Delete(context.Context, *privatev1.ExternalIPAttachmentsDeleteRequest, ...grpc.CallOption) (*privatev1.ExternalIPAttachmentsDeleteResponse, error)
}

type externalIPsCleanupClient interface {
	List(context.Context, *privatev1.ExternalIPsListRequest, ...grpc.CallOption) (*privatev1.ExternalIPsListResponse, error)
	Delete(context.Context, *privatev1.ExternalIPsDeleteRequest, ...grpc.CallOption) (*privatev1.ExternalIPsDeleteResponse, error)
}

type securityGroupsCleanupClient interface {
	List(context.Context, *privatev1.SecurityGroupsListRequest, ...grpc.CallOption) (*privatev1.SecurityGroupsListResponse, error)
	Delete(context.Context, *privatev1.SecurityGroupsDeleteRequest, ...grpc.CallOption) (*privatev1.SecurityGroupsDeleteResponse, error)
}

type workloadNetworkingCleanup struct {
	attachments    externalIPAttachmentsCleanupClient
	externalIPs    externalIPsCleanupClient
	securityGroups securityGroupsCleanupClient
}

// NewWorkloadNetworkingCleanup creates the shared cleanup path used by workload
// reconcilers. Workload reconcilers keep their existing finalizer until this
// cleanup reports complete.
func NewWorkloadNetworkingCleanup(connection grpc.ClientConnInterface) WorkloadNetworkingCleanup {
	return &workloadNetworkingCleanup{
		attachments:    privatev1.NewExternalIPAttachmentsClient(connection),
		externalIPs:    privatev1.NewExternalIPsClient(connection),
		securityGroups: privatev1.NewSecurityGroupsClient(connection),
	}
}

func (c *workloadNetworkingCleanup) Cleanup(ctx context.Context, workloadID string) (bool, error) {
	if workloadID == "" {
		return false, errors.New("workload identifier is mandatory for networking cleanup")
	}
	if c.securityGroups == nil {
		return false, errors.New("networking cleanup clients are not configured")
	}
	complete, err := c.CleanupExternalAccess(ctx, workloadID)
	if err != nil || !complete {
		return complete, err
	}
	return c.cleanupAutomaticClusterSecurityGroups(ctx, workloadID)
}

// CleanupExternalAccess removes automatic ExternalIPAttachments and
// ExternalIPs before the workload's hub CR is deleted. For a Cluster, the
// ClusterOrder still exists to remove public DNS holds from its attachments.
func (c *workloadNetworkingCleanup) CleanupExternalAccess(ctx context.Context, workloadID string) (bool, error) {
	if workloadID == "" {
		return false, errors.New("workload identifier is mandatory for networking cleanup")
	}
	if c.attachments == nil || c.externalIPs == nil {
		return false, errors.New("networking cleanup clients are not configured")
	}
	filter := automaticWorkloadNetworkFilter(workloadID)
	attachments, err := c.attachments.List(ctx, privatev1.ExternalIPAttachmentsListRequest_builder{
		Filter: &filter,
	}.Build())
	if err != nil {
		return false, fmt.Errorf("failed to list automatic ExternalIPAttachments for workload %s: %w", workloadID, err)
	}
	if len(attachments.GetItems()) > 0 {
		for _, attachment := range attachments.GetItems() {
			if attachment.GetMetadata().HasDeletionTimestamp() {
				continue
			}
			_, err = c.attachments.Delete(ctx, privatev1.ExternalIPAttachmentsDeleteRequest_builder{
				Id: attachment.GetId(),
			}.Build())
			if err != nil && status.Code(err) != codes.NotFound {
				return false, fmt.Errorf("failed to delete automatic ExternalIPAttachment %s for workload %s: %w",
					attachment.GetId(), workloadID, err)
			}
		}
		// Keep the target workload record available while attachment finalizers
		// finish: attachment lifecycle updates lock that target before detaching.
		return false, nil
	}

	complete, err := c.cleanupExternalIPs(ctx, workloadID, filter)
	if err != nil || !complete {
		return complete, err
	}
	return true, nil
}

func (c *workloadNetworkingCleanup) cleanupExternalIPs(ctx context.Context, workloadID, filter string) (bool, error) {
	response, err := c.externalIPs.List(ctx, privatev1.ExternalIPsListRequest_builder{
		Filter: &filter,
	}.Build())
	if err != nil {
		return false, fmt.Errorf("failed to list automatic ExternalIPs for workload %s: %w", workloadID, err)
	}

	pending := false
	for _, externalIP := range response.GetItems() {
		// The protected labels identify the owner; the creator check also avoids
		// deleting an object that merely copied those labels through an internal API.
		if externalIP.GetMetadata().GetCreator() != workloadSystemCreator {
			continue
		}
		pending = true
		if externalIP.GetMetadata().HasDeletionTimestamp() {
			continue
		}
		_, err = c.externalIPs.Delete(ctx, privatev1.ExternalIPsDeleteRequest_builder{
			Id: externalIP.GetId(),
		}.Build())
		if err != nil {
			switch status.Code(err) {
			case codes.NotFound:
				continue
			case codes.FailedPrecondition:
				// A remaining consumer still references this address. The API has
				// correctly refused deletion; retry after that reference is removed.
				continue
			default:
				return false, fmt.Errorf("failed to delete automatic ExternalIP %s for workload %s: %w",
					externalIP.GetId(), workloadID, err)
			}
		}
	}
	if !pending {
		return true, nil
	}

	// Delete may remove an unfinalized object immediately. Re-list to complete
	// in one pass where possible, while finalizer-held objects remain pending.
	remaining, err := c.externalIPs.List(ctx, privatev1.ExternalIPsListRequest_builder{
		Filter: &filter,
	}.Build())
	if err != nil {
		return false, fmt.Errorf("failed to verify automatic ExternalIP cleanup for workload %s: %w", workloadID, err)
	}
	for _, externalIP := range remaining.GetItems() {
		if externalIP.GetMetadata().GetCreator() == workloadSystemCreator {
			return false, nil
		}
	}
	return true, nil
}

func (c *workloadNetworkingCleanup) cleanupAutomaticClusterSecurityGroups(
	ctx context.Context,
	clusterID string,
) (bool, error) {
	filter := automaticClusterSecurityGroupFilter(clusterID)
	response, err := c.securityGroups.List(ctx, privatev1.SecurityGroupsListRequest_builder{
		Filter: &filter,
	}.Build())
	if err != nil {
		return false, fmt.Errorf("failed to list automatic SecurityGroups for Cluster %s: %w", clusterID, err)
	}
	pending := false
	for _, group := range response.GetItems() {
		if group.GetMetadata().GetCreator() != workloadSystemCreator {
			continue
		}
		pending = true
		if group.GetMetadata().HasDeletionTimestamp() {
			continue
		}
		_, err := c.securityGroups.Delete(ctx, privatev1.SecurityGroupsDeleteRequest_builder{
			Id: group.GetId(),
		}.Build())
		if err != nil {
			switch status.Code(err) {
			case codes.NotFound:
				continue
			case codes.FailedPrecondition:
				// A retained worker or another dependency still uses the group.
				continue
			default:
				return false, fmt.Errorf("failed to delete automatic SecurityGroup %s for Cluster %s: %w",
					group.GetId(), clusterID, err)
			}
		}
	}
	if !pending {
		return true, nil
	}
	remaining, err := c.securityGroups.List(ctx, privatev1.SecurityGroupsListRequest_builder{
		Filter: &filter,
	}.Build())
	if err != nil {
		return false, fmt.Errorf("failed to verify automatic SecurityGroup cleanup for Cluster %s: %w", clusterID, err)
	}
	for _, group := range remaining.GetItems() {
		if group.GetMetadata().GetCreator() == workloadSystemCreator {
			return false, nil
		}
	}
	return true, nil
}

func automaticWorkloadNetworkFilter(workloadID string) string {
	id := strconv.Quote(workloadID)
	return fmt.Sprintf(
		"(%s && %s) || (%s && %s)",
		"this.metadata.labels['"+workloadAutoCreatedLabel+"'] == 'true'",
		"this.metadata.labels['"+workloadAutoCreatedForLabel+"'] == "+id,
		"this.metadata.labels['"+workloadAutoProvisionedLabel+"'] == 'true'",
		"this.metadata.labels['"+workloadAutoProvisionedForLabel+"'] == "+id,
	)
}

func automaticClusterSecurityGroupFilter(clusterID string) string {
	id := strconv.Quote(clusterID)
	return fmt.Sprintf("%s && %s && %s",
		"this.metadata.labels['"+workloadAutoCreatedLabel+"'] == 'true'",
		"this.metadata.labels['"+workloadAutoCreatedForLabel+"'] == "+id,
		"this.metadata.labels['"+workloadAutoCreatedKindLabel+"'] == 'cluster'")
}

// ReconcileWorkloadNetworkingCleanup keeps the workload finalizer in place and
// retries while automatic network resources are still terminating.
func ReconcileWorkloadNetworkingCleanup(
	ctx context.Context,
	cleanup WorkloadNetworkingCleanup,
	workloadID string,
) error {
	if cleanup == nil {
		return errors.New("workload networking cleanup is not configured")
	}
	complete, err := cleanup.Cleanup(ctx, workloadID)
	if err != nil {
		return RequeueAfter(fmt.Errorf("failed to clean up networking resources for workload %s: %w", workloadID, err), time.Second)
	}
	if !complete {
		return RequeueAfterNetworkingCleanup()
	}
	return nil
}
