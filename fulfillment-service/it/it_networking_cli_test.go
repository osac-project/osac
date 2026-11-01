/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package it

// Component-integration specs of the IPv4-only networking CLI commands (create and describe of virtualnetwork,
// subnet and securitygroup, and the name resolution of create computeinstance --network-attachment) against the
// deployed fulfillment service and PostgreSQL. They run in the service-only phase of the SUITE=fulfillment installer
// target, where the fixtures report READY through the private API as the operator's feedback controllers would.

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Networking CLI", Label("cli", "networking-cli"), func() {
	var (
		c    ndClients
		home string
	)

	BeforeEach(func(ctx context.Context) {
		c = newNDClients()
		ndReadyNetworkClass(ctx, c)

		var err error
		home, err = tool.NewCLIHomeDir()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(home)).To(Succeed()) })
		_, stderr, code := tool.LoginCLI(ctx, home, userUsername, usersPassword)
		Expect(code).To(Equal(0), "login failed: %s", stderr)
	})

	// cli runs the CLI as the regular test user and fails the spec when the exit code isn't the expected one.
	cli := func(ctx context.Context, wantCode int, args ...string) (stdout, stderr string) {
		GinkgoHelper()
		stdout, stderr, code := tool.RunCLI(ctx, home, args...)
		fmt.Fprintf(GinkgoWriter, "osac %s: code=%d\nstdout:\n%s\nstderr:\n%s\n", strings.Join(args, " "), code,
			stdout, stderr)
		if wantCode == 0 {
			Expect(code).To(Equal(0), "osac %s failed: %s", strings.Join(args, " "), stderr)
		} else {
			Expect(code).ToNot(Equal(0), "osac %s succeeded, stdout: %s", strings.Join(args, " "), stdout)
		}
		return stdout, stderr
	}

	// storedVirtualNetwork returns the VirtualNetwork with the name, which the CLI created, from the private API.
	storedVirtualNetwork := func(ctx context.Context, name string) *privatev1.VirtualNetwork {
		GinkgoHelper()
		filter := fmt.Sprintf("this.metadata.name == %q", name)
		list, err := c.virtualNetworks.List(ctx, privatev1.VirtualNetworksListRequest_builder{Filter: &filter}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(list.GetItems()).To(HaveLen(1), "VirtualNetwork %s was not persisted", name)
		object := list.GetItems()[0]
		id := object.GetId()
		DeferCleanup(func(cleanupCtx context.Context) {
			Expect(cleanupVirtualNetworkFixture(cleanupCtx, c.virtualNetworks, id)).To(Succeed())
		})
		return object
	}
	storedSubnet := func(ctx context.Context, name string) *privatev1.Subnet {
		GinkgoHelper()
		filter := fmt.Sprintf("this.metadata.name == %q", name)
		list, err := c.subnets.List(ctx, privatev1.SubnetsListRequest_builder{Filter: &filter}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(list.GetItems()).To(HaveLen(1), "Subnet %s was not persisted", name)
		object := list.GetItems()[0]
		id := object.GetId()
		DeferCleanup(func(cleanupCtx context.Context) {
			Expect(cleanupSubnetFixture(cleanupCtx, c.subnets, id)).To(Succeed())
		})
		return object
	}
	storedSecurityGroup := func(ctx context.Context, name string) *privatev1.SecurityGroup {
		GinkgoHelper()
		filter := fmt.Sprintf("this.metadata.name == %q", name)
		list, err := c.securityGroups.List(ctx, privatev1.SecurityGroupsListRequest_builder{Filter: &filter}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(list.GetItems()).To(HaveLen(1), "SecurityGroup %s was not persisted", name)
		object := list.GetItems()[0]
		id := object.GetId()
		DeferCleanup(func(cleanupCtx context.Context) {
			Expect(cleanupSecurityGroupFixture(cleanupCtx, c.securityGroups, id)).To(Succeed())
		})
		return object
	}

	It("creates and describes a VirtualNetwork, Subnet and SecurityGroup with the IPv4-only flags",
		func(ctx context.Context) {
			ndSetSecurityGroupICMP(ctx, c, new(true))
			cidr := ndVirtualNetworkCIDR()
			base := netip.MustParsePrefix(cidr).Addr()
			subnetCIDR := netip.PrefixFrom(base, 24).String()

			By("creating a VirtualNetwork with --ipv4-cidr")
			vnName := ndUniqueName("cli-vn")
			stdout, _ := cli(ctx, 0, "create", "virtualnetwork", "--name", vnName, "--ipv4-cidr", cidr)
			Expect(stdout).To(ContainSubstring(fmt.Sprintf("Created virtual network '%s'", vnName)))
			vn := storedVirtualNetwork(ctx, vnName)
			Expect(vn.GetSpec().GetIpv4Cidr()).To(Equal(cidr), "stored: %s", ndText(vn))

			By("describing the VirtualNetwork by name: IPv4 CIDR, no IPv6 columns")
			stdout, _ = cli(ctx, 0, "describe", "virtualnetwork", vnName)
			Expect(stdout).To(MatchRegexp(`ID:\s+`+vn.GetId()), "output: %s", stdout)
			Expect(stdout).To(MatchRegexp(`Name:\s+`+vnName), "output: %s", stdout)
			Expect(stdout).To(MatchRegexp(`IPv4 CIDR:\s+`+cidr), "output: %s", stdout)
			Expect(stdout).To(MatchRegexp(`State:\s+PENDING`), "output: %s", stdout)
			Expect(strings.ToLower(stdout)).ToNot(ContainSubstring("ipv6"))

			By("reporting the VirtualNetwork READY so the Subnet and SecurityGroup gates open")
			ndWaitVirtualNetworkPlaced(ctx, c, vn.GetId())
			ndSetVirtualNetworkState(ctx, c, vn.GetId(), privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY)

			By("creating a Subnet in the VirtualNetwork, resolved by name")
			subnetName := ndUniqueName("cli-subnet")
			stdout, _ = cli(ctx, 0, "create", "subnet", "--name", subnetName, "--virtual-network", vnName,
				"--ipv4-cidr", subnetCIDR)
			Expect(stdout).To(ContainSubstring(fmt.Sprintf("Created subnet '%s'", subnetName)))
			subnet := storedSubnet(ctx, subnetName)
			Expect(subnet.GetSpec().GetIpv4Cidr()).To(Equal(subnetCIDR), "stored: %s", ndText(subnet))
			Expect(subnet.GetSpec().GetVirtualNetwork().GetId()).To(Equal(vn.GetId()))

			By("describing the Subnet by ID: IPv4 CIDR, no IPv6 columns")
			stdout, _ = cli(ctx, 0, "describe", "subnet", subnet.GetId())
			Expect(stdout).To(MatchRegexp(`Name:\s+`+subnetName), "output: %s", stdout)
			Expect(stdout).To(MatchRegexp(`IPv4 CIDR:\s+`+subnetCIDR), "output: %s", stdout)
			Expect(stdout).To(ContainSubstring("Virtual Network:"))
			Expect(strings.ToLower(stdout)).ToNot(ContainSubstring("ipv6"))

			By("creating a SecurityGroup with ingress and egress rules")
			groupName := ndUniqueName("cli-sg")
			stdout, _ = cli(ctx, 0, "create", "securitygroup", "--name", groupName, "--virtual-network", vn.GetId(),
				"--ingress", "protocol=tcp,port-from=80,port-to=443,ipv4-cidr=10.0.0.0/8",
				"--ingress", "protocol=icmp,ipv4-cidr=192.168.0.0/16",
				"--egress", "protocol=all,ipv4-cidr=0.0.0.0/0")
			Expect(stdout).To(ContainSubstring(fmt.Sprintf("Created security group '%s'", groupName)))
			group := storedSecurityGroup(ctx, groupName)
			Expect(group.GetSpec().GetIngress()).To(HaveLen(2), "stored: %s", ndText(group))
			Expect(group.GetSpec().GetEgress()).To(HaveLen(1), "stored: %s", ndText(group))
			tcp := group.GetSpec().GetIngress()[0]
			Expect(tcp.GetProtocol()).To(Equal(privatev1.Protocol_PROTOCOL_TCP))
			Expect(tcp.GetPortFrom()).To(BeEquivalentTo(80))
			Expect(tcp.GetPortTo()).To(BeEquivalentTo(443))
			Expect(tcp.GetIpv4Cidr()).To(Equal("10.0.0.0/8"))
			Expect(group.GetSpec().GetIngress()[1].GetProtocol()).To(Equal(privatev1.Protocol_PROTOCOL_ICMP))
			Expect(group.GetSpec().GetIngress()[1].GetIpv4Cidr()).To(Equal("192.168.0.0/16"))
			Expect(group.GetSpec().GetEgress()[0].GetProtocol()).To(Equal(privatev1.Protocol_PROTOCOL_ALL))
			Expect(group.GetSpec().GetEgress()[0].GetIpv4Cidr()).To(Equal("0.0.0.0/0"))

			By("describing the SecurityGroup: IPv4 rule columns, no IPv6 columns")
			stdout, _ = cli(ctx, 0, "describe", "securitygroup", groupName)
			Expect(stdout).To(MatchRegexp(`Name:\s+`+groupName), "output: %s", stdout)
			Expect(stdout).To(ContainSubstring("Ingress Rules:"))
			Expect(stdout).To(ContainSubstring("Egress Rules:"))
			Expect(stdout).To(MatchRegexp(`PROTOCOL\s+PORT-FROM\s+PORT-TO\s+IPV4-CIDR`), "output: %s", stdout)
			Expect(stdout).To(MatchRegexp(`tcp\s+80\s+443\s+10\.0\.0\.0/8`), "output: %s", stdout)
			Expect(stdout).To(MatchRegexp(`icmp\s+-\s+-\s+192\.168\.0\.0/16`), "output: %s", stdout)
			Expect(stdout).To(MatchRegexp(`all\s+-\s+-\s+0\.0\.0\.0/0`), "output: %s", stdout)
			Expect(strings.ToLower(stdout)).ToNot(ContainSubstring("ipv6"))
		})

	It("rejects an icmp rule from the API and hides icmp in the help", func(ctx context.Context) {
		ndSetSecurityGroupICMP(ctx, c, new(false))
		vn := ndReadyVirtualNetwork(ctx, c, usersGroup, ndVirtualNetworkCIDR())

		By("creating a SecurityGroup with an icmp rule: the CLI still sends it and the API rejects it")
		groupName := ndUniqueName("cli-sg-icmp")
		_, stderr := cli(ctx, 1, "create", "securitygroup", "--name", groupName, "--virtual-network", vn.GetId(),
			"--ingress", "protocol=icmp,ipv4-cidr=10.0.0.0/8")
		Expect(stderr).To(ContainSubstring("use protocol all"))
		expectNoSecurityGroupNamed(ctx, c.securityGroups, groupName)

		By("checking that the help no longer offers icmp")
		stdout, stderr := cli(ctx, 0, "create", "securitygroup", "--help")
		Expect(stdout + stderr).ToNot(ContainSubstring("protocol=icmp"))
	})

	It("rejects --ipv6-cidr, IPv6 and non-canonical CIDRs and persists nothing", func(ctx context.Context) {
		By("rejecting --ipv6-cidr on create virtualnetwork as an unknown flag")
		name := ndUniqueName("cli-vn-v6")
		_, stderr := cli(ctx, 1, "create", "virtualnetwork", "--name", name, "--ipv6-cidr", "fd00:10::/48")
		Expect(stderr).To(ContainSubstring("unknown flag: --ipv6-cidr"))
		expectNoVirtualNetworkNamed(ctx, c.virtualNetworks, name)

		By("rejecting --ipv6-cidr on create subnet as an unknown flag")
		subnetName := ndUniqueName("cli-subnet-v6")
		_, stderr = cli(ctx, 1, "create", "subnet", "--name", subnetName, "--virtual-network", "any",
			"--ipv6-cidr", "fd00:10::/64")
		Expect(stderr).To(ContainSubstring("unknown flag: --ipv6-cidr"))
		expectNoSubnetNamed(ctx, c.subnets, subnetName)

		By("rejecting an IPv6 value of --ipv4-cidr before any request")
		name = ndUniqueName("cli-vn-v6value")
		_, stderr = cli(ctx, 1, "create", "virtualnetwork", "--name", name, "--ipv4-cidr", "fd00:10::/48")
		Expect(stderr).To(ContainSubstring("invalid IPv4 CIDR"))
		expectNoVirtualNetworkNamed(ctx, c.virtualNetworks, name)

		By("rejecting a missing --ipv4-cidr")
		name = ndUniqueName("cli-vn-nocidr")
		_, stderr = cli(ctx, 1, "create", "virtualnetwork", "--name", name)
		Expect(stderr).To(ContainSubstring("--ipv4-cidr is required"))
		expectNoVirtualNetworkNamed(ctx, c.virtualNetworks, name)

		By("rejecting a non-canonical CIDR in the server")
		name = ndUniqueName("cli-vn-noncanon")
		_, stderr = cli(ctx, 1, "create", "virtualnetwork", "--name", name, "--ipv4-cidr", "10.150.0.1/16")
		Expect(stderr).To(ContainSubstring("canonical"))
		expectNoVirtualNetworkNamed(ctx, c.virtualNetworks, name)

		By("rejecting an IPv6 key and an IPv6 CIDR in a SecurityGroup rule")
		vn := ndReadyVirtualNetwork(ctx, c, usersGroup, ndVirtualNetworkCIDR())
		groupName := ndUniqueName("cli-sg-v6")
		_, stderr = cli(ctx, 1, "create", "securitygroup", "--name", groupName, "--virtual-network", vn.GetId(),
			"--ingress", "protocol=tcp,ipv6-cidr=::/0")
		Expect(stderr).To(ContainSubstring(`unknown key "ipv6-cidr"`))
		_, stderr = cli(ctx, 1, "create", "securitygroup", "--name", groupName, "--virtual-network", vn.GetId(),
			"--egress", "protocol=tcp,ipv4-cidr=fd00:10::/64")
		Expect(stderr).To(ContainSubstring("address is not IPv4"))
		expectNoSecurityGroupNamed(ctx, c.securityGroups, groupName)
	})

	It("resolves --network-attachment names to IDs and never resolves another tenant's Subnet",
		func(ctx context.Context) {
			By("creating a READY VirtualNetwork, Subnet and SecurityGroup in the user's tenant")
			vn := ndReadyVirtualNetwork(ctx, c, usersGroup, ndVirtualNetworkCIDR())
			subnetCIDR := netip.PrefixFrom(netip.MustParsePrefix(vn.GetSpec().GetIpv4Cidr()).Addr(), 24).String()
			subnet := ndReadySubnet(ctx, c, usersGroup, vn.GetId(), subnetCIDR)
			group := ndReadySecurityGroup(ctx, c, usersGroup, vn.GetId(), nil, nil)

			By("creating a Subnet with the same kind of name in another tenant")
			_, otherTenant := ndCreateTenant(ctx, c)
			otherVN := ndReadyVirtualNetwork(ctx, c, otherTenant, ndVirtualNetworkCIDR())
			otherSubnet := ndReadySubnet(ctx, c, otherTenant, otherVN.GetId(),
				netip.PrefixFrom(netip.MustParsePrefix(otherVN.GetSpec().GetIpv4Cidr()).Addr(), 24).String())

			template := createCatalogItemComputeInstanceProvisioningTemplateFixture(ctx, nil)

			By("refusing the other tenant's Subnet name before creating anything")
			name := catalogItemFixtureName()
			_, stderr := cli(ctx, 1, "create", "computeinstance", "--template", template, "--name", name,
				"--network-attachment", "subnet="+otherSubnet.GetMetadata().GetName())
			Expect(stderr).To(ContainSubstring(fmt.Sprintf("subnet %q not found", otherSubnet.GetMetadata().GetName())))
			client := publicv1.NewComputeInstancesClient(tool.ExternalView().UserConn())
			filter := fmt.Sprintf("this.metadata.name == %q", name)
			listed, err := client.List(ctx, publicv1.ComputeInstancesListRequest_builder{Filter: &filter}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(listed.GetItems()).To(BeEmpty(), "a ComputeInstance was persisted for an unresolved Subnet")

			By("refusing an unknown SecurityGroup name")
			_, stderr = cli(ctx, 1, "create", "computeinstance", "--template", template, "--name", name,
				"--network-attachment",
				"subnet="+subnet.GetMetadata().GetName()+",security-groups="+ndUniqueName("no-such-sg"))
			Expect(stderr).To(ContainSubstring("not found"))
			listed, err = client.List(ctx, publicv1.ComputeInstancesListRequest_builder{Filter: &filter}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(listed.GetItems()).To(BeEmpty())

			By("creating a ComputeInstance with the Subnet and SecurityGroup names")
			cli(ctx, 0, "create", "computeinstance", "--template", template, "--name", name,
				"--network-attachment",
				"subnet="+subnet.GetMetadata().GetName()+",security-groups="+group.GetMetadata().GetName())
			listed, err = client.List(ctx, publicv1.ComputeInstancesListRequest_builder{Filter: &filter}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(listed.GetItems()).To(HaveLen(1))
			created := listed.GetItems()[0]
			deferComputeInstanceDeletion(created.GetId(), tool.ExternalView().UserConn())
			attachments := created.GetSpec().GetNetworkAttachments()
			Expect(attachments).To(HaveLen(1), "stored: %s", ndText(created))
			Expect(attachments[0].GetSubnet().GetId()).To(Equal(subnet.GetId()), "names resolve to IDs")
			Expect(attachments[0].GetSecurityGroups()).To(HaveLen(1))
			Expect(attachments[0].GetSecurityGroups()[0].GetId()).To(Equal(group.GetId()))
		})
})
