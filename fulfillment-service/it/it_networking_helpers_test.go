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
	"fmt"
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// The fulfillment-service integration suite does not run the networking
// operator. This placeholder marks test NetworkClasses as VM-capable so API
// tests can exercise attachment validation without testing manager dispatch.
const testVMK8SManagerName = "test-k8s-manager"

func waitForNetworkingFixtureResource(ctx context.Context, get func(context.Context) error) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		g.Expect(get(ctx)).ToNot(HaveOccurred())
	}, time.Minute, time.Second).Should(Succeed())
}

func cleanupNetworkFixtureResource(
	ctx context.Context,
	deleteResource func(context.Context) error,
	getMetadata func(context.Context) (*privatev1.Metadata, error),
	clearFinalizers func(context.Context) error,
) error {
	if err := deleteResource(ctx); err != nil && grpcstatus.Code(err) != grpccodes.NotFound {
		return err
	}
	const maxFinalizerUpdateAttempts = 5
	const deleteTimeout = time.Minute
	const pollInterval = 100 * time.Millisecond
	deadline := time.NewTimer(deleteTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		metadata, err := getMetadata(ctx)
		if grpcstatus.Code(err) == grpccodes.NotFound {
			return nil
		}
		if err != nil {
			return err
		}
		if metadata.HasDeletionTimestamp() && len(metadata.GetFinalizers()) > 0 {
			var clearErr error
			for attempt := 0; attempt < maxFinalizerUpdateAttempts; attempt++ {
				clearErr = clearFinalizers(ctx)
				if grpcstatus.Code(clearErr) == grpccodes.NotFound {
					clearErr = nil
					break
				}
				if clearErr == nil || grpcstatus.Code(clearErr) != grpccodes.Aborted {
					break
				}
				if attempt < maxFinalizerUpdateAttempts-1 {
					// Controllers may update status between the cleanup Get and Update.
					// The caller re-reads the resource on each attempt, so retry version conflicts.
					time.Sleep(50 * time.Millisecond)
				}
			}
			if clearErr != nil {
				return fmt.Errorf("clear fixture finalizers after %d attempts: %w", maxFinalizerUpdateAttempts, clearErr)
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("resource remains after delete request")
		case <-ticker.C:
		}
	}
}

type networkClassFixtureCleanupSummary struct {
	SchemaVersion int    `json:"schema_version"`
	TotalNS       int64  `json:"total_ns"`
	Outcome       string `json:"outcome"`
}

func cleanupNetworkClassFixture(ctx context.Context, client privatev1.NetworkClassesClient, id string) error {
	summary := networkClassFixtureCleanupSummary{SchemaVersion: 1, Outcome: "interrupted"}
	totalStarted := time.Now()
	defer func() {
		summary.TotalNS = time.Since(totalStarted).Nanoseconds()
		_, reporterConfig := GinkgoConfiguration()
		if reporterConfig.JSONReport != "" {
			AddReportEntry("networkclass-fixture-cleanup", summary, ReportEntryVisibilityNever)
		}
	}()

	err := cleanupNetworkFixtureResource(ctx,
		func(ctx context.Context) error {
			_, err := client.Delete(ctx, privatev1.NetworkClassesDeleteRequest_builder{Id: id}.Build())
			return err
		},
		func(ctx context.Context) (*privatev1.Metadata, error) {
			response, err := client.Get(ctx, privatev1.NetworkClassesGetRequest_builder{Id: id}.Build())
			if err != nil {
				return nil, err
			}
			return response.GetObject().GetMetadata(), nil
		},
		func(ctx context.Context) error {
			response, err := client.Get(ctx, privatev1.NetworkClassesGetRequest_builder{Id: id}.Build())
			if err != nil {
				return err
			}
			object := response.GetObject()
			object.GetMetadata().SetFinalizers(nil)
			_, err = client.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
				Object: object, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}}, Lock: true,
			}.Build())
			return err
		})
	if err != nil {
		summary.Outcome = "error"
	} else {
		summary.Outcome = "success"
	}
	return err
}

func cleanupSecurityGroupFixture(ctx context.Context, client privatev1.SecurityGroupsClient, id string) error {
	return cleanupNetworkFixtureResource(ctx,
		func(ctx context.Context) error {
			_, err := client.Delete(ctx, privatev1.SecurityGroupsDeleteRequest_builder{Id: id}.Build())
			return err
		},
		func(ctx context.Context) (*privatev1.Metadata, error) {
			response, err := client.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: id}.Build())
			if err != nil {
				return nil, err
			}
			return response.GetObject().GetMetadata(), nil
		},
		func(ctx context.Context) error {
			response, err := client.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: id}.Build())
			if err != nil {
				return err
			}
			object := response.GetObject()
			object.GetMetadata().SetFinalizers(nil)
			_, err = client.Update(ctx, privatev1.SecurityGroupsUpdateRequest_builder{
				Object: object, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}}, Lock: true,
			}.Build())
			return err
		})
}

func cleanupExternalIPPoolFixture(ctx context.Context, client privatev1.ExternalIPPoolsClient, id string) error {
	return cleanupNetworkFixtureResource(ctx,
		func(ctx context.Context) error {
			_, err := client.Delete(ctx, privatev1.ExternalIPPoolsDeleteRequest_builder{Id: id}.Build())
			return err
		},
		func(ctx context.Context) (*privatev1.Metadata, error) {
			response, err := client.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: id}.Build())
			if err != nil {
				return nil, err
			}
			return response.GetObject().GetMetadata(), nil
		},
		func(ctx context.Context) error {
			response, err := client.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: id}.Build())
			if err != nil {
				return err
			}
			object := response.GetObject()
			object.GetMetadata().SetFinalizers(nil)
			_, err = client.Update(ctx, privatev1.ExternalIPPoolsUpdateRequest_builder{
				Object: object, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}}, Lock: true,
			}.Build())
			return err
		})
}

func cleanupExternalIPFixture(ctx context.Context, client privatev1.ExternalIPsClient, id string) error {
	return cleanupNetworkFixtureResource(ctx,
		func(ctx context.Context) error {
			_, err := client.Delete(ctx, privatev1.ExternalIPsDeleteRequest_builder{Id: id}.Build())
			return err
		},
		func(ctx context.Context) (*privatev1.Metadata, error) {
			response, err := client.Get(ctx, privatev1.ExternalIPsGetRequest_builder{Id: id}.Build())
			if err != nil {
				return nil, err
			}
			return response.GetObject().GetMetadata(), nil
		},
		func(ctx context.Context) error {
			response, err := client.Get(ctx, privatev1.ExternalIPsGetRequest_builder{Id: id}.Build())
			if err != nil {
				return err
			}
			object := response.GetObject()
			object.GetMetadata().SetFinalizers(nil)
			_, err = client.Update(ctx, privatev1.ExternalIPsUpdateRequest_builder{
				Object: object, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}}, Lock: true,
			}.Build())
			return err
		})
}

func setExternalIPFixtureState(
	ctx context.Context,
	client privatev1.ExternalIPsClient,
	id string,
	state privatev1.ExternalIPState,
) error {
	const maxAttempts = 5
	for attempt := 0; attempt < maxAttempts; attempt++ {
		response, err := client.Get(ctx, privatev1.ExternalIPsGetRequest_builder{Id: id}.Build())
		if err != nil {
			return err
		}
		ip := response.GetObject()
		if ip.GetStatus().GetState() == state {
			return nil
		}

		ip.GetStatus().SetState(state)
		ip.GetStatus().SetStateTransitionTime(nextFixtureStateTransitionTime(ip.GetStatus().GetStateTransitionTime()))
		_, err = client.Update(ctx, privatev1.ExternalIPsUpdateRequest_builder{
			Object: ip,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{
				"status.state", "status.state_transition_time",
			}},
			Lock: true,
		}.Build())
		if grpcstatus.Code(err) != grpccodes.Aborted {
			return err
		}
		if attempt < maxAttempts-1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	return fmt.Errorf("update ExternalIP %q state after %d attempts", id, maxAttempts)
}

func setNATGatewayFixtureState(
	ctx context.Context,
	client privatev1.NATGatewaysClient,
	id string,
	state privatev1.NATGatewayState,
) error {
	const maxAttempts = 5
	for attempt := 0; attempt < maxAttempts; attempt++ {
		response, err := client.Get(ctx, privatev1.NATGatewaysGetRequest_builder{Id: id}.Build())
		if err != nil {
			return err
		}
		gateway := response.GetObject()
		if gateway.GetStatus().GetState() == state {
			return nil
		}

		gateway.GetStatus().SetState(state)
		gateway.GetStatus().SetStateTransitionTime(nextFixtureStateTransitionTime(gateway.GetStatus().GetStateTransitionTime()))
		_, err = client.Update(ctx, privatev1.NATGatewaysUpdateRequest_builder{
			Object: gateway,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{
				"status.state", "status.state_transition_time",
			}},
			Lock: true,
		}.Build())
		if grpcstatus.Code(err) != grpccodes.Aborted {
			return err
		}
		if attempt < maxAttempts-1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	return fmt.Errorf("update NATGateway %q state after %d attempts", id, maxAttempts)
}

func nextFixtureStateTransitionTime(previous *timestamppb.Timestamp) *timestamppb.Timestamp {
	transitionTime := time.Now()
	if previous != nil {
		minimum := previous.AsTime().Add(time.Second)
		if transitionTime.Before(minimum) {
			transitionTime = minimum
		}
	}
	return timestamppb.New(transitionTime)
}

func cleanupExternalIPAttachmentFixture(
	ctx context.Context,
	client privatev1.ExternalIPAttachmentsClient,
	id string,
) error {
	return cleanupNetworkFixtureResource(ctx,
		func(ctx context.Context) error {
			_, err := client.Delete(ctx, privatev1.ExternalIPAttachmentsDeleteRequest_builder{Id: id}.Build())
			return err
		},
		func(ctx context.Context) (*privatev1.Metadata, error) {
			response, err := client.Get(ctx, privatev1.ExternalIPAttachmentsGetRequest_builder{Id: id}.Build())
			if err != nil {
				return nil, err
			}
			return response.GetObject().GetMetadata(), nil
		},
		func(ctx context.Context) error {
			response, err := client.Get(ctx, privatev1.ExternalIPAttachmentsGetRequest_builder{Id: id}.Build())
			if err != nil {
				return err
			}
			object := response.GetObject()
			object.GetMetadata().SetFinalizers(nil)
			_, err = client.Update(ctx, privatev1.ExternalIPAttachmentsUpdateRequest_builder{
				Object: object, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}}, Lock: true,
			}.Build())
			return err
		})
}

func mutateExternalIPAttachmentFinalizersWithRetry(
	ctx context.Context,
	client privatev1.ExternalIPAttachmentsClient,
	id string,
	mutate func([]string) []string,
) error {
	const maxAttempts = 5
	var err error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		response, getErr := client.Get(ctx, privatev1.ExternalIPAttachmentsGetRequest_builder{Id: id}.Build())
		if getErr != nil {
			return getErr
		}
		attachment := response.GetObject()
		finalizers := slices.Clone(attachment.GetMetadata().GetFinalizers())
		updatedFinalizers := mutate(finalizers)
		if slices.Equal(finalizers, updatedFinalizers) {
			return nil
		}
		attachment.GetMetadata().SetFinalizers(updatedFinalizers)
		_, err = client.Update(ctx, privatev1.ExternalIPAttachmentsUpdateRequest_builder{
			Object:     attachment,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}},
			Lock:       true,
		}.Build())
		if err == nil || grpcstatus.Code(err) != grpccodes.Aborted {
			return err
		}
		if attempt < maxAttempts-1 {
			time.Sleep(50 * time.Millisecond)
		}
	}
	return fmt.Errorf("update ExternalIPAttachment %q finalizers after %d attempts: %w", id, maxAttempts, err)
}

func addExternalIPAttachmentFinalizerWithRetry(
	ctx context.Context,
	client privatev1.ExternalIPAttachmentsClient,
	id string,
	finalizer string,
) error {
	return mutateExternalIPAttachmentFinalizersWithRetry(ctx, client, id, func(finalizers []string) []string {
		if slices.Contains(finalizers, finalizer) {
			return finalizers
		}
		return append(finalizers, finalizer)
	})
}

func removeExternalIPAttachmentFinalizerWithRetry(
	ctx context.Context,
	client privatev1.ExternalIPAttachmentsClient,
	id string,
	finalizer string,
) error {
	return mutateExternalIPAttachmentFinalizersWithRetry(ctx, client, id, func(finalizers []string) []string {
		return slices.DeleteFunc(finalizers, func(existing string) bool { return existing == finalizer })
	})
}

func cleanupNATGatewayFixture(
	ctx context.Context,
	publicClient publicv1.NATGatewaysClient,
	privateClient privatev1.NATGatewaysClient,
	id string,
) error {
	return cleanupNetworkFixtureResource(ctx,
		func(ctx context.Context) error {
			_, err := publicClient.Delete(ctx, publicv1.NATGatewaysDeleteRequest_builder{Id: id}.Build())
			return err
		},
		func(ctx context.Context) (*privatev1.Metadata, error) {
			response, err := privateClient.Get(ctx, privatev1.NATGatewaysGetRequest_builder{Id: id}.Build())
			if err != nil {
				return nil, err
			}
			return response.GetObject().GetMetadata(), nil
		},
		func(ctx context.Context) error {
			response, err := privateClient.Get(ctx, privatev1.NATGatewaysGetRequest_builder{Id: id}.Build())
			if err != nil {
				return err
			}
			object := response.GetObject()
			object.GetMetadata().SetFinalizers(nil)
			_, err = privateClient.Update(ctx, privatev1.NATGatewaysUpdateRequest_builder{
				Object: object, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}}, Lock: true,
			}.Build())
			return err
		})
}

func cleanupClusterFixture(
	ctx context.Context,
	publicClient publicv1.ClustersClient,
	privateClient privatev1.ClustersClient,
	id string,
) error {
	return cleanupNetworkFixtureResource(ctx,
		func(ctx context.Context) error {
			_, err := publicClient.Delete(ctx, publicv1.ClustersDeleteRequest_builder{Id: id}.Build())
			return err
		},
		func(ctx context.Context) (*privatev1.Metadata, error) {
			response, err := privateClient.Get(ctx, privatev1.ClustersGetRequest_builder{Id: id}.Build())
			if err != nil {
				return nil, err
			}
			return response.GetObject().GetMetadata(), nil
		},
		func(ctx context.Context) error {
			response, err := privateClient.Get(ctx, privatev1.ClustersGetRequest_builder{Id: id}.Build())
			if err != nil {
				return err
			}
			object := response.GetObject()
			object.GetMetadata().SetFinalizers(nil)
			_, err = privateClient.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: object, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}}, Lock: true,
			}.Build())
			return err
		})
}

func cleanupPrivateClusterFixture(ctx context.Context, client privatev1.ClustersClient, id string) error {
	return cleanupNetworkFixtureResource(ctx,
		func(ctx context.Context) error {
			_, err := client.Delete(ctx, privatev1.ClustersDeleteRequest_builder{Id: id}.Build())
			return err
		},
		func(ctx context.Context) (*privatev1.Metadata, error) {
			response, err := client.Get(ctx, privatev1.ClustersGetRequest_builder{Id: id}.Build())
			if err != nil {
				return nil, err
			}
			return response.GetObject().GetMetadata(), nil
		},
		func(ctx context.Context) error {
			response, err := client.Get(ctx, privatev1.ClustersGetRequest_builder{Id: id}.Build())
			if err != nil {
				return err
			}
			object := response.GetObject()
			object.GetMetadata().SetFinalizers(nil)
			_, err = client.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: object, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}}, Lock: true,
			}.Build())
			return err
		})
}

func setClusterFixtureReadiness(ctx context.Context, client privatev1.ClustersClient, id string, ready bool) error {
	const maxAttempts = 5
	const settleTimeout = time.Minute
	var err error
	deadline := time.NewTimer(settleTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	// Let the Cluster controller persist its initial PROGRESSING state before
	// overriding fixture readiness; otherwise an in-flight defaulting update can
	// race this status write and restore UNSPECIFIED as PROGRESSING.
	for {
		response, getErr := client.Get(ctx, privatev1.ClustersGetRequest_builder{Id: id}.Build())
		if getErr != nil {
			return getErr
		}
		if response.GetObject().GetStatus().GetState() != privatev1.ClusterState_CLUSTER_STATE_UNSPECIFIED {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("Cluster %q did not reach its initial state before readiness update", id)
		case <-ticker.C:
		}
	}

	state := privatev1.ClusterState_CLUSTER_STATE_PROGRESSING
	apiEndpoint, ingressEndpoint := "", ""
	if ready {
		state = privatev1.ClusterState_CLUSTER_STATE_READY
		apiEndpoint = "198.51.100.10"
		ingressEndpoint = "198.51.100.11"
	}
	for attempt := 0; attempt < maxAttempts; attempt++ {
		response, getErr := client.Get(ctx, privatev1.ClustersGetRequest_builder{Id: id}.Build())
		if getErr != nil {
			return getErr
		}
		cluster := response.GetObject()
		if !cluster.HasStatus() {
			cluster.SetStatus(&privatev1.ClusterStatus{})
		}
		cluster.GetStatus().SetState(state)
		cluster.GetStatus().SetApiEndpoint(apiEndpoint)
		cluster.GetStatus().SetIngressEndpoint(ingressEndpoint)
		_, err = client.Update(ctx, privatev1.ClustersUpdateRequest_builder{
			Object: cluster,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{
				"status.state", "status.api_endpoint", "status.ingress_endpoint",
			}},
		}.Build())
		if err == nil || grpcstatus.Code(err) != grpccodes.Aborted {
			if err != nil {
				return err
			}
			for {
				updated, getErr := client.Get(ctx, privatev1.ClustersGetRequest_builder{Id: id}.Build())
				if getErr != nil {
					return getErr
				}
				status := updated.GetObject().GetStatus()
				if status.GetState() == state && status.GetApiEndpoint() == apiEndpoint && status.GetIngressEndpoint() == ingressEndpoint {
					return nil
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-deadline.C:
					return fmt.Errorf("Cluster %q did not retain the requested readiness state", id)
				case <-ticker.C:
				}
			}
		}
		if attempt < maxAttempts-1 {
			time.Sleep(50 * time.Millisecond)
		}
	}
	return fmt.Errorf("update Cluster %q readiness after %d version conflicts: %w", id, maxAttempts, err)
}

func cleanupVirtualNetworkFixture(ctx context.Context, client privatev1.VirtualNetworksClient, id string) error {
	return cleanupNetworkFixtureResource(ctx,
		func(ctx context.Context) error {
			_, err := client.Delete(ctx, privatev1.VirtualNetworksDeleteRequest_builder{Id: id}.Build())
			return err
		},
		func(ctx context.Context) (*privatev1.Metadata, error) {
			response, err := client.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: id}.Build())
			if err != nil {
				return nil, err
			}
			return response.GetObject().GetMetadata(), nil
		},
		func(ctx context.Context) error {
			response, err := client.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: id}.Build())
			if err != nil {
				return err
			}
			object := response.GetObject()
			object.GetMetadata().SetFinalizers(nil)
			_, err = client.Update(ctx, privatev1.VirtualNetworksUpdateRequest_builder{
				Object: object, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}}, Lock: true,
			}.Build())
			return err
		})
}

func cleanupSubnetFixture(ctx context.Context, client privatev1.SubnetsClient, id string) error {
	return cleanupNetworkFixtureResource(ctx,
		func(ctx context.Context) error {
			_, err := client.Delete(ctx, privatev1.SubnetsDeleteRequest_builder{Id: id}.Build())
			return err
		},
		func(ctx context.Context) (*privatev1.Metadata, error) {
			response, err := client.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: id}.Build())
			if err != nil {
				return nil, err
			}
			return response.GetObject().GetMetadata(), nil
		},
		func(ctx context.Context) error {
			response, err := client.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: id}.Build())
			if err != nil {
				return err
			}
			object := response.GetObject()
			object.GetMetadata().SetFinalizers(nil)
			_, err = client.Update(ctx, privatev1.SubnetsUpdateRequest_builder{
				Object: object, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}}, Lock: true,
			}.Build())
			return err
		})
}

func waitForNetworkClassReady(
	ctx context.Context,
	client privatev1.NetworkClassesClient,
	id string,
) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		response, err := client.Get(ctx, privatev1.NetworkClassesGetRequest_builder{Id: id}.Build())
		g.Expect(err).NotTo(HaveOccurred())
		object := response.GetObject()
		g.Expect(object.GetStatus().GetState()).To(
			Equal(privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY),
			"NetworkClass %q did not become ready: %s",
			id,
			object.GetStatus().GetMessage(),
		)
	}, time.Minute, time.Second).Should(Succeed())

	Eventually(func(g Gomega) {
		response, err := client.Get(ctx, privatev1.NetworkClassesGetRequest_builder{Id: id}.Build())
		g.Expect(err).NotTo(HaveOccurred())
		object := response.GetObject()
		g.Expect(object.GetStatus().GetHub()).NotTo(
			BeEmpty(),
			"NetworkClass %q has no canonical Hub binding: %s",
			id,
			object.GetStatus().GetMessage(),
		)
	}, time.Minute, time.Second).Should(Succeed())
}

func waitForNetworkingCR(
	ctx context.Context,
	list func(context.Context) (int, error),
) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		count, err := list(ctx)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(count).To(Equal(1))
	}, time.Minute, time.Second).Should(Succeed())
}

func listNetworkingCR[T crclient.ObjectList](
	kubeClient crclient.Client,
	labelKey string,
	resourceID string,
	objects T,
	count func(T) int,
) func(context.Context) (int, error) {
	return func(ctx context.Context) (int, error) {
		err := kubeClient.List(ctx, objects, crclient.MatchingLabels{labelKey: resourceID})
		return count(objects), err
	}
}

func updateNetworkClassStatusWithRetry(
	ctx context.Context,
	client privatev1.NetworkClassesClient,
	id string,
	status *privatev1.NetworkClassStatus,
	updateMask *fieldmaskpb.FieldMask,
) error {
	const maxAttempts = 5
	var err error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		response, getErr := client.Get(ctx, privatev1.NetworkClassesGetRequest_builder{Id: id}.Build())
		if getErr != nil {
			return getErr
		}

		object := response.GetObject()
		object.SetStatus(status)
		_, err = client.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
			Object:     object,
			UpdateMask: updateMask,
			Lock:       true,
		}.Build())
		if err == nil || grpcstatus.Code(err) != grpccodes.Aborted {
			return err
		}

		if attempt < maxAttempts-1 {
			time.Sleep(50 * time.Millisecond)
		}
	}

	return fmt.Errorf("update NetworkClass %q status after %d version conflicts: %w", id, maxAttempts, err)
}
