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

type mockVirtualNetworksClient struct {
	publicv1.VirtualNetworksClient
	getFunc    func(context.Context, *publicv1.VirtualNetworksGetRequest, ...grpc.CallOption) (*publicv1.VirtualNetworksGetResponse, error)
	createFunc func(context.Context, *publicv1.VirtualNetworksCreateRequest, ...grpc.CallOption) (*publicv1.VirtualNetworksCreateResponse, error)
	deleteFunc func(context.Context, *publicv1.VirtualNetworksDeleteRequest, ...grpc.CallOption) (*publicv1.VirtualNetworksDeleteResponse, error)
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
		_, output, err := handler(context.Background(), requestWithToken("raw-bearer-value"), CreateNetworkResourceInput{
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
		Expect(capturedToken).To(Equal("Bearer api-for-raw-bearer-value"))
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
		_, output, err := handler(context.Background(), requestWithToken("raw-bearer-value"), CreateNetworkResourceInput{
			ResourceType:     NetworkResourceTypeSubnet,
			Name:             "demo-subnet",
			VirtualNetworkID: "virtual-network-1",
			IPv4Cidr:         "10.10.1.0/24",
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(output.ID).To(Equal("subnet-1"))
		Expect(output.ResourceType).To(Equal(NetworkResourceTypeSubnet))
		Expect(output.Version).To(Equal(int32(2)))
		Expect(capturedToken).To(Equal("Bearer api-for-raw-bearer-value"))
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
		_, output, err := handler(context.Background(), requestWithToken("raw-bearer-value"), CreateNetworkResourceInput{
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
		_, _, err := handler(context.Background(), requestWithToken("raw-bearer-value"), CreateNetworkResourceInput{
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
		_, _, err := handler(context.Background(), requestWithToken("raw-bearer-value"), CreateNetworkResourceInput{
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
		_, _, err := handler(context.Background(), requestWithToken("raw-bearer-value"), CreateNetworkResourceInput{
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
		_, _, err := handler(context.Background(), requestWithToken("raw-bearer-value"), CreateNetworkResourceInput{
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
			_, _, err := handler(context.Background(), requestWithToken("raw-bearer-value"), CreateNetworkResourceInput{
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
			_, _, err := handler(context.Background(), requestWithToken("raw-bearer-value"), CreateNetworkResourceInput{
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
			_, _, err := handler(context.Background(), requestWithToken("raw-bearer-value"), input)
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
		_, _, err := handler(context.Background(), requestWithToken("raw-bearer-value"), CreateNetworkResourceInput{
			ResourceType: NetworkResourceTypeVirtualNetwork,
			Name:         "demo-network",
			IPv4Cidr:     "10.10.0.0/16",
		})
		envelope := decodeToolError(err)
		Expect(envelope.Category).To(Equal("not_found"))
		Expect(envelope.GRPCCode).To(Equal("NotFound"))
		Expect(envelope.Details).To(ContainElement("NetworkClass  is missing"))
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

			handler := handleDeleteNetworkResource(
				&mockVirtualNetworksClient{deleteFunc: deleteVirtualNetwork},
				&mockSubnetsClient{deleteFunc: deleteSubnet},
				&mockSecurityGroupsClient{deleteFunc: deleteSecurityGroup},
			)
			_, output, err := handler(context.Background(), requestWithToken("raw-bearer-value"), DeleteNetworkResourceInput{
				ResourceType: resourceType,
				ID:           expectedID,
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(output).To(Equal(DeleteNetworkResourceOutput{ResourceType: resourceType, ID: expectedID}))
			Expect(capturedID).To(Equal(expectedID))
			Expect(capturedToken).To(Equal("Bearer api-for-raw-bearer-value"))
		},
		Entry("VirtualNetwork", NetworkResourceTypeVirtualNetwork, "virtual-network-1"),
		Entry("Subnet", NetworkResourceTypeSubnet, "subnet-1"),
		Entry("SecurityGroup", NetworkResourceTypeSecurityGroup, "security-group-1"),
	)

	It("Rejects a blank target before calling the public API", func() {
		virtualNetworks := &mockVirtualNetworksClient{
			deleteFunc: func(context.Context, *publicv1.VirtualNetworksDeleteRequest, ...grpc.CallOption) (*publicv1.VirtualNetworksDeleteResponse, error) {
				return nil, errors.New("Delete should not be called")
			},
		}

		handler := handleDeleteNetworkResource(virtualNetworks, nil, nil)
		_, _, err := handler(context.Background(), requestWithToken("raw-bearer-value"), DeleteNetworkResourceInput{
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

		handler := handleDeleteNetworkResource(virtualNetworks, nil, nil)
		_, _, err := handler(context.Background(), requestWithToken("raw-bearer-value"), DeleteNetworkResourceInput{
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

		handler := handleDeleteNetworkResource(virtualNetworks, nil, nil)
		_, _, err := handler(context.Background(), requestWithToken("raw-bearer-value"), DeleteNetworkResourceInput{
			ResourceType: NetworkResourceTypeVirtualNetwork,
			ID:           "virtual-network-1",
		})
		envelope := decodeToolError(err)
		Expect(envelope.Category).To(Equal("conflict"))
		Expect(envelope.GRPCCode).To(Equal("Aborted"))
		Expect(envelope.Retryable).To(BeFalse())
		Expect(envelope.Message).To(ContainSubstring("active dependencies"))
		Expect(err.Error()).ToNot(ContainSubstring("subnet-1"))
	})
})
