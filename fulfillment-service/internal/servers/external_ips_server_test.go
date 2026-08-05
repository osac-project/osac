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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/database/dao"
)

var _ = Describe("Public external IPs server", func() {
	Describe("Builder", func() {
		It("Builds successfully with required parameters", func() {
			s, err := NewExternalIPsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(s).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			s, err := NewExternalIPsServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(s).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			s, err := NewExternalIPsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(s).To(BeNil())
		})

		It("Fails if attribution logic is not set", func() {
			s, err := NewExternalIPsServer().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("attribution logic is mandatory"))
			Expect(s).To(BeNil())
		})
	})

	Describe("CRUD operations", func() {
		var (
			publicServer      *ExternalIPsServer
			externalIPPoolDao *dao.GenericDAO[*privatev1.ExternalIPPool]
		)

		BeforeEach(func() {
			var err error

			publicServer, err = NewExternalIPsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			externalIPPoolDao, err = dao.NewGenericDAO[*privatev1.ExternalIPPool]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Create a READY pool for CRUD tests:
			_, err = externalIPPoolDao.Create().SetObject(
				privatev1.ExternalIPPool_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "test-pool",
						Tenant: auth.SharedTenant,
					}.Build(),
					Spec: privatev1.ExternalIPPoolSpec_builder{
						Cidrs: []string{"10.0.0.0/24"},
					}.Build(),
					Status: privatev1.ExternalIPPoolStatus_builder{
						State:     privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY,
						Total:     100,
						Allocated: 0,
						Available: 100,
					}.Build(),
				}.Build(),
			).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
		})

		// getPoolID returns the ID of the test pool created in BeforeEach.
		getPoolID := func() string {
			externalIPPoolDao, err := dao.NewGenericDAO[*privatev1.ExternalIPPool]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			resp, err := externalIPPoolDao.List().Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetItems()).ToNot(BeEmpty())
			return resp.GetItems()[0].GetId()
		}

		It("creates and retrieves an ExternalIP", func() {
			poolID := getPoolID()
			createResp, err := publicServer.Create(ctx, publicv1.ExternalIPsCreateRequest_builder{
				Object: publicv1.ExternalIP_builder{
					Metadata: publicv1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
					Spec:     publicv1.ExternalIPSpec_builder{Pool: poolID}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(createResp.GetObject().GetId()).ToNot(BeEmpty())

			getResp, err := publicServer.Get(ctx, publicv1.ExternalIPsGetRequest_builder{
				Id: createResp.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResp.GetObject().GetId()).To(Equal(createResp.GetObject().GetId()))
		})

		It("lists ExternalIPs", func() {
			poolID := getPoolID()
			for range 3 {
				_, err := publicServer.Create(ctx, publicv1.ExternalIPsCreateRequest_builder{
					Object: publicv1.ExternalIP_builder{
						Metadata: publicv1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
						Spec:     publicv1.ExternalIPSpec_builder{Pool: poolID}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			listResp, err := publicServer.List(ctx, publicv1.ExternalIPsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(listResp.GetItems()).To(HaveLen(3))
		})

		It("deletes an ExternalIP", func() {
			poolID := getPoolID()
			createResp, err := publicServer.Create(ctx, publicv1.ExternalIPsCreateRequest_builder{
				Object: publicv1.ExternalIP_builder{
					Metadata: publicv1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
					Spec:     publicv1.ExternalIPSpec_builder{Pool: poolID}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Transition to ALLOCATED via private server (public API doesn't expose state changes):
			privateServer, err := NewPrivateExternalIPsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			getResp, err := privateServer.Get(ctx, privatev1.ExternalIPsGetRequest_builder{
				Id: createResp.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := getResp.GetObject()
			object.GetStatus().SetState(privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED)
			_, err = privateServer.Update(ctx, privatev1.ExternalIPsUpdateRequest_builder{
				Object: object,
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = publicServer.Delete(ctx, publicv1.ExternalIPsDeleteRequest_builder{
				Id: createResp.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})
	})
})
