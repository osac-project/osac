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
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

var _ = Describe("Batched attachment SecurityGroup resolution", func() {
	group := func(id, name, vn string) *privatev1.SecurityGroup {
		return privatev1.SecurityGroup_builder{
			Id:       id,
			Metadata: privatev1.Metadata_builder{Name: name, Tenant: testTenant}.Build(),
			Spec: privatev1.SecurityGroupSpec_builder{
				VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: vn}.Build(),
			}.Build(),
		}.Build()
	}
	request := func(keys ...string) networkAttachmentRequest {
		r := networkAttachmentRequest{tenant: testTenant, source: "spec.network_attachments[0]"}
		for _, key := range keys {
			r.securityGroups = append(r.securityGroups, privatev1.SecurityGroupLocalReference_builder{Id: key}.Build())
		}
		return r
	}

	It("looks up all keys once and keeps request order with exact IDs taking precedence", func() {
		first, second := group("sg-a", "alpha", "vn"), group("sg-b", "beta", "vn")
		nameCollision := group("sg-c", "sg-a", "vn")
		calls := 0
		lookup := func(_ context.Context, tenant string, ids, names []string) ([]*privatev1.SecurityGroup, error) {
			calls++
			Expect(tenant).To(Equal(testTenant))
			Expect(ids).To(Equal([]string{"beta", "sg-a"}))
			Expect(names).To(Equal(ids))
			return []*privatev1.SecurityGroup{nameCollision, first, second}, nil
		}
		resolved, err := resolveRequestedSecurityGroupsWithLookup(ctx, request("beta", "sg-a"), "vn", "subnet", lookup)
		Expect(err).NotTo(HaveOccurred())
		Expect(resolved).To(Equal([]*privatev1.SecurityGroup{second, first}))
		Expect(calls).To(Equal(1))
	})

	DescribeTable("preserves reference validation errors in request order",
		func(keys []string, rows []*privatev1.SecurityGroup, message string) {
			calls := 0
			lookup := func(context.Context, string, []string, []string) ([]*privatev1.SecurityGroup, error) {
				calls++
				return rows, nil
			}
			_, err := resolveRequestedSecurityGroupsWithLookup(ctx, request(keys...), "vn", "subnet", lookup)
			Expect(err).To(MatchError(message))
			Expect(calls).To(Equal(1))
		},
		Entry("ambiguous name", []string{"same"}, []*privatev1.SecurityGroup{group("a", "same", "vn"), group("b", "same", "vn")},
			"rpc error: code = InvalidArgument desc = name 'same' matches 2 records; use the ID"),
		Entry("missing reference before a later invalid reference", []string{"absent", "other"}, []*privatev1.SecurityGroup{group("other", "other", "other-vn")},
			"rpc error: code = InvalidArgument desc = spec.network_attachments[0].security_groups[0]: SecurityGroup 'absent' does not exist"),
		Entry("duplicate ID and name", []string{"a", "alpha"}, []*privatev1.SecurityGroup{group("a", "alpha", "vn")},
			"rpc error: code = InvalidArgument desc = spec.network_attachments[0].security_groups[1]: SecurityGroup 'a' is listed twice"),
		Entry("VirtualNetwork mismatch", []string{"a"}, []*privatev1.SecurityGroup{group("a", "alpha", "other-vn")},
			"rpc error: code = InvalidArgument desc = spec.network_attachments[0].security_groups[0]: SecurityGroup 'a' belongs to another VirtualNetwork than Subnet 'subnet'"),
	)

	It("deduplicates query keys while preserving duplicate-reference rejection", func() {
		lookup := func(_ context.Context, _ string, ids, names []string) ([]*privatev1.SecurityGroup, error) {
			Expect(ids).To(Equal([]string{"a"}))
			Expect(names).To(Equal(ids))
			return []*privatev1.SecurityGroup{group("a", "alpha", "vn")}, nil
		}
		_, err := resolveRequestedSecurityGroupsWithLookup(ctx, request("a", "a"), "vn", "subnet", lookup)
		Expect(err).To(MatchError(ContainSubstring("is listed twice")))
	})

	It("maps a lookup failure to the existing Internal error", func() {
		lookup := func(context.Context, string, []string, []string) ([]*privatev1.SecurityGroup, error) {
			return nil, errors.New("database unavailable")
		}
		_, err := resolveRequestedSecurityGroupsWithLookup(ctx, request("a"), "vn", "subnet", lookup)
		Expect(err).To(MatchError("rpc error: code = Internal desc = failed to resolve the network references"))
	})

	It("resolves mixed IDs and names through the real scoped DAO", func() {
		vn := seedVirtualNetwork(testTenant, "10.10.0.0/16", privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY, nil)
		first := seedSecurityGroup(vn, "alpha", privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY, nil)
		second := seedSecurityGroup(vn, "beta", privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY, nil)
		r := request(second.GetMetadata().GetName(), first.GetId())
		r.securityGroupsDao = fixtureDAO[*privatev1.SecurityGroup]()
		result := &networkAttachmentResolution{virtualNetwork: vn, subnet: privatev1.Subnet_builder{Id: "subnet"}.Build()}
		Expect(resolveAttachmentSecurityGroups(ctx, nil, r, result)).To(Succeed())
		Expect(result.securityGroupIDs()).To(Equal([]string{second.GetId(), first.GetId()}))
		references := result.securityGroupReferences()
		Expect(references).To(HaveLen(2))
		Expect(references[0].GetId()).To(Equal(second.GetId()))
		Expect(references[0].GetName()).To(Equal("beta"))
		Expect(references[1].GetId()).To(Equal(first.GetId()))
		Expect(references[1].GetName()).To(Equal("alpha"))
	})

	It("cannot resolve a group in another tenant even through a total-visibility DAO", func() {
		vn := seedVirtualNetwork(testTenant, "10.10.0.0/16", privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY, nil)
		sg := seedSecurityGroup(vn, "alpha", privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY, nil)
		r := request(sg.GetId())
		r.tenant = "another-tenant"
		r.securityGroupsDao = fixtureDAO[*privatev1.SecurityGroup]()
		result := &networkAttachmentResolution{virtualNetwork: vn, subnet: privatev1.Subnet_builder{Id: "subnet"}.Build()}
		Expect(resolveAttachmentSecurityGroups(ctx, nil, r, result)).To(MatchError(ContainSubstring("does not exist")))
	})

	It("cannot resolve a group in another project", func() {
		vn := seedVirtualNetwork(testTenant, "10.10.0.0/16", privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY, nil)
		sg := seedSecurityGroup(vn, "alpha", privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY, nil)
		r := request(sg.GetId())
		r.project = "another-project"
		r.securityGroupsDao = fixtureDAO[*privatev1.SecurityGroup]()
		result := &networkAttachmentResolution{virtualNetwork: vn, subnet: privatev1.Subnet_builder{Id: "subnet"}.Build()}
		Expect(resolveAttachmentSecurityGroups(ctx, nil, r, result)).To(MatchError(ContainSubstring("does not exist")))
	})

	It("resolves the tenant default group from another project", func() {
		defaultNetwork := map[string]string{defaultLabel: "true"}
		vn := seedVirtualNetwork(testTenant, "10.10.0.0/16",
			privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY, defaultNetwork)
		sg := seedSecurityGroup(vn, "default", privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY, defaultNetwork)
		r := request(sg.GetId())
		r.project = "another-project"
		r.securityGroupsDao = fixtureDAO[*privatev1.SecurityGroup]()
		result := &networkAttachmentResolution{virtualNetwork: vn, subnet: privatev1.Subnet_builder{Id: "subnet"}.Build()}
		Expect(resolveAttachmentSecurityGroups(ctx, nil, r, result)).To(Succeed())
		Expect(result.securityGroupIDs()).To(Equal([]string{sg.GetId()}))
	})

	It("prefers the tenant default's exact ID over a local group named with that ID", func() {
		seedProject("another-project")
		defaultNetwork := map[string]string{defaultLabel: "true"}
		vn := seedVirtualNetwork(testTenant, "10.11.0.0/16",
			privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY, defaultNetwork)
		tenantDefault := seedSecurityGroup(vn, "default", privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY,
			defaultNetwork)
		seedSecurityGroupInProject("another-project", vn, tenantDefault.GetId(),
			privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY, nil)
		for _, ref := range []*privatev1.SecurityGroupLocalReference{
			privatev1.SecurityGroupLocalReference_builder{Id: tenantDefault.GetId()}.Build(),
			privatev1.SecurityGroupLocalReference_builder{
				Id: tenantDefault.GetId(), Name: tenantDefault.GetMetadata().GetName(),
			}.Build(),
		} {
			r := request()
			r.securityGroups = []*privatev1.SecurityGroupLocalReference{ref}
			r.project = "another-project"
			r.securityGroupsDao = fixtureDAO[*privatev1.SecurityGroup]()
			result := &networkAttachmentResolution{virtualNetwork: vn, subnet: privatev1.Subnet_builder{Id: "subnet"}.Build()}
			Expect(resolveAttachmentSecurityGroups(ctx, nil, r, result)).To(Succeed())
			Expect(result.securityGroupIDs()).To(Equal([]string{tenantDefault.GetId()}))
		}
	})

	It("does not resolve a default group outside the tenant default project", func() {
		seedProject("third-project")
		vn := seedVirtualNetwork(testTenant, "10.24.0.0/16",
			privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY, nil)
		sg := seedSecurityGroupInProject("third-project", vn, "default",
			privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY, map[string]string{defaultLabel: "true"})
		r := request(sg.GetId())
		r.project = "another-project"
		r.securityGroupsDao = fixtureDAO[*privatev1.SecurityGroup]()
		result := &networkAttachmentResolution{virtualNetwork: vn, subnet: privatev1.Subnet_builder{Id: "subnet"}.Build()}
		Expect(resolveAttachmentSecurityGroups(ctx, nil, r, result)).To(MatchError(
			ContainSubstring("does not exist")))
	})

	It("resolves a name-only reference to the tenant default group from another project", func() {
		seedProject("another-project")
		defaultNetwork := map[string]string{defaultLabel: "true"}
		vn := seedVirtualNetwork(testTenant, "10.12.0.0/16",
			privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY, defaultNetwork)
		tenantDefault := seedSecurityGroup(vn, "default", privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY,
			defaultNetwork)
		r := request()
		r.securityGroups = []*privatev1.SecurityGroupLocalReference{
			privatev1.SecurityGroupLocalReference_builder{Name: "default"}.Build(),
		}
		r.project = "another-project"
		r.securityGroupsDao = fixtureDAO[*privatev1.SecurityGroup]()
		result := &networkAttachmentResolution{virtualNetwork: vn, subnet: privatev1.Subnet_builder{Id: "subnet"}.Build()}
		Expect(resolveAttachmentSecurityGroups(ctx, nil, r, result)).To(Succeed())
		Expect(result.securityGroupIDs()).To(Equal([]string{tenantDefault.GetId()}))
	})

	It("prefers a local group over the tenant default with the same name", func() {
		seedProject("another-project")
		defaultNetwork := map[string]string{defaultLabel: "true"}
		vn := seedVirtualNetwork(testTenant, "10.13.0.0/16",
			privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY, defaultNetwork)
		seedSecurityGroup(vn, "default", privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY, defaultNetwork)
		local := seedSecurityGroupInProject("another-project", vn, "default",
			privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY, nil)
		r := request()
		r.securityGroups = []*privatev1.SecurityGroupLocalReference{
			privatev1.SecurityGroupLocalReference_builder{Name: "default"}.Build(),
		}
		r.project = "another-project"
		r.securityGroupsDao = fixtureDAO[*privatev1.SecurityGroup]()
		result := &networkAttachmentResolution{virtualNetwork: vn, subnet: privatev1.Subnet_builder{Id: "subnet"}.Build()}
		Expect(resolveAttachmentSecurityGroups(ctx, nil, r, result)).To(Succeed())
		Expect(result.securityGroupIDs()).To(Equal([]string{local.GetId()}))
	})

	It("reports a deleting tenant default group referenced from another project as being deleted", func() {
		defaultNetwork := map[string]string{defaultLabel: "true"}
		vn := seedVirtualNetwork(testTenant, "10.14.0.0/16",
			privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY, defaultNetwork)
		subnet := seedSubnet(vn, "default-ipv4", "10.14.1.0/24", privatev1.SubnetState_SUBNET_STATE_READY,
			defaultNetwork)
		sg := seedSecurityGroup(vn, "default", privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY,
			defaultNetwork)
		groups := fixtureDAO[*privatev1.SecurityGroup]()
		sg.GetMetadata().SetFinalizers([]string{"test-finalizer"})
		_, err := groups.Update().SetObject(sg).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		_, err = groups.Delete().SetId(sg.GetId()).Do(ctx)
		Expect(err).NotTo(HaveOccurred())

		r := request(sg.GetId())
		r.project = "another-project"
		r.securityGroupsDao = groups
		result := &networkAttachmentResolution{virtualNetwork: vn, subnet: subnet}
		Expect(resolveAttachmentSecurityGroups(ctx, nil, r, result)).To(Succeed())
		err = checkAttachmentResolution(r, result)
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
		Expect(err).To(MatchError(ContainSubstring("is being deleted")))
	})

	It("fails closed when the DAO cap truncates matching candidates", func() {
		vn := seedVirtualNetwork(testTenant, "10.10.0.0/16", privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY, nil)
		first := seedSecurityGroup(vn, "alpha", privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY, nil)
		second := seedSecurityGroup(vn, "beta", privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY, nil)
		limited, err := dao.NewGenericDAO[*privatev1.SecurityGroup]().SetLogger(logger).SetTenancyLogic(tenancy).
			SetDefaultLimit(1).SetMaxLimit(1).Build()
		Expect(err).NotTo(HaveOccurred())
		r := request(first.GetId(), second.GetId())
		r.securityGroupsDao = limited
		result := &networkAttachmentResolution{virtualNetwork: vn, subnet: privatev1.Subnet_builder{Id: "subnet"}.Build()}
		Expect(resolveAttachmentSecurityGroups(ctx, nil, r, result)).To(MatchError(
			"rpc error: code = Internal desc = failed to resolve the network references: matching SecurityGroups exceed the DAO query limit"))
		Expect(result.securityGroups).To(BeEmpty())
	})

	It("locks resolved groups together and preserves attachment order", func() {
		vn := seedVirtualNetwork(testTenant, "10.10.0.0/16", privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY, nil)
		first := seedSecurityGroup(vn, "alpha", privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY, nil)
		second := seedSecurityGroup(vn, "beta", privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY, nil)
		result := &networkAttachmentResolution{securityGroups: []*privatev1.SecurityGroup{second, first}}
		guards := &networkingGuards{securityGroups: fixtureDAO[*privatev1.SecurityGroup]()}

		Expect(lockAttachmentSecurityGroups(ctx, guards, result)).To(Succeed())
		Expect(result.securityGroupIDs()).To(Equal([]string{second.GetId(), first.GetId()}))
		Expect(result.securityGroups[0].GetMetadata().GetName()).To(Equal("beta"))
		Expect(result.securityGroups[1].GetMetadata().GetName()).To(Equal("alpha"))
	})
})

var _ = Describe("Network attachment Subnet resolution", func() {
	It("reports a deleting tenant default referenced from another project", func() {
		virtualNetwork := seedVirtualNetwork(testTenant, "10.20.0.0/16",
			privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY, nil)
		subnet := seedSubnet(virtualNetwork, "default-ipv4", "10.20.1.0/24",
			privatev1.SubnetState_SUBNET_STATE_READY, map[string]string{defaultLabel: "true"})
		subnets := fixtureDAO[*privatev1.Subnet]()
		subnet.GetMetadata().SetFinalizers([]string{"test-finalizer"})
		_, err := subnets.Update().SetObject(subnet).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		_, err = subnets.Delete().SetId(subnet.GetId()).Do(ctx)
		Expect(err).NotTo(HaveOccurred())

		owner := privatev1.Metadata_builder{Tenant: testTenant, Project: "another-project"}.Build()
		reference := privatev1.SubnetLocalReference_builder{Id: subnet.GetId()}.Build()
		_, err = resolveNetworkingReference(ctx, subnets, owner, reference, "Subnet",
			grpccodes.InvalidArgument, true)
		Expect(grpcstatus.Convert(err).Message()).To(ContainSubstring("has been deleted"))
	})

	It("prefers the tenant default Subnet's exact ID over a local Subnet named with that ID", func() {
		seedProject("another-project")
		defaultNetwork := map[string]string{defaultLabel: "true"}
		vn := seedVirtualNetwork(testTenant, "10.31.0.0/16",
			privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY, defaultNetwork)
		tenantDefault := seedSubnet(vn, "default-ipv4", "10.31.1.0/24", privatev1.SubnetState_SUBNET_STATE_READY,
			defaultNetwork)
		seedSubnetInProject("another-project", vn, tenantDefault.GetId(), "10.31.2.0/24",
			privatev1.SubnetState_SUBNET_STATE_READY, nil)
		for _, ref := range []*privatev1.SubnetLocalReference{
			privatev1.SubnetLocalReference_builder{Id: tenantDefault.GetId()}.Build(),
			privatev1.SubnetLocalReference_builder{
				Id: tenantDefault.GetId(), Name: tenantDefault.GetMetadata().GetName(),
			}.Build(),
		} {
			request := networkAttachmentRequest{
				tenant:     testTenant,
				project:    "another-project",
				subnetsDao: fixtureDAO[*privatev1.Subnet](),
				subnet:     ref,
				source:     "spec.network_attachments[0]",
			}
			guards := &networkingGuards{
				subnets:         fixtureDAO[*privatev1.Subnet](),
				virtualNetworks: fixtureDAO[*privatev1.VirtualNetwork](),
			}
			result := &networkAttachmentResolution{}
			Expect(resolveAttachmentSubnet(ctx, guards, request, result)).To(Succeed())
			Expect(result.subnet.GetId()).To(Equal(tenantDefault.GetId()))
		}
	})

	DescribeTable("resolves a name-only Subnet reference from another project",
		func(withLocal bool) {
			seedProject("another-project")
			defaultNetwork := map[string]string{defaultLabel: "true"}
			vn := seedVirtualNetwork(testTenant, "10.32.0.0/16",
				privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY, defaultNetwork)
			expected := seedSubnet(vn, "default-ipv4", "10.32.1.0/24", privatev1.SubnetState_SUBNET_STATE_READY,
				defaultNetwork)
			if withLocal {
				expected = seedSubnetInProject("another-project", vn, "default-ipv4", "10.32.2.0/24",
					privatev1.SubnetState_SUBNET_STATE_READY, nil)
			}
			request := networkAttachmentRequest{
				tenant:     testTenant,
				project:    "another-project",
				subnetsDao: fixtureDAO[*privatev1.Subnet](),
				subnet:     privatev1.SubnetLocalReference_builder{Name: "default-ipv4"}.Build(),
				source:     "spec.network_attachments[0]",
			}
			guards := &networkingGuards{
				subnets:         fixtureDAO[*privatev1.Subnet](),
				virtualNetworks: fixtureDAO[*privatev1.VirtualNetwork](),
			}
			result := &networkAttachmentResolution{}
			Expect(resolveAttachmentSubnet(ctx, guards, request, result)).To(Succeed())
			Expect(result.subnet.GetId()).To(Equal(expected.GetId()))
		},
		Entry("to the tenant default", false),
		Entry("preferring a local Subnet with the same name", true),
	)

	It("rejects a reference whose ID and name identify different Subnets", func() {
		virtualNetwork := seedVirtualNetwork(testTenant, "10.20.0.0/16",
			privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY, nil)
		subnet := seedSubnet(virtualNetwork, "subnet-a", "10.20.1.0/24",
			privatev1.SubnetState_SUBNET_STATE_READY, nil)
		request := networkAttachmentRequest{
			tenant:     testTenant,
			subnetsDao: fixtureDAO[*privatev1.Subnet](),
			subnet: privatev1.SubnetLocalReference_builder{
				Id: subnet.GetId(), Name: "different-subnet",
			}.Build(),
			source: "spec.network_attachments[0]",
		}
		guards := &networkingGuards{
			subnets:         fixtureDAO[*privatev1.Subnet](),
			virtualNetworks: fixtureDAO[*privatev1.VirtualNetwork](),
		}

		err := resolveAttachmentSubnet(ctx, guards, request, &networkAttachmentResolution{})
		Expect(err).To(MatchError(ContainSubstring("id and name do not refer to the same resource")))
	})

	DescribeTable("resolves explicit Subnet references from another project",
		func(labels map[string]string, subnetProject, vnCIDR, subnetCIDR, expectedError string) {
			if subnetProject != "" {
				seedProject(subnetProject)
			}
			virtualNetwork := seedVirtualNetwork(testTenant, vnCIDR,
				privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY, labels)
			subnet := seedSubnetInProject(subnetProject, virtualNetwork, "subnet-a", subnetCIDR,
				privatev1.SubnetState_SUBNET_STATE_READY, labels)
			request := networkAttachmentRequest{
				tenant:     testTenant,
				project:    "another-project",
				subnetsDao: fixtureDAO[*privatev1.Subnet](),
				subnet:     privatev1.SubnetLocalReference_builder{Id: subnet.GetId()}.Build(),
				source:     "spec.network_attachments[0]",
			}
			guards := &networkingGuards{
				subnets:         fixtureDAO[*privatev1.Subnet](),
				virtualNetworks: fixtureDAO[*privatev1.VirtualNetwork](),
			}
			result := &networkAttachmentResolution{}

			err := resolveAttachmentSubnet(ctx, guards, request, result)
			if expectedError != "" {
				Expect(err).To(MatchError(ContainSubstring(expectedError)))
				return
			}
			Expect(err).NotTo(HaveOccurred())
			Expect(result.subnet.GetId()).To(Equal(subnet.GetId()))
			Expect(result.usedDefaults).To(BeFalse())
		},
		Entry("tenant default", map[string]string{defaultLabel: "true"}, "", "10.21.0.0/16", "10.21.1.0/24", ""),
		Entry("not a tenant default", nil, "", "10.22.0.0/16", "10.22.1.0/24", "does not exist"),
		Entry("default outside the default project", map[string]string{defaultLabel: "true"}, "third-project",
			"10.23.0.0/16", "10.23.1.0/24", "does not exist"),
	)
})

var _ = Describe("Network attachment default networking readiness gate", func() {
	var (
		defaults defaultNetworkingFixture
		guards   *networkingGuards
		settings NetworkingSettings
	)

	BeforeEach(func() {
		defaults = seedReadyDefaultNetworking(testTenant, 0)
		tenants := fixtureDAO[*privatev1.Tenant]()
		response, err := tenants.Get().SetId(testTenant).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		tenant := response.GetObject()
		tenant.GetStatus().SetConditions([]*privatev1.TenantCondition{
			privatev1.TenantCondition_builder{
				Type:   privatev1.TenantConditionType_TENANT_CONDITION_TYPE_DEFAULT_NETWORKING_READY,
				Status: privatev1.ConditionStatus_CONDITION_STATUS_FALSE,
			}.Build(),
		})
		_, err = tenants.Update().SetObject(tenant).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		guards, err = newNetworkingGuards(logger)
		Expect(err).NotTo(HaveOccurred())
		settings = NetworkingSettings{ProvisioningEnabled: true}
	})

	It("does not gate a supplied reference to the tenant defaults", func() {
		result, err := resolveNetworkAttachment(ctx, logger, guards, settings, networkAttachmentRequest{
			tenant:            testTenant,
			project:           "another-project",
			subnetsDao:        fixtureDAO[*privatev1.Subnet](),
			securityGroupsDao: fixtureDAO[*privatev1.SecurityGroup](),
			subnet:            privatev1.SubnetLocalReference_builder{Id: defaults.subnetID}.Build(),
			securityGroups: []*privatev1.SecurityGroupLocalReference{
				privatev1.SecurityGroupLocalReference_builder{Id: defaults.securityGroupID}.Build(),
			},
			source: "spec.network_attachments[0]",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.usedDefaults).To(BeFalse())
		Expect(result.subnet.GetId()).To(Equal(defaults.subnetID))
		Expect(result.securityGroupIDs()).To(Equal([]string{defaults.securityGroupID}))
	})

	It("gates an omitted attachment filled from the tenant defaults", func() {
		_, err := resolveNetworkAttachment(ctx, logger, guards, settings, networkAttachmentRequest{
			tenant:            testTenant,
			project:           "another-project",
			subnetsDao:        fixtureDAO[*privatev1.Subnet](),
			securityGroupsDao: fixtureDAO[*privatev1.SecurityGroup](),
			source:            "spec.network_attachments[0]",
		})
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
		Expect(grpcstatus.Convert(err).Message()).To(Equal("tenant default networking is not ready"))
	})
})
