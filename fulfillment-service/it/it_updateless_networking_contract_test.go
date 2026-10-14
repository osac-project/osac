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
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// publicNetworkingProbes groups the read, guarded-delete, and update probes for one
// public networking service. Update is probed through a raw connection invoke because
// the generated clients have no Update method for these services.
type publicNetworkingProbes struct {
	list   func(context.Context) error
	get    func(context.Context, string) error
	delete func(context.Context, string) error
	update func(context.Context, string) error
}

func publicUpdateProbe[Req, Resp proto.Message](
	conn *grpc.ClientConn, method string, req Req, resp Resp,
) func(context.Context, string) error {
	return func(ctx context.Context, _ string) error {
		return conn.Invoke(ctx, method, req, resp)
	}
}

var _ = Describe("Updateless networking API contract", Label("updateless-networking"), func() {
	var (
		ctx   context.Context
		user  *grpc.ClientConn
		admin *grpc.ClientConn
	)

	BeforeEach(func() {
		ctx = context.Background()
		user = tool.ExternalView().UserConn()
		admin = tool.InternalView().AdminConn()
	})

	DescribeTable("serves the create/read/delete-only contract through the public tenant API",
		func(service string, probes publicNetworkingProbes) {
			uniqueID := fmt.Sprintf("osac-5374-%s", uuid.New())

			By(fmt.Sprintf("listing %s for a tenant caller", service))
			Expect(probes.list(ctx)).ToNot(HaveOccurred())

			By("returning NotFound for an unknown identifier")
			Expect(grpcstatus.Code(probes.get(ctx, uniqueID))).To(Equal(grpccodes.NotFound))

			By("guarding delete of an unknown identifier without persisting anything")
			Expect(grpcstatus.Code(probes.delete(ctx, uniqueID))).To(Equal(grpccodes.NotFound))

			By("rejecting an update attempt as unimplemented")
			Expect(grpcstatus.Code(probes.update(ctx, uniqueID))).To(Equal(grpccodes.Unimplemented))
		},
		Entry("VirtualNetworks", "VirtualNetworks", publicNetworkingProbes{
			list: func(ctx context.Context) error {
				_, err := publicv1.NewVirtualNetworksClient(user).List(ctx, publicv1.VirtualNetworksListRequest_builder{}.Build())
				return err
			},
			get: func(ctx context.Context, id string) error {
				_, err := publicv1.NewVirtualNetworksClient(user).Get(ctx, publicv1.VirtualNetworksGetRequest_builder{Id: id}.Build())
				return err
			},
			delete: func(ctx context.Context, id string) error {
				_, err := publicv1.NewVirtualNetworksClient(user).Delete(ctx, publicv1.VirtualNetworksDeleteRequest_builder{Id: id}.Build())
				return err
			},
			update: publicUpdateProbe(user, "/osac.public.v1.VirtualNetworks/Update",
				&publicv1.VirtualNetworksGetRequest{}, &publicv1.VirtualNetworksGetResponse{}),
		}),
		Entry("Subnets", "Subnets", publicNetworkingProbes{
			list: func(ctx context.Context) error {
				_, err := publicv1.NewSubnetsClient(user).List(ctx, publicv1.SubnetsListRequest_builder{}.Build())
				return err
			},
			get: func(ctx context.Context, id string) error {
				_, err := publicv1.NewSubnetsClient(user).Get(ctx, publicv1.SubnetsGetRequest_builder{Id: id}.Build())
				return err
			},
			delete: func(ctx context.Context, id string) error {
				_, err := publicv1.NewSubnetsClient(user).Delete(ctx, publicv1.SubnetsDeleteRequest_builder{Id: id}.Build())
				return err
			},
			update: publicUpdateProbe(user, "/osac.public.v1.Subnets/Update",
				&publicv1.SubnetsGetRequest{}, &publicv1.SubnetsGetResponse{}),
		}),
		Entry("SecurityGroups", "SecurityGroups", publicNetworkingProbes{
			list: func(ctx context.Context) error {
				_, err := publicv1.NewSecurityGroupsClient(user).List(ctx, publicv1.SecurityGroupsListRequest_builder{}.Build())
				return err
			},
			get: func(ctx context.Context, id string) error {
				_, err := publicv1.NewSecurityGroupsClient(user).Get(ctx, publicv1.SecurityGroupsGetRequest_builder{Id: id}.Build())
				return err
			},
			delete: func(ctx context.Context, id string) error {
				_, err := publicv1.NewSecurityGroupsClient(user).Delete(ctx, publicv1.SecurityGroupsDeleteRequest_builder{Id: id}.Build())
				return err
			},
			update: publicUpdateProbe(user, "/osac.public.v1.SecurityGroups/Update",
				&publicv1.SecurityGroupsGetRequest{}, &publicv1.SecurityGroupsGetResponse{}),
		}),
		Entry("ExternalIPs", "ExternalIPs", publicNetworkingProbes{
			list: func(ctx context.Context) error {
				_, err := publicv1.NewExternalIPsClient(user).List(ctx, publicv1.ExternalIPsListRequest_builder{}.Build())
				return err
			},
			get: func(ctx context.Context, id string) error {
				_, err := publicv1.NewExternalIPsClient(user).Get(ctx, publicv1.ExternalIPsGetRequest_builder{Id: id}.Build())
				return err
			},
			delete: func(ctx context.Context, id string) error {
				_, err := publicv1.NewExternalIPsClient(user).Delete(ctx, publicv1.ExternalIPsDeleteRequest_builder{Id: id}.Build())
				return err
			},
			update: publicUpdateProbe(user, "/osac.public.v1.ExternalIPs/Update",
				&publicv1.ExternalIPsGetRequest{}, &publicv1.ExternalIPsGetResponse{}),
		}),
		Entry("ExternalIPAttachments", "ExternalIPAttachments", publicNetworkingProbes{
			list: func(ctx context.Context) error {
				_, err := publicv1.NewExternalIPAttachmentsClient(user).List(ctx, publicv1.ExternalIPAttachmentsListRequest_builder{}.Build())
				return err
			},
			get: func(ctx context.Context, id string) error {
				_, err := publicv1.NewExternalIPAttachmentsClient(user).Get(ctx, publicv1.ExternalIPAttachmentsGetRequest_builder{Id: id}.Build())
				return err
			},
			delete: func(ctx context.Context, id string) error {
				_, err := publicv1.NewExternalIPAttachmentsClient(user).Delete(ctx, publicv1.ExternalIPAttachmentsDeleteRequest_builder{Id: id}.Build())
				return err
			},
			update: publicUpdateProbe(user, "/osac.public.v1.ExternalIPAttachments/Update",
				&publicv1.ExternalIPAttachmentsGetRequest{}, &publicv1.ExternalIPAttachmentsGetResponse{}),
		}),
		Entry("NATGateways", "NATGateways", publicNetworkingProbes{
			list: func(ctx context.Context) error {
				_, err := publicv1.NewNATGatewaysClient(user).List(ctx, publicv1.NATGatewaysListRequest_builder{}.Build())
				return err
			},
			get: func(ctx context.Context, id string) error {
				_, err := publicv1.NewNATGatewaysClient(user).Get(ctx, publicv1.NATGatewaysGetRequest_builder{Id: id}.Build())
				return err
			},
			delete: func(ctx context.Context, id string) error {
				_, err := publicv1.NewNATGatewaysClient(user).Delete(ctx, publicv1.NATGatewaysDeleteRequest_builder{Id: id}.Build())
				return err
			},
			update: publicUpdateProbe(user, "/osac.public.v1.NATGateways/Update",
				&publicv1.NATGatewaysGetRequest{}, &publicv1.NATGatewaysGetResponse{}),
		}),
	)

	It("keeps the ExternalIPPool provider lifecycle working through the private API", func(ctx context.Context) {
		By("creating a disposable provider pool")
		pools := privatev1.NewExternalIPPoolsClient(admin)
		name := fmt.Sprintf("updateless-pool-%s", uuid.New()[24:])
		cidr := uniqueCIDR()
		response, err := pools.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
			Object: privatev1.ExternalIPPool_builder{
				Metadata: privatev1.Metadata_builder{Name: name}.Build(),
				Spec: privatev1.ExternalIPPoolSpec_builder{
					Cidrs:    []string{cidr},
					IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		id := response.GetObject().GetId()
		DeferCleanup(func(cleanupCtx context.Context) {
			_, _ = pools.Delete(cleanupCtx, privatev1.ExternalIPPoolsDeleteRequest_builder{Id: id}.Build())
		})

		By("reading the pool back and listing it")
		getResponse, err := pools.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: id}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetId()).To(Equal(id))
		Expect(getResponse.GetObject().GetSpec().GetCidrs()).To(Equal([]string{cidr}))
		listResponse, err := pools.List(ctx, privatev1.ExternalIPPoolsListRequest_builder{}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse.GetItems()).To(ContainElement(WithTransform(
			func(object *privatev1.ExternalIPPool) string { return object.GetId() }, Equal(id))))

		By("deleting the disposable pool and confirming NotFound")
		_, err = pools.Delete(ctx, privatev1.ExternalIPPoolsDeleteRequest_builder{Id: id}.Build())
		Expect(err).ToNot(HaveOccurred())
		_, err = pools.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: id}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound))
	})

	It("cannot edit a tenant networking resource through the CLI", func(ctx context.Context) {
		By("logging the CLI in")
		homeDir, err := tool.NewCLIHomeDir()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			Expect(os.RemoveAll(homeDir)).To(Succeed())
		})
		_, _, exitCode := tool.LoginCLI(ctx, homeDir, adminUsername, adminsPassword)
		Expect(exitCode).To(Equal(0))

		By("attempting to edit a VirtualNetwork")
		probeID := fmt.Sprintf("osac-5374-cli-%s", uuid.New())
		stdout, stderr, exitCode := tool.RunCLI(ctx, homeDir, "edit", "virtualnetwork", probeID)
		Expect(exitCode).ToNot(Equal(0), "editing an immutable networking resource should fail")
		Expect(stdout + stderr).To(ContainSubstring("immutable; updates are not supported"))

		By("issuing no write for the probed identifier")
		_, err = publicv1.NewVirtualNetworksClient(user).Get(ctx, publicv1.VirtualNetworksGetRequest_builder{
			Id: probeID,
		}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound))
	})
})
