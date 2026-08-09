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

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
)

var _ = Describe("ExternalIP pool selector", func() {
	var poolDao *dao.GenericDAO[*privatev1.ExternalIPPool]

	createPool := func(name string, ipFamily privatev1.IPFamily, state privatev1.ExternalIPPoolState, available int64) *privatev1.ExternalIPPool {
		pool := privatev1.ExternalIPPool_builder{
			Metadata: privatev1.Metadata_builder{
				Name:   name,
				Tenant: "system",
			}.Build(),
			Spec: privatev1.ExternalIPPoolSpec_builder{
				IpFamily: ipFamily,
			}.Build(),
			Status: privatev1.ExternalIPPoolStatus_builder{
				State:     state,
				Total:     available,
				Available: available,
				Allocated: 0,
			}.Build(),
		}.Build()
		resp, err := poolDao.Create().SetObject(pool).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		return resp.GetObject()
	}

	BeforeEach(func() {
		var err error
		poolDao, err = dao.NewGenericDAO[*privatev1.ExternalIPPool]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	It("selects READY pool with highest available capacity", func() {
		createPool("small", privatev1.IPFamily_IP_FAMILY_IPV4, privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY, 5)
		createPool("large", privatev1.IPFamily_IP_FAMILY_IPV4, privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY, 100)

		pool, err := SelectExternalIPPool(ctx, poolDao, privatev1.IPFamily_IP_FAMILY_UNSPECIFIED)
		Expect(err).ToNot(HaveOccurred())
		Expect(pool.GetMetadata().GetName()).To(Equal("large"))
	})

	It("skips pools not in READY state", func() {
		createPool("pending", privatev1.IPFamily_IP_FAMILY_IPV4, privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_PENDING, 100)
		createPool("ready", privatev1.IPFamily_IP_FAMILY_IPV4, privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY, 10)

		pool, err := SelectExternalIPPool(ctx, poolDao, privatev1.IPFamily_IP_FAMILY_UNSPECIFIED)
		Expect(err).ToNot(HaveOccurred())
		Expect(pool.GetMetadata().GetName()).To(Equal("ready"))
	})

	It("skips pools with zero available capacity", func() {
		createPool("empty", privatev1.IPFamily_IP_FAMILY_IPV4, privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY, 0)
		createPool("available", privatev1.IPFamily_IP_FAMILY_IPV4, privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY, 5)

		pool, err := SelectExternalIPPool(ctx, poolDao, privatev1.IPFamily_IP_FAMILY_UNSPECIFIED)
		Expect(err).ToNot(HaveOccurred())
		Expect(pool.GetMetadata().GetName()).To(Equal("available"))
	})

	It("filters by IPv4 family", func() {
		createPool("ipv6-pool", privatev1.IPFamily_IP_FAMILY_IPV6, privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY, 100)
		createPool("ipv4-pool", privatev1.IPFamily_IP_FAMILY_IPV4, privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY, 10)

		pool, err := SelectExternalIPPool(ctx, poolDao, privatev1.IPFamily_IP_FAMILY_IPV4)
		Expect(err).ToNot(HaveOccurred())
		Expect(pool.GetMetadata().GetName()).To(Equal("ipv4-pool"))
	})

	It("filters by IPv6 family", func() {
		createPool("ipv4-pool", privatev1.IPFamily_IP_FAMILY_IPV4, privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY, 100)
		createPool("ipv6-pool", privatev1.IPFamily_IP_FAMILY_IPV6, privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY, 10)

		pool, err := SelectExternalIPPool(ctx, poolDao, privatev1.IPFamily_IP_FAMILY_IPV6)
		Expect(err).ToNot(HaveOccurred())
		Expect(pool.GetMetadata().GetName()).To(Equal("ipv6-pool"))
	})

	It("accepts any family when IP_FAMILY_UNSPECIFIED", func() {
		createPool("ipv6-pool", privatev1.IPFamily_IP_FAMILY_IPV6, privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY, 50)
		createPool("ipv4-pool", privatev1.IPFamily_IP_FAMILY_IPV4, privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY, 100)

		pool, err := SelectExternalIPPool(ctx, poolDao, privatev1.IPFamily_IP_FAMILY_UNSPECIFIED)
		Expect(err).ToNot(HaveOccurred())
		Expect(pool.GetMetadata().GetName()).To(Equal("ipv4-pool"))
	})

	It("breaks ties deterministically by pool ID", func() {
		poolA := createPool("pool-a", privatev1.IPFamily_IP_FAMILY_IPV4, privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY, 50)
		poolB := createPool("pool-b", privatev1.IPFamily_IP_FAMILY_IPV4, privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY, 50)

		pool, err := SelectExternalIPPool(ctx, poolDao, privatev1.IPFamily_IP_FAMILY_UNSPECIFIED)
		Expect(err).ToNot(HaveOccurred())

		if poolA.GetId() < poolB.GetId() {
			Expect(pool.GetId()).To(Equal(poolA.GetId()))
		} else {
			Expect(pool.GetId()).To(Equal(poolB.GetId()))
		}
	})

	It("returns error when no pools exist", func() {
		_, err := SelectExternalIPPool(ctx, poolDao, privatev1.IPFamily_IP_FAMILY_UNSPECIFIED)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no READY ExternalIP pool"))
	})

	It("returns error with IP family detail when no matching pools exist", func() {
		createPool("ipv4-pool", privatev1.IPFamily_IP_FAMILY_IPV4, privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY, 10)

		_, err := SelectExternalIPPool(ctx, poolDao, privatev1.IPFamily_IP_FAMILY_IPV6)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("IP_FAMILY_IPV6"))
	})
})
