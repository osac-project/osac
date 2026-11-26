/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions under the License.
*/

package mcpserver

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

const mockToken = "mock-token-value"

type mockVirtualNetworksClient struct {
	publicv1.VirtualNetworksClient
	getFunc    func(context.Context, *publicv1.VirtualNetworksGetRequest, ...grpc.CallOption) (*publicv1.VirtualNetworksGetResponse, error)
	createFunc func(context.Context, *publicv1.VirtualNetworksCreateRequest, ...grpc.CallOption) (*publicv1.VirtualNetworksCreateResponse, error)
	deleteFunc func(context.Context, *publicv1.VirtualNetworksDeleteRequest, ...grpc.CallOption) (*publicv1.VirtualNetworksDeleteResponse, error)
}

type mockExternalIPsClient struct {
	publicv1.ExternalIPsClient
	listFunc   func(context.Context, *publicv1.ExternalIPsListRequest, ...grpc.CallOption) (*publicv1.ExternalIPsListResponse, error)
	getFunc    func(context.Context, *publicv1.ExternalIPsGetRequest, ...grpc.CallOption) (*publicv1.ExternalIPsGetResponse, error)
	createFunc func(context.Context, *publicv1.ExternalIPsCreateRequest, ...grpc.CallOption) (*publicv1.ExternalIPsCreateResponse, error)
	deleteFunc func(context.Context, *publicv1.ExternalIPsDeleteRequest, ...grpc.CallOption) (*publicv1.ExternalIPsDeleteResponse, error)
}

type mockExternalIPPoolsClient struct {
	publicv1.ExternalIPPoolsClient
	listFunc func(context.Context, *publicv1.ExternalIPPoolsListRequest, ...grpc.CallOption) (*publicv1.ExternalIPPoolsListResponse, error)
	getFunc  func(context.Context, *publicv1.ExternalIPPoolsGetRequest, ...grpc.CallOption) (*publicv1.ExternalIPPoolsGetResponse, error)
}

func (m *mockExternalIPPoolsClient) List(
	ctx context.Context, request *publicv1.ExternalIPPoolsListRequest, options ...grpc.CallOption,
) (*publicv1.ExternalIPPoolsListResponse, error) {
	return m.listFunc(ctx, request, options...)
}

func (m *mockExternalIPPoolsClient) Get(
	ctx context.Context, request *publicv1.ExternalIPPoolsGetRequest, options ...grpc.CallOption,
) (*publicv1.ExternalIPPoolsGetResponse, error) {
	return m.getFunc(ctx, request, options...)
}

func (m *mockExternalIPsClient) List(
	ctx context.Context, request *publicv1.ExternalIPsListRequest, options ...grpc.CallOption,
) (*publicv1.ExternalIPsListResponse, error) {
	return m.listFunc(ctx, request, options...)
}

func (m *mockExternalIPsClient) Get(
	ctx context.Context, request *publicv1.ExternalIPsGetRequest, options ...grpc.CallOption,
) (*publicv1.ExternalIPsGetResponse, error) {
	return m.getFunc(ctx, request, options...)
}

func (m *mockExternalIPsClient) Create(
	ctx context.Context, request *publicv1.ExternalIPsCreateRequest, options ...grpc.CallOption,
) (*publicv1.ExternalIPsCreateResponse, error) {
	return m.createFunc(ctx, request, options...)
}

func (m *mockExternalIPsClient) Delete(
	ctx context.Context, request *publicv1.ExternalIPsDeleteRequest, options ...grpc.CallOption,
) (*publicv1.ExternalIPsDeleteResponse, error) {
	return m.deleteFunc(ctx, request, options...)
}

type mockExternalIPAttachmentsClient struct {
	publicv1.ExternalIPAttachmentsClient
	listFunc   func(context.Context, *publicv1.ExternalIPAttachmentsListRequest, ...grpc.CallOption) (*publicv1.ExternalIPAttachmentsListResponse, error)
	getFunc    func(context.Context, *publicv1.ExternalIPAttachmentsGetRequest, ...grpc.CallOption) (*publicv1.ExternalIPAttachmentsGetResponse, error)
	createFunc func(context.Context, *publicv1.ExternalIPAttachmentsCreateRequest, ...grpc.CallOption) (*publicv1.ExternalIPAttachmentsCreateResponse, error)
	deleteFunc func(context.Context, *publicv1.ExternalIPAttachmentsDeleteRequest, ...grpc.CallOption) (*publicv1.ExternalIPAttachmentsDeleteResponse, error)
}

func (m *mockExternalIPAttachmentsClient) List(
	ctx context.Context, request *publicv1.ExternalIPAttachmentsListRequest, options ...grpc.CallOption,
) (*publicv1.ExternalIPAttachmentsListResponse, error) {
	return m.listFunc(ctx, request, options...)
}

func (m *mockExternalIPAttachmentsClient) Get(
	ctx context.Context, request *publicv1.ExternalIPAttachmentsGetRequest, options ...grpc.CallOption,
) (*publicv1.ExternalIPAttachmentsGetResponse, error) {
	return m.getFunc(ctx, request, options...)
}

func (m *mockExternalIPAttachmentsClient) Create(
	ctx context.Context, request *publicv1.ExternalIPAttachmentsCreateRequest, options ...grpc.CallOption,
) (*publicv1.ExternalIPAttachmentsCreateResponse, error) {
	return m.createFunc(ctx, request, options...)
}

func (m *mockExternalIPAttachmentsClient) Delete(
	ctx context.Context, request *publicv1.ExternalIPAttachmentsDeleteRequest, options ...grpc.CallOption,
) (*publicv1.ExternalIPAttachmentsDeleteResponse, error) {
	return m.deleteFunc(ctx, request, options...)
}

type mockNATGatewaysClient struct {
	publicv1.NATGatewaysClient
	listFunc   func(context.Context, *publicv1.NATGatewaysListRequest, ...grpc.CallOption) (*publicv1.NATGatewaysListResponse, error)
	getFunc    func(context.Context, *publicv1.NATGatewaysGetRequest, ...grpc.CallOption) (*publicv1.NATGatewaysGetResponse, error)
	createFunc func(context.Context, *publicv1.NATGatewaysCreateRequest, ...grpc.CallOption) (*publicv1.NATGatewaysCreateResponse, error)
	deleteFunc func(context.Context, *publicv1.NATGatewaysDeleteRequest, ...grpc.CallOption) (*publicv1.NATGatewaysDeleteResponse, error)
}

func (m *mockNATGatewaysClient) List(
	ctx context.Context, request *publicv1.NATGatewaysListRequest, options ...grpc.CallOption,
) (*publicv1.NATGatewaysListResponse, error) {
	return m.listFunc(ctx, request, options...)
}

func (m *mockNATGatewaysClient) Get(
	ctx context.Context, request *publicv1.NATGatewaysGetRequest, options ...grpc.CallOption,
) (*publicv1.NATGatewaysGetResponse, error) {
	return m.getFunc(ctx, request, options...)
}

func (m *mockNATGatewaysClient) Create(
	ctx context.Context, request *publicv1.NATGatewaysCreateRequest, options ...grpc.CallOption,
) (*publicv1.NATGatewaysCreateResponse, error) {
	return m.createFunc(ctx, request, options...)
}

func (m *mockNATGatewaysClient) Delete(
	ctx context.Context, request *publicv1.NATGatewaysDeleteRequest, options ...grpc.CallOption,
) (*publicv1.NATGatewaysDeleteResponse, error) {
	return m.deleteFunc(ctx, request, options...)
}

func (m *mockVirtualNetworksClient) Get(
	ctx context.Context, request *publicv1.VirtualNetworksGetRequest, options ...grpc.CallOption,
) (*publicv1.VirtualNetworksGetResponse, error) {
	return m.getFunc(ctx, request, options...)
}

func (m *mockVirtualNetworksClient) Create(
	ctx context.Context, request *publicv1.VirtualNetworksCreateRequest, options ...grpc.CallOption,
) (*publicv1.VirtualNetworksCreateResponse, error) {
	return m.createFunc(ctx, request, options...)
}

func (m *mockVirtualNetworksClient) Delete(
	ctx context.Context, request *publicv1.VirtualNetworksDeleteRequest, options ...grpc.CallOption,
) (*publicv1.VirtualNetworksDeleteResponse, error) {
	return m.deleteFunc(ctx, request, options...)
}

var _ = Describe("handleCreateNetworkResource", func() {
	It("Creates a VirtualNetwork through the public API as the caller", func() {
		var capturedToken string
		var capturedObject *publicv1.VirtualNetwork
		virtualNetworks := &mockVirtualNetworksClient{
			createFunc: func(
				ctx context.Context, request *publicv1.VirtualNetworksCreateRequest, _ ...grpc.CallOption,
			) (*publicv1.VirtualNetworksCreateResponse, error) {
				capturedToken = forwardedToken(ctx)
				capturedObject = request.GetObject()
				return publicv1.VirtualNetworksCreateResponse_builder{
					Object: publicv1.VirtualNetwork_builder{
						Id:       "virtual-network-1",
						Metadata: publicv1.Metadata_builder{Version: 4}.Build(),
						Status: publicv1.VirtualNetworkStatus_builder{
							State: publicv1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING,
						}.Build(),
					}.Build(),
				}.Build(), nil
			},
		}

		handler := handleCreateNetworkResource(virtualNetworks, nil, nil)
		_, output, err := handler(context.Background(), requestWithToken(mockToken), CreateNetworkResourceInput{
			ResourceType: NetworkResourceTypeVirtualNetwork,
			Name:         "demo-network",
			IPv4Cidr:     "10.10.0.0/16",
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(output).To(Equal(CreateNetworkResourceOutput{
			ResourceType: NetworkResourceTypeVirtualNetwork,
			ID:           "virtual-network-1",
			Version:      4,
			State:        "VIRTUAL_NETWORK_STATE_PENDING",
		}))
		Expect(capturedToken).To(Equal("Bearer api-for-" + mockToken))
		Expect(capturedObject.GetMetadata().GetName()).To(Equal("demo-network"))
		Expect(capturedObject.GetMetadata().GetTenant()).To(BeEmpty())
		Expect(capturedObject.GetSpec().GetIpv4Cidr()).To(Equal("10.10.0.0/16"))
	})

	It("Creates a Subnet through the public API as the caller", func() {
		var capturedToken string
		var capturedObject *publicv1.Subnet
		subnets := &mockSubnetsClient{
			createFunc: func(
				ctx context.Context, request *publicv1.SubnetsCreateRequest, _ ...grpc.CallOption,
			) (*publicv1.SubnetsCreateResponse, error) {
				capturedToken = forwardedToken(ctx)
				capturedObject = request.GetObject()
				return publicv1.SubnetsCreateResponse_builder{
					Object: publicv1.Subnet_builder{
						Id:       "subnet-1",
						Metadata: publicv1.Metadata_builder{Version: 2}.Build(),
						Status:   publicv1.SubnetStatus_builder{State: publicv1.SubnetState_SUBNET_STATE_PENDING}.Build(),
					}.Build(),
				}.Build(), nil
			},
		}

		handler := handleCreateNetworkResource(nil, subnets, nil)
		_, output, err := handler(context.Background(), requestWithToken(mockToken), CreateNetworkResourceInput{
			ResourceType:     NetworkResourceTypeSubnet,
			Name:             "demo-subnet",
			VirtualNetworkID: "virtual-network-1",
			IPv4Cidr:         "10.10.1.0/24",
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(output.ID).To(Equal("subnet-1"))
		Expect(output.ResourceType).To(Equal(NetworkResourceTypeSubnet))
		Expect(output.Version).To(Equal(int32(2)))
		Expect(capturedToken).To(Equal("Bearer api-for-" + mockToken))
		Expect(capturedObject.GetMetadata().GetTenant()).To(BeEmpty())
		Expect(capturedObject.GetSpec().GetVirtualNetwork().GetId()).To(Equal("virtual-network-1"))
		Expect(capturedObject.GetSpec().GetIpv4Cidr()).To(Equal("10.10.1.0/24"))
	})

	It("Creates a SecurityGroup with typed rules as the caller", func() {
		var capturedObject *publicv1.SecurityGroup
		securityGroups := &mockSecurityGroupsClient{
			createFunc: func(
				_ context.Context, request *publicv1.SecurityGroupsCreateRequest, _ ...grpc.CallOption,
			) (*publicv1.SecurityGroupsCreateResponse, error) {
				capturedObject = request.GetObject()
				return publicv1.SecurityGroupsCreateResponse_builder{
					Object: publicv1.SecurityGroup_builder{
						Id:       "security-group-1",
						Metadata: publicv1.Metadata_builder{Version: 1}.Build(),
						Status: publicv1.SecurityGroupStatus_builder{
							State: publicv1.SecurityGroupState_SECURITY_GROUP_STATE_PENDING,
						}.Build(),
					}.Build(),
				}.Build(), nil
			},
		}

		handler := handleCreateNetworkResource(nil, nil, securityGroups)
		_, output, err := handler(context.Background(), requestWithToken(mockToken), CreateNetworkResourceInput{
			ResourceType:     NetworkResourceTypeSecurityGroup,
			Name:             "demo-security-group",
			VirtualNetworkID: "virtual-network-1",
			IngressRules: []NetworkSecurityRuleInput{{
				Protocol: "tcp",
				PortFrom: int32Pointer(22),
				PortTo:   int32Pointer(22),
				IPv4Cidr: "192.168.0.0/24",
			}},
			EgressRules: []NetworkSecurityRuleInput{{
				Protocol: "all",
				IPv4Cidr: "0.0.0.0/0",
			}},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(output.ID).To(Equal("security-group-1"))
		Expect(output.State).To(Equal("SECURITY_GROUP_STATE_PENDING"))
		Expect(capturedObject.GetMetadata().GetTenant()).To(BeEmpty())
		Expect(capturedObject.GetSpec().GetVirtualNetwork().GetId()).To(Equal("virtual-network-1"))
		Expect(capturedObject.GetSpec().GetIngress()).To(HaveLen(1))
		Expect(capturedObject.GetSpec().GetIngress()[0].GetProtocol()).To(Equal(publicv1.Protocol_PROTOCOL_TCP))
		Expect(capturedObject.GetSpec().GetIngress()[0].GetPortFrom()).To(Equal(int32(22)))
		Expect(capturedObject.GetSpec().GetIngress()[0].GetIpv4Cidr()).To(Equal("192.168.0.0/24"))
		Expect(capturedObject.GetSpec().GetEgress()).To(HaveLen(1))
		Expect(capturedObject.GetSpec().GetEgress()[0].GetProtocol()).To(Equal(publicv1.Protocol_PROTOCOL_ALL))
	})

	It("Rejects an edit-shaped request before any public API call", func() {
		publicCall := func() error { return errors.New("public API should not be called") }
		virtualNetworks := &mockVirtualNetworksClient{
			createFunc: func(context.Context, *publicv1.VirtualNetworksCreateRequest, ...grpc.CallOption) (*publicv1.VirtualNetworksCreateResponse, error) {
				return nil, publicCall()
			},
		}
		subnets := &mockSubnetsClient{
			createFunc: func(context.Context, *publicv1.SubnetsCreateRequest, ...grpc.CallOption) (*publicv1.SubnetsCreateResponse, error) {
				return nil, publicCall()
			},
		}

		handler := handleCreateNetworkResource(virtualNetworks, subnets, nil)
		_, _, err := handler(context.Background(), requestWithToken(mockToken), CreateNetworkResourceInput{
			ID:           "virtual-network-1",
			ResourceType: NetworkResourceTypeVirtualNetwork,
			Name:         "demo-network",
			IPv4Cidr:     "10.20.0.0/16",
		})
		envelope := decodeToolError(err)
		Expect(envelope.Category).To(Equal("invalid_request"))
		Expect(envelope.GRPCCode).To(Equal("InvalidArgument"))
		Expect(envelope.Details).To(ContainElement(ContainSubstring("unsupported_in_place")))
	})

	It("Rejects too many ingress rules before dispatch", func() {
		securityGroups := &mockSecurityGroupsClient{
			createFunc: func(context.Context, *publicv1.SecurityGroupsCreateRequest, ...grpc.CallOption) (*publicv1.SecurityGroupsCreateResponse, error) {
				return nil, errors.New("Create should not be called")
			},
		}

		handler := handleCreateNetworkResource(nil, nil, securityGroups)
		_, _, err := handler(context.Background(), requestWithToken(mockToken), CreateNetworkResourceInput{
			ResourceType:     NetworkResourceTypeSecurityGroup,
			Name:             "demo-security-group",
			VirtualNetworkID: "virtual-network-1",
			IngressRules:     make([]NetworkSecurityRuleInput, maxNetworkSecurityRules+1),
		})
		envelope := decodeToolError(err)
		Expect(envelope.Category).To(Equal("invalid_request"))
		Expect(envelope.GRPCCode).To(Equal("InvalidArgument"))
		Expect(envelope.Details).To(ContainElement(ContainSubstring("ingress_rules")))
	})

	It("Rejects too many total security rules before dispatch", func() {
		securityGroups := &mockSecurityGroupsClient{
			createFunc: func(context.Context, *publicv1.SecurityGroupsCreateRequest, ...grpc.CallOption) (*publicv1.SecurityGroupsCreateResponse, error) {
				return nil, errors.New("Create should not be called")
			},
		}

		handler := handleCreateNetworkResource(nil, nil, securityGroups)
		_, _, err := handler(context.Background(), requestWithToken(mockToken), CreateNetworkResourceInput{
			ResourceType:     NetworkResourceTypeSecurityGroup,
			Name:             "demo-security-group",
			VirtualNetworkID: "virtual-network-1",
			IngressRules:     make([]NetworkSecurityRuleInput, maxNetworkSecurityRules),
			EgressRules:      make([]NetworkSecurityRuleInput, 1),
		})
		envelope := decodeToolError(err)
		Expect(envelope.Category).To(Equal("invalid_request"))
		Expect(envelope.GRPCCode).To(Equal("InvalidArgument"))
		Expect(envelope.Details).To(ContainElement(ContainSubstring("in total")))
	})

	It("Rejects an unsupported create resource type before dispatch", func() {
		publicCall := func() error { return errors.New("public API should not be called") }
		virtualNetworks := &mockVirtualNetworksClient{
			createFunc: func(context.Context, *publicv1.VirtualNetworksCreateRequest, ...grpc.CallOption) (*publicv1.VirtualNetworksCreateResponse, error) {
				return nil, publicCall()
			},
		}

		handler := handleCreateNetworkResource(virtualNetworks, nil, nil)
		_, _, err := handler(context.Background(), requestWithToken(mockToken), CreateNetworkResourceInput{
			ResourceType: "external_ip",
			Name:         "demo-address",
		})
		envelope := decodeToolError(err)
		Expect(envelope.Category).To(Equal("invalid_request"))
		Expect(envelope.GRPCCode).To(Equal("InvalidArgument"))
		Expect(envelope.Details[0]).To(ContainSubstring("resource_type"))
	})

	DescribeTable("Rejects non-canonical or non-IPv4 CIDRs before dispatch",
		func(cidr string) {
			virtualNetworks := &mockVirtualNetworksClient{
				createFunc: func(context.Context, *publicv1.VirtualNetworksCreateRequest, ...grpc.CallOption) (*publicv1.VirtualNetworksCreateResponse, error) {
					return nil, errors.New("Create should not be called")
				},
			}

			handler := handleCreateNetworkResource(virtualNetworks, nil, nil)
			_, _, err := handler(context.Background(), requestWithToken(mockToken), CreateNetworkResourceInput{
				ResourceType: NetworkResourceTypeVirtualNetwork,
				Name:         "demo-network",
				IPv4Cidr:     cidr,
			})
			envelope := decodeToolError(err)
			Expect(envelope.Category).To(Equal("invalid_request"))
			Expect(envelope.GRPCCode).To(Equal("InvalidArgument"))
			Expect(envelope.Details[0]).To(ContainSubstring("ipv4_cidr"))
		},
		Entry("IPv6", "2001:db8::/32"),
		Entry("dual-stack text", "10.0.0.0/8,2001:db8::/32"),
		Entry("host bits set", "10.10.0.1/16"),
		Entry("not a CIDR", "10.10.0.0"),
	)

	DescribeTable("Rejects invalid SecurityGroup rules before dispatch",
		func(rule NetworkSecurityRuleInput) {
			securityGroups := &mockSecurityGroupsClient{
				createFunc: func(context.Context, *publicv1.SecurityGroupsCreateRequest, ...grpc.CallOption) (*publicv1.SecurityGroupsCreateResponse, error) {
					return nil, errors.New("Create should not be called")
				},
			}

			handler := handleCreateNetworkResource(nil, nil, securityGroups)
			_, _, err := handler(context.Background(), requestWithToken(mockToken), CreateNetworkResourceInput{
				ResourceType:     NetworkResourceTypeSecurityGroup,
				Name:             "demo-security-group",
				VirtualNetworkID: "virtual-network-1",
				IngressRules:     []NetworkSecurityRuleInput{rule},
			})
			envelope := decodeToolError(err)
			Expect(envelope.Category).To(Equal("invalid_request"))
			Expect(envelope.GRPCCode).To(Equal("InvalidArgument"))
		},
		Entry("unsupported protocol", NetworkSecurityRuleInput{Protocol: "sctp", IPv4Cidr: "0.0.0.0/0"}),
		Entry("missing TCP ports", NetworkSecurityRuleInput{Protocol: "tcp", IPv4Cidr: "0.0.0.0/0"}),
		Entry("ports on ICMP", NetworkSecurityRuleInput{
			Protocol: "icmp", PortFrom: int32Pointer(1), PortTo: int32Pointer(1), IPv4Cidr: "0.0.0.0/0",
		}),
		Entry("invalid port range", NetworkSecurityRuleInput{
			Protocol: "tcp", PortFrom: int32Pointer(100), PortTo: int32Pointer(22), IPv4Cidr: "0.0.0.0/0",
		}),
		Entry("invalid rule CIDR", NetworkSecurityRuleInput{Protocol: "all", IPv4Cidr: "2001:db8::/32"}),
	)

	DescribeTable("Maps a missing or unauthorized parent from the public Create error",
		func(resourceType NetworkResourceType, grpcCode codes.Code, errorMessage, expectedCategory string) {
			virtualNetworks := &mockVirtualNetworksClient{
				getFunc: func(
					_ context.Context, _ *publicv1.VirtualNetworksGetRequest, _ ...grpc.CallOption,
				) (*publicv1.VirtualNetworksGetResponse, error) {
					return nil, errors.New("parent Get should not be called")
				},
			}
			subnets := &mockSubnetsClient{
				createFunc: func(context.Context, *publicv1.SubnetsCreateRequest, ...grpc.CallOption) (*publicv1.SubnetsCreateResponse, error) {
					if grpcCode == codes.InvalidArgument {
						return nil, status.Error(grpcCode, errorMessage)
					}
					return nil, status.Error(grpcCode, "private upstream detail")
				},
			}
			securityGroups := &mockSecurityGroupsClient{
				createFunc: func(context.Context, *publicv1.SecurityGroupsCreateRequest, ...grpc.CallOption) (*publicv1.SecurityGroupsCreateResponse, error) {
					if grpcCode == codes.InvalidArgument {
						return nil, status.Error(grpcCode, errorMessage)
					}
					return nil, status.Error(grpcCode, "private upstream detail")
				},
			}

			handler := handleCreateNetworkResource(virtualNetworks, subnets, securityGroups)
			input := CreateNetworkResourceInput{
				ResourceType:     resourceType,
				Name:             "demo-dependent",
				VirtualNetworkID: "virtual-network-1",
			}
			if resourceType == NetworkResourceTypeSubnet {
				input.IPv4Cidr = "10.10.1.0/24"
			}
			_, _, err := handler(context.Background(), requestWithToken(mockToken), input)
			envelope := decodeToolError(err)
			Expect(envelope.Category).To(Equal(expectedCategory))
			Expect(envelope.Details).To(ContainElement(ContainSubstring("VirtualNetwork virtual-network-1")))
			Expect(err.Error()).ToNot(ContainSubstring("private upstream detail"))
		},
		Entry("missing Subnet parent", NetworkResourceTypeSubnet, codes.NotFound, "", "not_found"),
		Entry(
			"missing reference Subnet parent",
			NetworkResourceTypeSubnet,
			codes.InvalidArgument,
			"reference validation failed: object.spec.virtual_network: "+
				"VirtualNetwork \"virtual-network-1\" not found",
			"not_found",
		),
		Entry(
			"missing locked Subnet parent",
			NetworkResourceTypeSubnet,
			codes.InvalidArgument,
			"parent VirtualNetwork 'virtual-network-1' does not exist",
			"not_found",
		),
		Entry("unauthorized Subnet parent", NetworkResourceTypeSubnet, codes.PermissionDenied, "", "authorization"),
		Entry("missing SecurityGroup parent", NetworkResourceTypeSecurityGroup, codes.NotFound, "", "not_found"),
		Entry(
			"missing locked SecurityGroup parent",
			NetworkResourceTypeSecurityGroup,
			codes.InvalidArgument,
			"parent VirtualNetwork 'virtual-network-1' does not exist",
			"not_found",
		),
	)

	It("Names a missing provider-managed NetworkClass", func() {
		virtualNetworks := &mockVirtualNetworksClient{
			createFunc: func(
				_ context.Context, _ *publicv1.VirtualNetworksCreateRequest, _ ...grpc.CallOption,
			) (*publicv1.VirtualNetworksCreateResponse, error) {
				return nil, status.Error(codes.FailedPrecondition, "no NetworkClass is configured")
			},
		}

		handler := handleCreateNetworkResource(virtualNetworks, nil, nil)
		_, _, err := handler(context.Background(), requestWithToken(mockToken), CreateNetworkResourceInput{
			ResourceType: NetworkResourceTypeVirtualNetwork,
			Name:         "demo-network",
			IPv4Cidr:     "10.10.0.0/16",
		})
		envelope := decodeToolError(err)
		Expect(envelope.Category).To(Equal("not_found"))
		Expect(envelope.GRPCCode).To(Equal("NotFound"))
		Expect(envelope.Details).To(ContainElement("NetworkClass is not configured"))
	})
})

var _ = Describe("handleExposeNetworkResource", func() {
	It("Discovers an eligible ExternalIPPool and creates an ExternalIP as the caller", func() {
		var listToken string
		var capturedFilter string
		var capturedOffset, capturedLimit int32
		pools := &mockExternalIPPoolsClient{
			listFunc: func(
				ctx context.Context, request *publicv1.ExternalIPPoolsListRequest, _ ...grpc.CallOption,
			) (*publicv1.ExternalIPPoolsListResponse, error) {
				listToken = forwardedToken(ctx)
				capturedFilter = request.GetFilter()
				capturedOffset = request.GetOffset()
				capturedLimit = request.GetLimit()
				return publicv1.ExternalIPPoolsListResponse_builder{
					Size:  1,
					Total: 1,
					Items: []*publicv1.ExternalIPPool{publicv1.ExternalIPPool_builder{
						Id:       "external-ip-pool-1",
						Metadata: publicv1.Metadata_builder{Name: "provider-pool"}.Build(),
						Status:   publicv1.ExternalIPPoolStatus_builder{Available: 4}.Build(),
					}.Build()},
				}.Build(), nil
			},
		}
		var capturedToken string
		var capturedObject *publicv1.ExternalIP
		externalIPs := &mockExternalIPsClient{
			createFunc: func(
				ctx context.Context, request *publicv1.ExternalIPsCreateRequest, _ ...grpc.CallOption,
			) (*publicv1.ExternalIPsCreateResponse, error) {
				capturedToken = forwardedToken(ctx)
				capturedObject = request.GetObject()
				return publicv1.ExternalIPsCreateResponse_builder{
					Object: publicv1.ExternalIP_builder{
						Id:       "external-ip-1",
						Metadata: publicv1.Metadata_builder{Version: 3}.Build(),
						Status: publicv1.ExternalIPStatus_builder{
							State: publicv1.ExternalIPState_EXTERNAL_IP_STATE_PENDING,
						}.Build(),
					}.Build(),
				}.Build(), nil
			},
		}

		listHandler := handleListResources(newResourceRegistry(ServerDeps{ExternalIPPoolsClient: pools}))
		_, discovery, err := listHandler(context.Background(), requestWithToken(mockToken), ListResourcesInput{
			ResourceType: ResourceTypeExternalIPPool,
			Filter:       `this.id == "external-ip-pool-1"`,
			Offset:       2,
			PageSize:     20,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(discovery).To(Equal(ListResourcesOutput{
			Offset: 2,
			Size:   1,
			Total:  1,
			Items:  []ResourceSummary{{ID: "external-ip-pool-1", Name: "provider-pool"}},
		}))
		Expect(listToken).To(Equal("Bearer api-for-" + mockToken))
		Expect(capturedFilter).To(Equal(`this.id == "external-ip-pool-1"`))
		Expect(capturedOffset).To(Equal(int32(2)))
		Expect(capturedLimit).To(Equal(int32(20)))

		handler := handleExposeNetworkResource(externalIPs, nil, nil)
		_, output, err := handler(context.Background(), requestWithToken(mockToken), ExposeNetworkResourceInput{
			ResourceType:     NetworkResourceTypeExternalIP,
			Name:             "demo-address",
			ExternalIPPoolID: discovery.Items[0].ID,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(output).To(Equal(ExposeNetworkResourceOutput{
			ResourceType: NetworkResourceTypeExternalIP,
			ID:           "external-ip-1",
			Version:      3,
			State:        "EXTERNAL_IP_STATE_PENDING",
		}))
		Expect(capturedToken).To(Equal("Bearer api-for-" + mockToken))
		Expect(capturedObject.GetMetadata().GetName()).To(Equal("demo-address"))
		Expect(capturedObject.GetMetadata().GetTenant()).To(BeEmpty())
		Expect(capturedObject.GetSpec().GetPool().GetId()).To(Equal("external-ip-pool-1"))
	})

	It("Creates a cluster ExternalIPAttachment through the public API", func() {
		var capturedObject *publicv1.ExternalIPAttachment
		attachments := &mockExternalIPAttachmentsClient{
			createFunc: func(
				_ context.Context, request *publicv1.ExternalIPAttachmentsCreateRequest, _ ...grpc.CallOption,
			) (*publicv1.ExternalIPAttachmentsCreateResponse, error) {
				capturedObject = request.GetObject()
				return publicv1.ExternalIPAttachmentsCreateResponse_builder{
					Object: publicv1.ExternalIPAttachment_builder{
						Id:       "external-ip-attachment-1",
						Metadata: publicv1.Metadata_builder{Version: 2}.Build(),
						Status: publicv1.ExternalIPAttachmentStatus_builder{
							State: publicv1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_PENDING,
						}.Build(),
					}.Build(),
				}.Build(), nil
			},
		}

		handler := handleExposeNetworkResource(nil, attachments, nil)
		_, output, err := handler(context.Background(), requestWithToken(mockToken), ExposeNetworkResourceInput{
			ResourceType:    NetworkResourceTypeExternalIPAttachment,
			Name:            "demo-attachment",
			ExternalIPID:    "external-ip-1",
			ClusterID:       "cluster-1",
			ClusterEndpoint: NetworkClusterEndpointAPI,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(output.ID).To(Equal("external-ip-attachment-1"))
		Expect(output.State).To(Equal("EXTERNAL_IP_ATTACHMENT_STATE_PENDING"))
		Expect(capturedObject.GetMetadata().GetTenant()).To(BeEmpty())
		Expect(capturedObject.GetSpec().GetExternalIp().GetId()).To(Equal("external-ip-1"))
		Expect(capturedObject.GetSpec().GetCluster().GetId()).To(Equal("cluster-1"))
		Expect(capturedObject.GetSpec().GetTargetEndpoint()).To(Equal(
			publicv1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
		))
	})

	It("Creates a NATGateway through the public API", func() {
		var capturedObject *publicv1.NATGateway
		natGateways := &mockNATGatewaysClient{
			createFunc: func(
				_ context.Context, request *publicv1.NATGatewaysCreateRequest, _ ...grpc.CallOption,
			) (*publicv1.NATGatewaysCreateResponse, error) {
				capturedObject = request.GetObject()
				return publicv1.NATGatewaysCreateResponse_builder{
					Object: publicv1.NATGateway_builder{
						Id:       "nat-gateway-1",
						Metadata: publicv1.Metadata_builder{Version: 5}.Build(),
						Status: publicv1.NATGatewayStatus_builder{
							State: publicv1.NATGatewayState_NAT_GATEWAY_STATE_PENDING,
						}.Build(),
					}.Build(),
				}.Build(), nil
			},
		}

		handler := handleExposeNetworkResource(nil, nil, natGateways)
		_, output, err := handler(context.Background(), requestWithToken(mockToken), ExposeNetworkResourceInput{
			ResourceType:     NetworkResourceTypeNATGateway,
			Name:             "demo-nat-gateway",
			VirtualNetworkID: "virtual-network-1",
			ExternalIPID:     "external-ip-1",
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(output.ID).To(Equal("nat-gateway-1"))
		Expect(output.State).To(Equal("NAT_GATEWAY_STATE_PENDING"))
		Expect(capturedObject.GetMetadata().GetTenant()).To(BeEmpty())
		Expect(capturedObject.GetSpec().GetVirtualNetwork().GetId()).To(Equal("virtual-network-1"))
		Expect(capturedObject.GetSpec().GetExternalIp().GetId()).To(Equal("external-ip-1"))
	})

	It("Rejects an edit-shaped exposure before any public API call", func() {
		publicCall := func() error { return errors.New("Create should not be called") }
		externalIPs := &mockExternalIPsClient{
			createFunc: func(context.Context, *publicv1.ExternalIPsCreateRequest, ...grpc.CallOption) (*publicv1.ExternalIPsCreateResponse, error) {
				return nil, publicCall()
			},
		}

		handler := handleExposeNetworkResource(externalIPs, nil, nil)
		_, _, err := handler(context.Background(), requestWithToken(mockToken), ExposeNetworkResourceInput{
			ID:               "external-ip-1",
			ResourceType:     NetworkResourceTypeExternalIP,
			Name:             "demo-address",
			ExternalIPPoolID: "external-ip-pool-1",
		})
		envelope := decodeToolError(err)
		Expect(envelope.Category).To(Equal("invalid_request"))
		Expect(envelope.GRPCCode).To(Equal("InvalidArgument"))
		Expect(envelope.Details).To(ContainElement(ContainSubstring("unsupported_in_place")))
	})

	It("Rejects an ambiguous ExternalIPAttachment target before dispatch", func() {
		attachments := &mockExternalIPAttachmentsClient{
			createFunc: func(context.Context, *publicv1.ExternalIPAttachmentsCreateRequest, ...grpc.CallOption) (*publicv1.ExternalIPAttachmentsCreateResponse, error) {
				return nil, errors.New("Create should not be called")
			},
		}

		handler := handleExposeNetworkResource(nil, attachments, nil)
		_, _, err := handler(context.Background(), requestWithToken(mockToken), ExposeNetworkResourceInput{
			ResourceType:      NetworkResourceTypeExternalIPAttachment,
			Name:              "demo-attachment",
			ExternalIPID:      "external-ip-1",
			ComputeInstanceID: "compute-instance-1",
			ClusterID:         "cluster-1",
		})
		envelope := decodeToolError(err)
		Expect(envelope.Category).To(Equal("invalid_request"))
		Expect(envelope.GRPCCode).To(Equal("InvalidArgument"))
		Expect(envelope.Details[0]).To(ContainSubstring("exactly one target"))
	})

	It("Maps a missing ExternalIPPool to a named prerequisite error", func() {
		externalIPs := &mockExternalIPsClient{
			createFunc: func(
				context.Context, *publicv1.ExternalIPsCreateRequest, ...grpc.CallOption,
			) (*publicv1.ExternalIPsCreateResponse, error) {
				return nil, status.Error(codes.InvalidArgument, "pool 'missing-pool' does not exist")
			},
		}

		handler := handleExposeNetworkResource(externalIPs, nil, nil)
		_, _, err := handler(context.Background(), requestWithToken(mockToken), ExposeNetworkResourceInput{
			ResourceType:     NetworkResourceTypeExternalIP,
			Name:             "demo-address",
			ExternalIPPoolID: "missing-pool",
		})
		envelope := decodeToolError(err)
		Expect(envelope.Category).To(Equal("not_found"))
		Expect(envelope.GRPCCode).To(Equal("NotFound"))
		Expect(envelope.Details).To(ContainElement("ExternalIPPool missing-pool is missing"))
		Expect(err.Error()).ToNot(ContainSubstring("does not exist"))
	})

	It("Maps a missing ExternalIP attachment dependency to a named prerequisite error", func() {
		attachments := &mockExternalIPAttachmentsClient{
			createFunc: func(
				context.Context, *publicv1.ExternalIPAttachmentsCreateRequest, ...grpc.CallOption,
			) (*publicv1.ExternalIPAttachmentsCreateResponse, error) {
				return nil, status.Error(
					codes.InvalidArgument,
					`reference validation failed: object.spec.external_ip: ExternalIP "missing-ip" not found`,
				)
			},
		}

		handler := handleExposeNetworkResource(nil, attachments, nil)
		_, _, err := handler(context.Background(), requestWithToken(mockToken), ExposeNetworkResourceInput{
			ResourceType:    NetworkResourceTypeExternalIPAttachment,
			Name:            "demo-attachment",
			ExternalIPID:    "missing-ip",
			ClusterID:       "cluster-1",
			ClusterEndpoint: NetworkClusterEndpointIngress,
		})
		envelope := decodeToolError(err)
		Expect(envelope.Category).To(Equal("not_found"))
		Expect(envelope.Details).To(ContainElement("ExternalIP missing-ip is missing"))
	})
})

var _ = Describe("handleDeleteNetworkResource", func() {
	DescribeTable("Deletes only the explicitly named resource as the caller",
		func(resourceType NetworkResourceType, expectedID string) {
			var capturedToken string
			var capturedID string
			deleteVirtualNetwork := func(
				ctx context.Context, request *publicv1.VirtualNetworksDeleteRequest, _ ...grpc.CallOption,
			) (*publicv1.VirtualNetworksDeleteResponse, error) {
				capturedToken = forwardedToken(ctx)
				capturedID = request.GetId()
				return publicv1.VirtualNetworksDeleteResponse_builder{}.Build(), nil
			}
			deleteSubnet := func(
				ctx context.Context, request *publicv1.SubnetsDeleteRequest, _ ...grpc.CallOption,
			) (*publicv1.SubnetsDeleteResponse, error) {
				capturedToken = forwardedToken(ctx)
				capturedID = request.GetId()
				return publicv1.SubnetsDeleteResponse_builder{}.Build(), nil
			}
			deleteSecurityGroup := func(
				ctx context.Context, request *publicv1.SecurityGroupsDeleteRequest, _ ...grpc.CallOption,
			) (*publicv1.SecurityGroupsDeleteResponse, error) {
				capturedToken = forwardedToken(ctx)
				capturedID = request.GetId()
				return publicv1.SecurityGroupsDeleteResponse_builder{}.Build(), nil
			}
			deleteExternalIP := func(
				ctx context.Context, request *publicv1.ExternalIPsDeleteRequest, _ ...grpc.CallOption,
			) (*publicv1.ExternalIPsDeleteResponse, error) {
				capturedToken = forwardedToken(ctx)
				capturedID = request.GetId()
				return publicv1.ExternalIPsDeleteResponse_builder{}.Build(), nil
			}
			deleteExternalIPAttachment := func(
				ctx context.Context, request *publicv1.ExternalIPAttachmentsDeleteRequest, _ ...grpc.CallOption,
			) (*publicv1.ExternalIPAttachmentsDeleteResponse, error) {
				capturedToken = forwardedToken(ctx)
				capturedID = request.GetId()
				return publicv1.ExternalIPAttachmentsDeleteResponse_builder{}.Build(), nil
			}
			deleteNATGateway := func(
				ctx context.Context, request *publicv1.NATGatewaysDeleteRequest, _ ...grpc.CallOption,
			) (*publicv1.NATGatewaysDeleteResponse, error) {
				capturedToken = forwardedToken(ctx)
				capturedID = request.GetId()
				return publicv1.NATGatewaysDeleteResponse_builder{}.Build(), nil
			}

			handler := handleDeleteNetworkResource(
				&mockVirtualNetworksClient{deleteFunc: deleteVirtualNetwork},
				&mockSubnetsClient{deleteFunc: deleteSubnet},
				&mockSecurityGroupsClient{deleteFunc: deleteSecurityGroup},
				&mockExternalIPsClient{deleteFunc: deleteExternalIP},
				&mockExternalIPAttachmentsClient{deleteFunc: deleteExternalIPAttachment},
				&mockNATGatewaysClient{deleteFunc: deleteNATGateway},
			)
			_, output, err := handler(context.Background(), requestWithToken(mockToken), DeleteNetworkResourceInput{
				ResourceType: resourceType,
				ID:           expectedID,
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(output).To(Equal(DeleteNetworkResourceOutput{ResourceType: resourceType, ID: expectedID}))
			Expect(capturedID).To(Equal(expectedID))
			Expect(capturedToken).To(Equal("Bearer api-for-" + mockToken))
		},
		Entry("VirtualNetwork", NetworkResourceTypeVirtualNetwork, "virtual-network-1"),
		Entry("Subnet", NetworkResourceTypeSubnet, "subnet-1"),
		Entry("SecurityGroup", NetworkResourceTypeSecurityGroup, "security-group-1"),
		Entry("ExternalIP", NetworkResourceTypeExternalIP, "external-ip-1"),
		Entry("ExternalIPAttachment", NetworkResourceTypeExternalIPAttachment, "external-ip-attachment-1"),
		Entry("NATGateway", NetworkResourceTypeNATGateway, "nat-gateway-1"),
	)

	It("Rejects a blank target before calling the public API", func() {
		virtualNetworks := &mockVirtualNetworksClient{
			deleteFunc: func(context.Context, *publicv1.VirtualNetworksDeleteRequest, ...grpc.CallOption) (*publicv1.VirtualNetworksDeleteResponse, error) {
				return nil, errors.New("Delete should not be called")
			},
		}

		handler := handleDeleteNetworkResource(virtualNetworks, nil, nil, nil, nil, nil)
		_, _, err := handler(context.Background(), requestWithToken(mockToken), DeleteNetworkResourceInput{
			ResourceType: NetworkResourceTypeVirtualNetwork,
			ID:           " ",
		})
		envelope := decodeToolError(err)
		Expect(envelope.Category).To(Equal("invalid_request"))
		Expect(envelope.GRPCCode).To(Equal("InvalidArgument"))
	})

	It("Rejects an unsupported delete resource type before dispatch", func() {
		virtualNetworks := &mockVirtualNetworksClient{
			deleteFunc: func(context.Context, *publicv1.VirtualNetworksDeleteRequest, ...grpc.CallOption) (*publicv1.VirtualNetworksDeleteResponse, error) {
				return nil, errors.New("Delete should not be called")
			},
		}

		handler := handleDeleteNetworkResource(virtualNetworks, nil, nil, nil, nil, nil)
		_, _, err := handler(context.Background(), requestWithToken(mockToken), DeleteNetworkResourceInput{
			ResourceType: "network_class",
			ID:           "network-class-1",
		})
		envelope := decodeToolError(err)
		Expect(envelope.Category).To(Equal("invalid_request"))
		Expect(envelope.GRPCCode).To(Equal("InvalidArgument"))
		Expect(envelope.Details[0]).To(ContainSubstring("resource_type"))
	})

	It("Keeps a dependency conflict visible without exposing the upstream payload", func() {
		virtualNetworks := &mockVirtualNetworksClient{
			deleteFunc: func(
				_ context.Context, _ *publicv1.VirtualNetworksDeleteRequest, _ ...grpc.CallOption,
			) (*publicv1.VirtualNetworksDeleteResponse, error) {
				return nil, status.Error(codes.FailedPrecondition, "1 active Subnet subnet-1")
			},
		}

		handler := handleDeleteNetworkResource(virtualNetworks, nil, nil, nil, nil, nil)
		_, _, err := handler(context.Background(), requestWithToken(mockToken), DeleteNetworkResourceInput{
			ResourceType: NetworkResourceTypeVirtualNetwork,
			ID:           "virtual-network-1",
		})
		envelope := decodeToolError(err)
		Expect(envelope.Category).To(Equal("conflict"))
		Expect(envelope.GRPCCode).To(Equal("FailedPrecondition"))
		Expect(envelope.Retryable).To(BeFalse())
		Expect(envelope.Message).To(ContainSubstring("active dependencies"))
		Expect(err.Error()).ToNot(ContainSubstring("subnet-1"))
	})
})
