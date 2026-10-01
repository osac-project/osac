/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package fabricdomain

import (
	"context"
	"io"
	"log/slog"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/config"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	"github.com/osac-project/osac/fulfillment-service/internal/testing"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type virtualNetworksServer struct {
	publicv1.UnimplementedVirtualNetworksServer
	request *publicv1.VirtualNetworksListRequest
	items   []*publicv1.VirtualNetwork
}

func (s *virtualNetworksServer) List(_ context.Context, request *publicv1.VirtualNetworksListRequest) (*publicv1.VirtualNetworksListResponse, error) {
	s.request = request
	return publicv1.VirtualNetworksListResponse_builder{Items: s.items}.Build(), nil
}

type fabricDomainsServer struct {
	publicv1.UnimplementedFabricDomainsServer
	request *publicv1.FabricDomainsCreateRequest
}

func (s *fabricDomainsServer) Create(_ context.Context, request *publicv1.FabricDomainsCreateRequest) (*publicv1.FabricDomainsCreateResponse, error) {
	s.request = request
	return publicv1.FabricDomainsCreateResponse_builder{Object: request.GetObject()}.Build(), nil
}

var _ = Describe("Create fabric domain", func() {
	DescribeTable("parses fabric domain types",
		func(input string, expected publicv1.FabricDomainType) {
			actual, err := parseType(input)
			Expect(err).NotTo(HaveOccurred())
			Expect(actual).To(Equal(expected))
		},
		Entry("ethernet east-west", "ethernet_ew", publicv1.FabricDomainType_FABRIC_DOMAIN_TYPE_ETHERNET_EW),
		Entry("case and whitespace variations", " Ethernet_EW ", publicv1.FabricDomainType_FABRIC_DOMAIN_TYPE_ETHERNET_EW),
	)

	It("rejects unsupported types", func() {
		_, err := parseType("infiniband_ew")
		Expect(err).To(MatchError("unsupported fabric domain type \"infiniband_ew\"; only 'ethernet_ew' is supported"))
	})

	DescribeTable("uses the effective tenant for creation and virtual network lookup",
		func(savedTenant, effectiveTenant, reference, expectedFilter string, found bool) {
			server := testing.NewServer()
			DeferCleanup(server.Stop)
			virtualNetworks := &virtualNetworksServer{}
			if found {
				virtualNetworks.items = []*publicv1.VirtualNetwork{
					publicv1.VirtualNetwork_builder{Id: "resolved-vn-id"}.Build(),
				}
			}
			fabricDomains := &fabricDomainsServer{}
			publicv1.RegisterVirtualNetworksServer(server.Registrar(), virtualNetworks)
			publicv1.RegisterFabricDomainsServer(server.Registrar(), fabricDomains)
			server.Start()

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			settings, err := config.NewSettings().SetLogger(logger).SetDir(GinkgoT().TempDir()).Build()
			Expect(err).NotTo(HaveOccurred())
			settings.SetAddress(server.Address())
			settings.SetPlaintext(true)
			settings.SetTenant(savedTenant)
			console, err := terminal.NewConsole().SetLogger(logger).SetStdout(io.Discard).SetStderr(io.Discard).Build()
			Expect(err).NotTo(HaveOccurred())
			ctx := logging.LoggerIntoContext(context.Background(), logger)
			ctx = config.SettingsIntoContext(ctx, settings)
			ctx = terminal.ConsoleIntoContext(ctx, console)
			// The root pre-run resolves --tenant (including an explicit empty value)
			// before invoking this command; the saved setting is not authoritative here.
			ctx = config.TenantIntoContext(ctx, effectiveTenant)
			cmd := Cmd()
			cmd.SetContext(ctx)
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs([]string{"--name", "domain", "--servers", "server-01", "--virtual-network", reference})
			err = cmd.Execute()

			Expect(virtualNetworks.request).NotTo(BeNil())
			Expect(virtualNetworks.request.GetFilter()).To(Equal(expectedFilter))
			Expect(virtualNetworks.request.GetLimit()).To(Equal(int32(2)))
			if !found {
				Expect(err).To(MatchError(ContainSubstring("not found")))
				Expect(fabricDomains.request).To(BeNil())
				return
			}
			Expect(err).NotTo(HaveOccurred())
			Expect(fabricDomains.request).NotTo(BeNil())
			object := fabricDomains.request.GetObject()
			Expect(object.GetMetadata().GetTenant()).To(Equal(effectiveTenant))
			Expect(object.GetSpec().GetVirtualNetwork()).To(Equal("resolved-vn-id"))
		},
		Entry("flag tenant overrides saved tenant for a name",
			"saved-tenant", "selected-tenant", "tenant-net",
			`(this.id == "tenant-net" || this.metadata.name == "tenant-net") && this.metadata.tenant == "selected-tenant"`, true),
		Entry("flag tenant scopes the ID branch as well as the name branch",
			"saved-tenant", "selected-tenant", "vn-id",
			`(this.id == "vn-id" || this.metadata.name == "vn-id") && this.metadata.tenant == "selected-tenant"`, true),
		Entry("saved tenant is used when resolved by the root command",
			"saved-tenant", "saved-tenant", "tenant-net",
			`(this.id == "tenant-net" || this.metadata.name == "tenant-net") && this.metadata.tenant == "saved-tenant"`, true),
		Entry("explicitly cleared tenant does not fall back to saved settings",
			"saved-tenant", "", "tenant-net",
			`this.id == "tenant-net" || this.metadata.name == "tenant-net"`, true),
		Entry("out-of-tenant ID is not resolved and creation is not attempted",
			"saved-tenant", "selected-tenant", "foreign-vn-id",
			`(this.id == "foreign-vn-id" || this.metadata.name == "foreign-vn-id") && this.metadata.tenant == "selected-tenant"`, false),
	)
})
