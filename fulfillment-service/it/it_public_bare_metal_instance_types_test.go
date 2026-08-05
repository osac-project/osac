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

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/uuid"
)

var _ = Describe("Public bare metal instance types", func() {
	var (
		ctx           context.Context
		publicClient  publicv1.BareMetalInstanceTypesClient
		privateClient privatev1.BareMetalInstanceTypesClient
	)

	BeforeEach(func() {
		ctx = context.Background()
		publicClient = publicv1.NewBareMetalInstanceTypesClient(tool.ExternalView().UserConn())
		privateClient = privatev1.NewBareMetalInstanceTypesClient(tool.InternalView().AdminConn())
	})

	// createViaPrivate creates a bare metal instance type through the private API and returns its ID.
	// Registers a DeferCleanup to delete the instance type when the test completes.
	createViaPrivate := func(suffix string, cores int32, memoryGb int64) string {
		name := fmt.Sprintf("it-pub-bm-%s-%s", suffix, uuid.New())
		_, err := privateClient.Create(ctx, privatev1.BareMetalInstanceTypesCreateRequest_builder{
			Object: privatev1.BareMetalInstanceType_builder{
				Metadata: privatev1.Metadata_builder{
					Name: name,
				}.Build(),
				Spec: privatev1.BareMetalInstanceTypeSpec_builder{
					Hardware: privatev1.BareMetalHardwareSpec_builder{
						Cpu: privatev1.BareMetalCPUSpec_builder{
							Cores:          cores,
							Architecture:   "x86_64",
							ThreadsPerCore: 2,
						}.Build(),
						Memory: privatev1.BareMetalMemorySpec_builder{
							TotalGb: memoryGb,
						}.Build(),
					}.Build(),
					HostLabelSelector: privatev1.BareMetalLabelSelector_builder{
						MatchLabels: map[string]string{
							"hardware.example.com/cpu-cores": fmt.Sprintf("%d", cores),
							"hardware.example.com/memory-gb": fmt.Sprintf("%d", memoryGb),
						},
					}.Build(),
					Description: "Public IT test bare metal type.",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = privateClient.Delete(ctx, privatev1.BareMetalInstanceTypesDeleteRequest_builder{
				Id: name,
			}.Build())
		})
		return name
	}

	It("Can list bare metal instance types", func() {
		createViaPrivate("list", 8, 32)

		response, err := publicClient.List(ctx, publicv1.BareMetalInstanceTypesListRequest_builder{}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
		Expect(response.GetItems()).ToNot(BeEmpty())
	})

	It("Can get a bare metal instance type", func() {
		id := createViaPrivate("get", 16, 64)

		response, err := publicClient.Get(ctx, publicv1.BareMetalInstanceTypesGetRequest_builder{
			Id: id,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
		object := response.GetObject()
		Expect(object).ToNot(BeNil())
		Expect(object.GetId()).To(Equal(id))
		Expect(object.GetSpec().GetHardware().GetCpu().GetCores()).To(Equal(int32(16)))
		Expect(object.GetSpec().GetHardware().GetMemory().GetTotalGb()).To(Equal(int64(64)))
		Expect(object.GetSpec().GetHardware().GetCpu().GetArchitecture()).To(Equal("x86_64"))
	})

	It("Can filter bare metal instance types by metadata", func() {
		id1 := createViaPrivate("filter1", 8, 16)
		id2 := createViaPrivate("filter2", 16, 32)

		// Test filter by specific ID
		idFilter := fmt.Sprintf("this.id == '%s'", id1)
		response, err := publicClient.List(ctx, publicv1.BareMetalInstanceTypesListRequest_builder{
			Filter: &idFilter,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
		Expect(response.GetItems()).To(HaveLen(1))
		Expect(response.GetItems()[0].GetId()).To(Equal(id1))

		// Test filter by name pattern
		nameFilter := "this.metadata.name.contains('filter')"
		response, err = publicClient.List(ctx, publicv1.BareMetalInstanceTypesListRequest_builder{
			Filter: &nameFilter,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
		Expect(response.GetItems()).To(HaveLen(2))

		// Verify both objects are in the response
		ids := []string{response.GetItems()[0].GetId(), response.GetItems()[1].GetId()}
		Expect(ids).To(ContainElements(id1, id2))
	})

	It("Supports pagination", func() {
		createViaPrivate("page1", 4, 8)
		createViaPrivate("page2", 8, 16)
		createViaPrivate("page3", 12, 24)

		// Request first page with limit of 2
		pageFilter := "this.metadata.name.contains('page')"
		limit2 := int32(2)
		response, err := publicClient.List(ctx, publicv1.BareMetalInstanceTypesListRequest_builder{
			Filter: &pageFilter,
			Limit:  &limit2,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
		Expect(response.GetItems()).To(HaveLen(2))
		Expect(response.GetTotal()).To(BeNumerically(">=", 3))
		Expect(response.GetSize()).To(Equal(int32(2)))

		// Request second page
		offset2 := int32(2)
		response, err = publicClient.List(ctx, publicv1.BareMetalInstanceTypesListRequest_builder{
			Filter: &pageFilter,
			Offset: &offset2,
			Limit:  &limit2,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
		Expect(response.GetItems()).To(HaveLen(1))
		Expect(response.GetTotal()).To(BeNumerically(">=", 3))
	})
})
