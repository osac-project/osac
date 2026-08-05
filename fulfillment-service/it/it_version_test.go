/*
Copyright (c) 2025 Red Hat Inc.

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
	"math"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/uuid"
)

var _ = Describe("Version", func() {
	var (
		ctx             context.Context
		clustersClient  publicv1.ClustersClient
		hostTypesClient privatev1.HostTypesClient
		templatesClient privatev1.ClusterTemplatesClient
		hostTypeId      string
		templateId      string
	)

	BeforeEach(func() {
		ctx = context.Background()
		clustersClient = publicv1.NewClustersClient(tool.ExternalView().UserConn())
		hostTypesClient = privatev1.NewHostTypesClient(tool.InternalView().AdminConn())
		templatesClient = privatev1.NewClusterTemplatesClient(tool.InternalView().AdminConn())

		hostTypeId = fmt.Sprintf("my-host-type-%s", uuid.New())
		_, err := hostTypesClient.Create(ctx, privatev1.HostTypesCreateRequest_builder{
			Object: privatev1.HostType_builder{
				Id: hostTypeId,
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, err := hostTypesClient.Delete(ctx, privatev1.HostTypesDeleteRequest_builder{
				Id: hostTypeId,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		templateId = fmt.Sprintf("my-template-%s", uuid.New())
		_, err = templatesClient.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
			Object: privatev1.ClusterTemplate_builder{
				Id:          templateId,
				Title:       "My template",
				Description: "My template.",
				NodeSets: map[string]*privatev1.ClusterTemplateNodeSet{
					"my-node-set": privatev1.ClusterTemplateNodeSet_builder{
						HostType: hostTypeId,
						Size:     3,
					}.Build(),
				},
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, err := templatesClient.Delete(ctx, privatev1.ClusterTemplatesDeleteRequest_builder{
				Id: templateId,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})
	})

	createCluster := func() *publicv1.Cluster {
		response, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Spec: publicv1.ClusterSpec_builder{
					Template: templateId,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		object := response.GetObject()
		DeferCleanup(func() {
			_, err := clustersClient.Delete(ctx, publicv1.ClustersDeleteRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})
		return object
	}

	It("Is populated on create", func() {
		object := createCluster()
		version := object.GetMetadata().GetVersion()
		Expect(version).To(BeZero())
	})

	It("Is populated when retrieved after create", func() {
		object := createCluster()
		id := object.GetId()
		getResponse, err := clustersClient.Get(ctx, publicv1.ClustersGetRequest_builder{
			Id: id,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		object = getResponse.GetObject()
		Expect(object.GetMetadata().GetVersion()).To(BeNumerically(">=", int32(0)))
	})

	It("Is populated when listed after create", func() {
		object := createCluster()
		id := object.GetId()
		listResponse, err := clustersClient.List(ctx, publicv1.ClustersListRequest_builder{
			Filter: new(fmt.Sprintf("this.id == %q", id)),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		items := listResponse.GetItems()
		Expect(items).To(HaveLen(1))
		item := items[0]
		Expect(item.GetMetadata().GetVersion()).To(BeNumerically(">=", int32(0)))
	})

	It("Increments on update", func() {
		// Create the object and get the version:
		object := createCluster()
		version := object.GetMetadata().GetVersion()

		// Update the object and verify that the version has been incremeted. Note that it may have been
		// incremented multiple times, by the controller that is running in the background, so we only can
		// assert that it is greater than the initial version.
		updateResponse, err := clustersClient.Update(ctx, publicv1.ClustersUpdateRequest_builder{
			Object: publicv1.Cluster_builder{
				Id: object.GetId(),
				Metadata: publicv1.Metadata_builder{
					Labels: map[string]string{
						"step": "one",
					},
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: templateId,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		object = updateResponse.GetObject()
		Expect(object.GetMetadata().GetVersion()).To(BeNumerically(">", version))
	})

	It("Matches after get", func() {
		// Create the object:
		object := createCluster()

		// Perform an update and record the version from the response:
		updateResponse, err := clustersClient.Update(ctx, publicv1.ClustersUpdateRequest_builder{
			Object: publicv1.Cluster_builder{
				Id: object.GetId(),
				Metadata: publicv1.Metadata_builder{
					Labels: map[string]string{
						"step": "one",
					},
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: templateId,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		object = updateResponse.GetObject()
		version := object.GetMetadata().GetVersion()

		// Get and verify that the version from the get response is greater than or equal to the version from
		// the update response:
		getResponse, err := clustersClient.Get(ctx, publicv1.ClustersGetRequest_builder{
			Id: object.GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		object = getResponse.GetObject()
		Expect(object.GetMetadata().GetVersion()).To(BeNumerically(">=", version))
	})

	Describe("Lock", func() {
		It("Succeeds when version matches", func() {
			// Create the object and get the version:
			object := createCluster()
			id := object.GetId()
			version := object.GetMetadata().GetVersion()

			// Try repeatedly till the update succeeds. It may initially fail because the controller may
			// update the object before us. Re-read the current version before each attempt to avoid
			// sending a stale version that will never match.
			Eventually(
				func(g Gomega) {
					getResponse, err := clustersClient.Get(ctx, publicv1.ClustersGetRequest_builder{
						Id: id,
					}.Build())
					g.Expect(err).ToNot(HaveOccurred())
					version = getResponse.GetObject().GetMetadata().GetVersion()

					updateResponse, err := clustersClient.Update(ctx, publicv1.ClustersUpdateRequest_builder{
						Object: publicv1.Cluster_builder{
							Id: id,
							Metadata: publicv1.Metadata_builder{
								Version: version,
								Annotations: map[string]string{
									"date": time.Now().Format(time.RFC3339Nano),
								},
							}.Build(),
						}.Build(),
						Lock: true,
					}.Build())
					g.Expect(err).ToNot(HaveOccurred())
					object = updateResponse.GetObject()
				},
				10*time.Second,
				100*time.Millisecond,
			).Should(Succeed())
		})

		It("Fails when version does not match", func() {
			// Create the object and get the version:
			object := createCluster()
			id := object.GetId()

			// Send an update with lock enabled but a version that will never match:
			_, err := clustersClient.Update(ctx, publicv1.ClustersUpdateRequest_builder{
				Object: publicv1.Cluster_builder{
					Id: id,
					Metadata: publicv1.Metadata_builder{
						Version: math.MaxInt32,
						Labels: map[string]string{
							"should": "fail",
						},
					}.Build(),
					Spec: publicv1.ClusterSpec_builder{
						Template: templateId,
					}.Build(),
				}.Build(),
				Lock: true,
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.Aborted))

			// Verify that our changes were not applied:
			getResponse, err := clustersClient.Get(ctx, publicv1.ClustersGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object = getResponse.GetObject()
			Expect(object.GetMetadata().GetLabels()).ToNot(HaveKeyWithValue("should", "fail"))
		})

		It("Is not enabled by default", func() {
			// Create the object and get the version:
			object := createCluster()
			id := object.GetId()

			// Send an update with a wrong version in the metadata but without enabling lock. The update
			// should succeed because optimistic locking is not enabled.
			updateResponse, err := clustersClient.Update(ctx, publicv1.ClustersUpdateRequest_builder{
				Object: publicv1.Cluster_builder{
					Id: id,
					Metadata: publicv1.Metadata_builder{
						Version: math.MaxInt32,
						Labels: map[string]string{
							"should": "succeed",
						},
					}.Build(),
					Spec: publicv1.ClusterSpec_builder{
						Template: templateId,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object = updateResponse.GetObject()
			Expect(object.GetMetadata().GetLabels()).To(HaveKeyWithValue("should", "succeed"))
		})
	})
})
