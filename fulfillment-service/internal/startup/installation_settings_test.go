/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package startup

import (
	"context"
	"net/netip"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
)

var _ = Describe("Installation settings", func() {
	var (
		ctx  context.Context
		pool *pgxpool.Pool
	)

	BeforeEach(func() {
		ctx = context.Background()
		db, err := server.NewInstance().Build()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(db.Close)
		pool, err = db.Pool(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(pool.Close)
	})

	settings := func(enabled bool, release string, services ...string) InstallationSettings {
		return InstallationSettings{ProvisioningEnabled: enabled, Release: release, EnabledServices: services}
	}

	exec := func(sql string, args ...any) {
		_, err := pool.Exec(ctx, sql, args...)
		Expect(err).ToNot(HaveOccurred())
	}

	addVirtualNetwork := func(cidr string) {
		exec(`insert into virtual_networks (id, name, tenant, data) values ('vn-1', 'vn-1', 'shared', $1::jsonb)`,
			`{"spec":{"ipv4_cidr":"`+cidr+`"}}`)
	}

	It("stores the settings at the first start", func() {
		Expect(CheckInstallationSettings(ctx, logger, pool, settings(true, "0.2.0", "vmaas"))).To(Succeed())
		var enabled bool
		var services []string
		var release string
		err := pool.QueryRow(ctx, `select provisioning_enabled, enabled_services, release from installation_settings`).
			Scan(&enabled, &services, &release)
		Expect(err).ToNot(HaveOccurred())
		Expect(enabled).To(BeTrue())
		Expect(services).To(Equal([]string{"vmaas"}))
		Expect(release).To(Equal("0.2.0"))
	})

	It("starts again with the same settings", func() {
		Expect(CheckInstallationSettings(ctx, logger, pool, settings(false, "0.2.0", "vmaas"))).To(Succeed())
		Expect(CheckInstallationSettings(ctx, logger, pool, settings(false, "0.2.0", "vmaas"))).To(Succeed())
	})

	It("refuses a changed provisioningEnabled with the exact message", func() {
		Expect(CheckInstallationSettings(ctx, logger, pool, settings(true, "0.2.0"))).To(Succeed())
		err := CheckInstallationSettings(ctx, logger, pool, settings(false, "0.2.0"))
		Expect(err).To(MatchError(
			"global.networking.provisioningEnabled cannot change after installation; reinstall to change it"))
	})

	DescribeTable("newly enabled BMaaS or CaaS",
		func(withNetwork bool, services []string, expectErr bool) {
			Expect(CheckInstallationSettings(ctx, logger, pool, settings(true, "0.2.0", "vmaas"))).To(Succeed())
			if withNetwork {
				addVirtualNetwork("10.0.0.0/16")
			}
			err := CheckInstallationSettings(ctx, logger, pool, settings(true, "0.2.0", services...))
			if expectErr {
				Expect(err).To(MatchError("Enabling BMaaS or CaaS after networking resources exist is not " +
					"supported in this release; delete every VirtualNetwork or reinstall"))
			} else {
				Expect(err).ToNot(HaveOccurred())
			}
		},
		Entry("BMaaS with a VirtualNetwork", true, []string{"vmaas", "bmaas"}, true),
		Entry("CaaS with a VirtualNetwork", true, []string{"vmaas", "bmaas", "caas"}, true),
		Entry("BMaaS without a VirtualNetwork", false, []string{"vmaas", "bmaas"}, false),
		Entry("disabling a service with a VirtualNetwork", true, []string{}, false),
	)

	It("refuses re-enabling BMaaS after it was disabled while networking resources existed", func() {
		Expect(CheckInstallationSettings(ctx, logger, pool, settings(true, "0.2.0", "bmaas"))).To(Succeed())
		addVirtualNetwork("10.0.0.0/16")
		Expect(CheckInstallationSettings(ctx, logger, pool, settings(true, "0.2.0"))).To(Succeed())
		Expect(CheckInstallationSettings(ctx, logger, pool, settings(true, "0.2.0", "bmaas"))).ToNot(Succeed())
	})

	DescribeTable("release changes",
		func(stored, current string, withNetwork, expectErr bool) {
			Expect(CheckInstallationSettings(ctx, logger, pool, settings(true, stored))).To(Succeed())
			if withNetwork {
				addVirtualNetwork("10.0.0.0/16")
			}
			err := CheckInstallationSettings(ctx, logger, pool, settings(true, current))
			if expectErr {
				Expect(err).To(MatchError("in-place upgrade or downgrade between minor releases is not " +
					"supported; delete all workloads and networking resources or reinstall"))
			} else {
				Expect(err).ToNot(HaveOccurred())
			}
		},
		Entry("minor upgrade with a VirtualNetwork", "0.2.3", "0.3.0", true, true),
		Entry("minor downgrade with a VirtualNetwork", "0.3.0", "0.2.9", true, true),
		Entry("patch upgrade with a VirtualNetwork", "0.2.0", "0.2.1", true, false),
		Entry("minor upgrade without records", "0.2.0", "0.3.0", false, false),
		Entry("development build", "0.2.0", "4f2c1d9", true, false),
	)

	It("stores the release of a patch upgrade", func() {
		Expect(CheckInstallationSettings(ctx, logger, pool, settings(true, "0.2.0"))).To(Succeed())
		Expect(CheckInstallationSettings(ctx, logger, pool, settings(true, "0.2.5"))).To(Succeed())
		var release string
		Expect(pool.QueryRow(ctx, `select release from installation_settings`).Scan(&release)).To(Succeed())
		Expect(release).To(Equal("0.2.5"))
	})

	DescribeTable("excluded ranges",
		func(setup func(), excluded, message string) {
			setup()
			current := settings(true, "0.2.0")
			current.ExcludedIPv4CIDRs = []netip.Prefix{netip.MustParsePrefix(excluded)}
			err := CheckInstallationSettings(ctx, logger, pool, current)
			if message == "" {
				Expect(err).ToNot(HaveOccurred())
			} else {
				Expect(err).To(MatchError(message))
			}
		},
		Entry("no overlap", func() { addVirtualNetwork("10.0.0.0/16") }, "192.168.0.0/24", ""),
		Entry("the NetworkClass default", func() {
			exec(`insert into network_classes (id, name, tenant, data) values ('nc', 'nc', 'shared',
				'{"spec":{"defaults":{"virtual_network_ipv4_cidr":"10.200.0.0/16"}}}')`)
		}, "10.200.1.0/24",
			"excludedIpv4Cidrs range 10.200.1.0/24 overlaps the NetworkClass default virtual_network_ipv4_cidr "+
				"10.200.0.0/16"),
		Entry("a VirtualNetwork", func() { addVirtualNetwork("10.0.0.0/16") }, "10.0.5.0/24",
			"excludedIpv4Cidrs range 10.0.5.0/24 overlaps VirtualNetwork vn-1"),
		Entry("an ExternalIPPool", func() {
			exec(`insert into external_ip_pools (id, name, tenant, data) values ('pool-1', 'pool-1', 'shared',
				'{"spec":{"cidrs":["192.0.2.0/24"]}}')`)
		}, "192.0.2.128/25", "excludedIpv4Cidrs range 192.0.2.128/25 overlaps ExternalIPPool pool-1"),
		Entry("a Cluster default pod and service network, which are not compared", func() {
			exec(`insert into tenants (id, name, tenant, creator, data) values ('t', 't', 't', 'system', '{}')`)
			exec(`insert into clusters (id, name, tenant, data) values ('cluster-1', 'cluster-1', 't', '{}')`)
		}, "10.0.0.0/8", ""),
	)

	It("serializes concurrent first starts", func() {
		var wg sync.WaitGroup
		errs := make([]error, 4)
		for i := range errs {
			wg.Add(1)
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				errs[i] = CheckInstallationSettings(ctx, logger, pool, settings(i%2 == 0, "0.2.0"))
			}()
		}
		wg.Wait()
		failures := 0
		for _, err := range errs {
			if err != nil {
				failures++
			}
		}
		Expect(failures).To(Equal(2))
		var count int
		Expect(pool.QueryRow(ctx, `select count(*) from installation_settings`).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(1))
	})
})
