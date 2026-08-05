/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
)

var _ = Describe("Public external IP pools server", func() {
	var privatePool *PrivateExternalIPPoolsServer

	BeforeEach(func() {
		var err error

		privatePool, err = NewPrivateExternalIPPoolsServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("Builder", func() {
		It("Builds successfully with required parameters", func() {
			s, err := NewExternalIPPoolsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(s).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			s, err := NewExternalIPPoolsServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(s).To(BeNil())
		})

		It("Fails if attribution logic is not set", func() {
			s, err := NewExternalIPPoolsServer().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("attribution logic is mandatory"))
			Expect(s).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			s, err := NewExternalIPPoolsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(s).To(BeNil())
		})
	})

	Describe("List", func() {
		It("Returns only READY pools with available capacity", func() {
			for i := range 3 {
				_, err := privatePool.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
					Object: privatev1.ExternalIPPool_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("pool-%d", i),
						}.Build(),
						Spec: privatev1.ExternalIPPoolSpec_builder{
							Cidrs:    []string{fmt.Sprintf("10.%d.0.0/24", i)},
							IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// Set pool-1 to READY with capacity:
			listResp, err := privatePool.List(ctx, privatev1.ExternalIPPoolsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			for _, p := range listResp.GetItems() {
				if p.GetMetadata().GetName() == "pool-1" {
					_, err = privatePool.Update(ctx, privatev1.ExternalIPPoolsUpdateRequest_builder{
						Object: privatev1.ExternalIPPool_builder{
							Id: p.GetId(),
							Status: privatev1.ExternalIPPoolStatus_builder{
								State:     privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY,
								Total:     10,
								Available: 5,
								Allocated: 5,
							}.Build(),
						}.Build(),
						UpdateMask: &fieldmaskpb.FieldMask{
							Paths: []string{"status"},
						},
					}.Build())
					Expect(err).ToNot(HaveOccurred())
				}
			}

			publicServer, err := NewExternalIPPoolsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			publicResp, err := publicServer.List(ctx, publicv1.ExternalIPPoolsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(publicResp.GetItems()).To(HaveLen(1))
			Expect(publicResp.GetTotal()).To(BeNumerically("==", 1))
		})

		It("Excludes READY pools with zero available capacity", func() {
			createResp, err := privatePool.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
				Object: privatev1.ExternalIPPool_builder{
					Spec: privatev1.ExternalIPPoolSpec_builder{
						Cidrs:    []string{"10.0.0.0/24"},
						IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = privatePool.Update(ctx, privatev1.ExternalIPPoolsUpdateRequest_builder{
				Object: privatev1.ExternalIPPool_builder{
					Id: createResp.GetObject().GetId(),
					Status: privatev1.ExternalIPPoolStatus_builder{
						State:     privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY,
						Total:     10,
						Available: 0,
						Allocated: 10,
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"status"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			publicServer, err := NewExternalIPPoolsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			publicResp, err := publicServer.List(ctx, publicv1.ExternalIPPoolsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(publicResp.GetItems()).To(BeEmpty())
		})
	})

	Describe("Get", func() {
		It("Returns pool when READY with available capacity", func() {
			createResp, err := privatePool.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
				Object: privatev1.ExternalIPPool_builder{
					Spec: privatev1.ExternalIPPoolSpec_builder{
						Cidrs:    []string{"10.0.0.0/24"},
						IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			poolID := createResp.GetObject().GetId()

			_, err = privatePool.Update(ctx, privatev1.ExternalIPPoolsUpdateRequest_builder{
				Object: privatev1.ExternalIPPool_builder{
					Id: poolID,
					Status: privatev1.ExternalIPPoolStatus_builder{
						State:     privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY,
						Total:     10,
						Available: 5,
						Allocated: 5,
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"status"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			publicServer, err := NewExternalIPPoolsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			getResp, err := publicServer.Get(ctx, publicv1.ExternalIPPoolsGetRequest_builder{
				Id: poolID,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResp.GetObject()).ToNot(BeNil())
		})

		It("Returns NotFound when pool is not READY", func() {
			createResp, err := privatePool.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
				Object: privatev1.ExternalIPPool_builder{
					Spec: privatev1.ExternalIPPoolSpec_builder{
						Cidrs:    []string{"10.0.0.0/24"},
						IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			publicServer, err := NewExternalIPPoolsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			_, err = publicServer.Get(ctx, publicv1.ExternalIPPoolsGetRequest_builder{
				Id: createResp.GetObject().GetId(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.NotFound))
		})
	})
})
