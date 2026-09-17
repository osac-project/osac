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

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/osac-project/osac/fulfillment-service/internal/testing"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Networking API contract", func() {
	resources := []struct {
		name   string
		plural string
	}{
		{name: "VirtualNetworks", plural: "virtual_networks"},
		{name: "Subnets", plural: "subnets"},
		{name: "SecurityGroups", plural: "security_groups"},
		{name: "ExternalIPs", plural: "external_ips"},
		{name: "ExternalIPAttachments", plural: "external_ip_attachments"},
		{name: "NATGateways", plural: "nat_gateways"},
	}

	It("exposes public CRUD and private update operations for every networking resource", func() {
		for _, resource := range resources {
			publicService := findService("osac.public.v1." + resource.name)
			privateService := findService("osac.private.v1." + resource.name)

			for _, method := range []protoreflect.Name{"List", "Get", "Create", "Delete"} {
				Expect(publicService.Methods().ByName(method)).ToNot(BeNil(), "public %s.%s", resource.name, method)
				Expect(privateService.Methods().ByName(method)).ToNot(BeNil(), "private %s.%s", resource.name, method)
			}
			Expect(publicService.Methods().ByName("Update")).To(BeNil(), "public %s.Update", resource.name)
			Expect(privateService.Methods().ByName("Update")).ToNot(BeNil(), "private %s.Update", resource.name)
		}
	})

	It("keeps public CRUD HTTP routes and removes public PATCH routes", func() {
		for _, resource := range resources {
			publicService := findService("osac.public.v1." + resource.name)
			privateService := findService("osac.private.v1." + resource.name)

			Expect(httpRule(publicService.Methods().ByName("List")).GetGet()).To(Equal("/api/fulfillment/v1/" + resource.plural))
			Expect(httpRule(publicService.Methods().ByName("Get")).GetGet()).To(Equal("/api/fulfillment/v1/" + resource.plural + "/{id}"))
			Expect(httpRule(publicService.Methods().ByName("Create")).GetPost()).To(Equal("/api/fulfillment/v1/" + resource.plural))
			Expect(httpRule(publicService.Methods().ByName("Delete")).GetDelete()).To(Equal("/api/fulfillment/v1/" + resource.plural + "/{id}"))
			Expect(publicService.Methods().ByName("Update")).To(BeNil(), "public %s PATCH", resource.name)

			privateUpdate := privateService.Methods().ByName("Update")
			Expect(privateUpdate).ToNot(BeNil())
			Expect(httpRule(privateUpdate).GetPatch()).To(Equal("/api/private/v1/" + resource.plural + "/{object.id}"))
		}
	})

	It("rejects legacy raw public Update invocations for every networking resource", func() {
		server := testing.NewServer()
		DeferCleanup(server.Stop)
		publicv1.RegisterVirtualNetworksServer(server.Registrar(), &publicv1.UnimplementedVirtualNetworksServer{})
		publicv1.RegisterSubnetsServer(server.Registrar(), &publicv1.UnimplementedSubnetsServer{})
		publicv1.RegisterSecurityGroupsServer(server.Registrar(), &publicv1.UnimplementedSecurityGroupsServer{})
		publicv1.RegisterExternalIPsServer(server.Registrar(), &publicv1.UnimplementedExternalIPsServer{})
		publicv1.RegisterExternalIPAttachmentsServer(server.Registrar(), &publicv1.UnimplementedExternalIPAttachmentsServer{})
		publicv1.RegisterNATGatewaysServer(server.Registrar(), &publicv1.UnimplementedNATGatewaysServer{})
		server.Start()

		connection, err := grpc.NewClient(
			server.Address(),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(connection.Close)

		for _, resource := range resources {
			err = connection.Invoke(
				context.Background(),
				"/osac.public.v1."+resource.name+"/Update",
				&emptypb.Empty{},
				&emptypb.Empty{},
			)
			Expect(grpcstatus.Code(err)).To(Equal(codes.Unimplemented), resource.name)
		}
	})
})

func findService(name string) protoreflect.ServiceDescriptor {
	descriptor, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(name))
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	service, ok := descriptor.(protoreflect.ServiceDescriptor)
	ExpectWithOffset(1, ok).To(BeTrue())
	return service
}

func httpRule(method protoreflect.MethodDescriptor) *annotations.HttpRule {
	rule, ok := proto.GetExtension(method.Options(), annotations.E_Http).(*annotations.HttpRule)
	ExpectWithOffset(1, ok).To(BeTrue())
	return rule
}
