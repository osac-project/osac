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
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
)

var cvVersionCounter atomic.Int64

func nextCVVersion() string {
	return fmt.Sprintf("100.0.%d", cvVersionCounter.Add(1))
}

var _ = Describe("Private cluster versions", func() {
	var (
		ctx    context.Context
		client privatev1.ClusterVersionsClient
	)

	BeforeEach(func() {
		ctx = context.Background()
		client = privatev1.NewClusterVersionsClient(tool.InternalView().AdminConn())
	})

	createCV := func(version string) *privatev1.ClusterVersion {
		response, err := client.Create(ctx, privatev1.ClusterVersionsCreateRequest_builder{
			Object: privatev1.ClusterVersion_builder{
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("test-cv-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: privatev1.ClusterVersionSpec_builder{
					Version: version,
					Image:   fmt.Sprintf("quay.io/openshift-release-dev/ocp-release:%s", version),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		object := response.GetObject()
		DeferCleanup(func() {
			_, _ = client.Delete(ctx, privatev1.ClusterVersionsDeleteRequest_builder{
				Id: object.GetId(),
			}.Build())
		})
		return object
	}

	It("CRUD lifecycle", func() {
		version := nextCVVersion()
		object := createCV(version)

		// Verify create response:
		Expect(object.GetId()).ToNot(BeEmpty())
		Expect(object.GetSpec().GetVersion()).To(Equal(version))
		Expect(object.GetSpec().GetImage()).To(ContainSubstring(version))
		Expect(object.GetMetadata().GetName()).ToNot(BeEmpty())
		Expect(object.GetMetadata().HasCreationTimestamp()).To(BeTrue())
		Expect(object.GetMetadata().HasDeletionTimestamp()).To(BeFalse())

		// Get by ID:
		getResponse, err := client.Get(ctx, privatev1.ClusterVersionsGetRequest_builder{
			Id: object.GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetSpec().GetVersion()).To(Equal(version))

		// List with filter:
		name := object.GetMetadata().GetName()
		listResponse, err := client.List(ctx, privatev1.ClusterVersionsListRequest_builder{
			Filter: new(fmt.Sprintf("this.metadata.name == %q", name)),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse.GetItems()).To(HaveLen(1))
		Expect(listResponse.GetItems()[0].GetId()).To(Equal(object.GetId()))

		// Update (add label):
		updateResponse, err := client.Update(ctx, privatev1.ClusterVersionsUpdateRequest_builder{
			Object: privatev1.ClusterVersion_builder{
				Id: object.GetId(),
				Metadata: privatev1.Metadata_builder{
					Labels: map[string]string{"env": "test"},
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.labels"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(updateResponse.GetObject().GetMetadata().GetLabels()).To(
			HaveKeyWithValue("env", "test"))

		// Delete:
		_, err = client.Delete(ctx, privatev1.ClusterVersionsDeleteRequest_builder{
			Id: object.GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Get returns not found:
		_, err = client.Get(ctx, privatev1.ClusterVersionsGetRequest_builder{
			Id: object.GetId(),
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.NotFound))
	})

	It("Rejects update of immutable field spec.version", func() {
		version := nextCVVersion()
		object := createCV(version)

		_, err := client.Update(ctx, privatev1.ClusterVersionsUpdateRequest_builder{
			Object: privatev1.ClusterVersion_builder{
				Id: object.GetId(),
				Spec: privatev1.ClusterVersionSpec_builder{
					Version: "9.9.9",
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.version"}},
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
	})

})
