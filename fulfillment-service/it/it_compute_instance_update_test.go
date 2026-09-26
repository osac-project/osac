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
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const computeInstanceTestFinalizer = "integration-test"

func setComputeInstanceTestFinalizer(ctx context.Context, client privatev1.ComputeInstancesClient, id string, add bool) {
	GinkgoHelper()
	timeout := 10 * time.Second
	if !add {
		timeout = 2 * time.Minute
	}
	Eventually(func(g Gomega) bool {
		response, err := client.Get(ctx, privatev1.ComputeInstancesGetRequest_builder{Id: id}.Build())
		if !add && status.Code(err) == codes.NotFound {
			return true
		}
		g.Expect(err).NotTo(HaveOccurred())
		object := response.GetObject()
		values := slices.DeleteFunc(object.GetMetadata().GetFinalizers(), func(value string) bool { return value == computeInstanceTestFinalizer })
		if add {
			values = append(values, computeInstanceTestFinalizer)
		}
		object.GetMetadata().SetFinalizers(values)
		_, err = client.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{Object: object, UpdateMask: catalogItemUpdateMask("metadata.finalizers"), Lock: true}.Build())
		g.Expect(err).NotTo(HaveOccurred())
		if add {
			return true
		}
		_, err = client.Signal(ctx, privatev1.ComputeInstancesSignalRequest_builder{Id: id}.Build())
		if status.Code(err) == codes.NotFound {
			return true
		}
		g.Expect(err).NotTo(HaveOccurred())
		return false
	}, timeout, 100*time.Millisecond).Should(BeTrue())
}

var _ = Describe("Compute instance updates", Label("compute-updates"), func() {
	DescribeTable("validates networking against persisted deletion state", func(ctx context.Context, deleteFirst, maskDeletionTimestamp bool) {
		network := createCatalogItemNetworkFixture(ctx, usersGroup, "")
		template := createCatalogItemComputeInstanceProvisioningTemplateFixture(ctx, nil)
		resource, err := createComputeInstanceFixture(ctx, tool.ExternalView().UserConn(), publicv1.ComputeInstanceSpec_builder{
			Template:           publicv1.ComputeInstanceTemplateReference_builder{Id: template}.Build(),
			NetworkAttachments: []*publicv1.ComputeNetworkAttachment{network.computeInstanceAttachment()},
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		client := privatev1.NewComputeInstancesClient(tool.InternalView().AdminConn())
		if deleteFirst {
			By("waiting for the reconciler to persist its finalizer")
			Eventually(func(g Gomega) {
				response, err := client.Get(ctx, privatev1.ComputeInstancesGetRequest_builder{Id: resource.GetId()}.Build())
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(response.GetObject().GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
			}, time.Minute, 100*time.Millisecond).Should(Succeed())

			setComputeInstanceTestFinalizer(ctx, client, resource.GetId(), true)
			DeferCleanup(func(ctx context.Context) { setComputeInstanceTestFinalizer(ctx, client, resource.GetId(), false) })
			beforeDeletion, err := client.Get(ctx, privatev1.ComputeInstancesGetRequest_builder{Id: resource.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(beforeDeletion.GetObject().GetMetadata().GetFinalizers()).To(ContainElement(computeInstanceTestFinalizer))

			By("starting deletion through the API while a test finalizer holds the resource")
			_, err = client.Delete(ctx, privatev1.ComputeInstancesDeleteRequest_builder{Id: resource.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
		}
		stored, err := client.Get(ctx, privatev1.ComputeInstancesGetRequest_builder{Id: resource.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(stored.GetObject().GetMetadata().HasDeletionTimestamp()).To(Equal(deleteFirst))
		if deleteFirst {
			Expect(stored.GetObject().GetMetadata().GetFinalizers()).To(ContainElement(computeInstanceTestFinalizer))
		}
		setCatalogItemSubnetFixtureState(ctx, network.subnetID, privatev1.SubnetState_SUBNET_STATE_PENDING)
		DeferCleanup(func(ctx context.Context) {
			setCatalogItemSubnetFixtureState(ctx, network.subnetID, privatev1.SubnetState_SUBNET_STATE_READY)
		})
		candidate := proto.Clone(stored.GetObject()).(*privatev1.ComputeInstance)
		candidate.GetSpec().GetNetworkAttachments()[0].SetSecurityGroups(nil)
		if !deleteFirst {
			By("supplying a deletion timestamp on a resource that is still live")
			candidate.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
		}
		mask := catalogItemUpdateMask("spec.network_attachments")
		if maskDeletionTimestamp {
			mask.Paths = append(mask.Paths, "metadata.deletion_timestamp")
		}
		_, err = client.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{Object: candidate, UpdateMask: mask}.Build())
		if deleteFirst {
			Expect(err).NotTo(HaveOccurred())
		} else {
			expectCatalogItemStatusCode(err, codes.FailedPrecondition)
		}
		persisted, err := client.Get(ctx, privatev1.ComputeInstancesGetRequest_builder{Id: resource.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(persisted.GetObject().GetMetadata().HasDeletionTimestamp()).To(Equal(deleteFirst))
		if deleteFirst {
			Expect(persisted.GetObject().GetSpec().GetNetworkAttachments()[0].GetSecurityGroups()).To(BeEmpty())
		} else {
			Expect(persisted.GetObject().GetSpec().GetNetworkAttachments()[0].GetSecurityGroups()).To(HaveLen(1))
		}
	},
		Entry("live resource: an unmasked deletion timestamp cannot bypass readiness", false, false),
		Entry("live resource: a masked deletion timestamp cannot bypass readiness", false, true),
		Entry("deleting resource: an ordinary update can release security groups while the subnet is pending", true, false),
	)
})
