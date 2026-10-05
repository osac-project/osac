/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package it

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("FabricDomain tenant API component integration", Label("networking", "multitenancy"), func() {
	var (
		ctx                  context.Context
		networkClassID       string
		networkClasses       privatev1.NetworkClassesClient
		virtualNetworks      privatev1.VirtualNetworksClient
		adminDomains         publicv1.FabricDomainsClient
		createdDomainIDs     []string
		createdVirtualNetIDs []string
	)

	BeforeEach(func(testCtx context.Context) {
		ctx = testCtx
		createdDomainIDs = nil
		createdVirtualNetIDs = nil
		requireFabricDomainProvisioningDisabled(ctx)

		adminConn := tool.InternalView().AdminConn()
		networkClasses = privatev1.NewNetworkClassesClient(adminConn)
		virtualNetworks = privatev1.NewVirtualNetworksClient(adminConn)
		adminDomains = publicv1.NewFabricDomainsClient(tool.ExternalView().AdminConn())

		manager := "netris"
		response, err := networkClasses.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
			Object: privatev1.NetworkClass_builder{
				Metadata:      privatev1.Metadata_builder{Name: fmt.Sprintf("fd-it-%s", uuid.New()[24:])}.Build(),
				Title:         "FabricDomain API integration NetworkClass",
				FabricManager: &manager,
				Capabilities: privatev1.NetworkClassCapabilities_builder{
					SupportsIpv4:             true,
					SupportsEastWestEthernet: true,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		networkClassID = response.GetObject().GetId()

		DeferCleanup(func(cleanupCtx context.Context) {
			_, deleteErr := networkClasses.Delete(cleanupCtx, privatev1.NetworkClassesDeleteRequest_builder{
				Id: networkClassID,
			}.Build())
			if isGRPCCode(deleteErr, grpccodes.NotFound) {
				return
			}
			Expect(deleteErr).ToNot(HaveOccurred())
		})

		DeferCleanup(func(cleanupCtx context.Context) {
			for i := len(createdVirtualNetIDs) - 1; i >= 0; i-- {
				id := createdVirtualNetIDs[i]
				_, deleteErr := virtualNetworks.Delete(cleanupCtx, privatev1.VirtualNetworksDeleteRequest_builder{Id: id}.Build())
				if deleteErr != nil && !isGRPCCode(deleteErr, grpccodes.NotFound) {
					Expect(deleteErr).ToNot(HaveOccurred(), "deleting test VirtualNetwork %s", id)
				}
				Eventually(func(g Gomega) {
					_, getErr := virtualNetworks.Get(cleanupCtx, privatev1.VirtualNetworksGetRequest_builder{Id: id}.Build())
					g.Expect(getErr).To(HaveOccurred())
					g.Expect(grpcstatus.Code(getErr)).To(Equal(grpccodes.NotFound))
				}, time.Minute, time.Second).Should(Succeed())
			}
		})

		DeferCleanup(func(cleanupCtx context.Context) {
			for i := len(createdDomainIDs) - 1; i >= 0; i-- {
				deleteAndWaitForFabricDomain(cleanupCtx, adminDomains, createdDomainIDs[i])
			}
		})
	})

	createVirtualNetwork := func(tenant string) string {
		response, err := virtualNetworks.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
			Object: privatev1.VirtualNetwork_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("fd-it-%s-%s", tenant, uuid.New()[24:]),
					Tenant: tenant,
				}.Build(),
				Spec: privatev1.VirtualNetworkSpec_builder{
					NetworkClass: privatev1.NetworkClassReference_builder{Id: networkClassID}.Build(),
					Region:       "us-east-1",
					Ipv4Cidr:     new(uniqueCIDR()),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		id := response.GetObject().GetId()
		createdVirtualNetIDs = append(createdVirtualNetIDs, id)
		return id
	}

	createDomain := func(tenant, name, virtualNetworkID string) string {
		response, err := adminDomains.Create(ctx, publicv1.FabricDomainsCreateRequest_builder{
			Object: publicv1.FabricDomain_builder{
				Metadata: publicv1.Metadata_builder{Name: name, Tenant: tenant}.Build(),
				Spec: publicv1.FabricDomainSpec_builder{
					Type:           publicv1.FabricDomainType_FABRIC_DOMAIN_TYPE_ETHERNET_EW,
					Servers:        []string{"server-a.example.test"},
					VirtualNetwork: virtualNetworkID,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		id := response.GetObject().GetId()
		createdDomainIDs = append(createdDomainIDs, id)
		return id
	}

	clientForTenant := func(user, tenant string) (publicv1.FabricDomainsClient, *grpc.ClientConn) {
		tokenSource, err := tool.makeKubernetesTokenSource(ctx, user, tenant)
		Expect(err).ToNot(HaveOccurred())
		conn, err := tool.makeGrpcConn(externalServiceAddr, tokenSource)
		Expect(err).ToNot(HaveOccurred())
		return publicv1.NewFabricDomainsClient(conn), conn
	}

	It("validates type, membership, and VirtualNetwork references through the public API", func() {
		for _, testCase := range []struct {
			name       string
			fabricType publicv1.FabricDomainType
			servers    []string
			vnet       string
			wantCode   grpccodes.Code
		}{
			{name: "unknown type", fabricType: publicv1.FabricDomainType(999), servers: []string{"server-a"}, vnet: "unused", wantCode: grpccodes.InvalidArgument},
			{name: "empty server list", fabricType: publicv1.FabricDomainType_FABRIC_DOMAIN_TYPE_ETHERNET_EW, vnet: "unused", wantCode: grpccodes.InvalidArgument},
			{name: "missing virtual network", fabricType: publicv1.FabricDomainType_FABRIC_DOMAIN_TYPE_ETHERNET_EW, servers: []string{"server-a"}, vnet: "missing", wantCode: grpccodes.NotFound},
		} {
			By(testCase.name)
			_, err := adminDomains.Create(ctx, publicv1.FabricDomainsCreateRequest_builder{
				Object: publicv1.FabricDomain_builder{
					Metadata: publicv1.Metadata_builder{Name: fmt.Sprintf("fd-invalid-%s", uuid.New()[24:]), Tenant: "a"}.Build(),
					Spec: publicv1.FabricDomainSpec_builder{
						Type: testCase.fabricType, Servers: testCase.servers, VirtualNetwork: testCase.vnet,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(testCase.wantCode))
		}
	})

	It("limits tenant reads to owned domains while allowing platform-admin CRUD", func() {
		vnetA := createVirtualNetwork("a")
		vnetB := createVirtualNetwork("b")
		domainA := createDomain("a", fmt.Sprintf("fd-a-%s", uuid.New()[24:]), vnetA)
		domainB := createDomain("b", fmt.Sprintf("fd-b-%s", uuid.New()[24:]), vnetB)

		clientA, connA := clientForTenant("alice", "a")
		DeferCleanup(func() { Expect(connA.Close()).To(Succeed()) })
		clientB, connB := clientForTenant("carol", "b")
		DeferCleanup(func() { Expect(connB.Close()).To(Succeed()) })

		updateResponse, err := adminDomains.Update(ctx, publicv1.FabricDomainsUpdateRequest_builder{
			Object: publicv1.FabricDomain_builder{
				Id: domainA,
				Spec: publicv1.FabricDomainSpec_builder{
					Servers: []string{"server-a.example.test", "server-b.example.test"},
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.servers"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(updateResponse.GetObject().GetSpec().GetServers()).To(Equal([]string{"server-a.example.test", "server-b.example.test"}))

		_, err = adminDomains.Update(ctx, publicv1.FabricDomainsUpdateRequest_builder{
			Object: publicv1.FabricDomain_builder{
				Id:   domainA,
				Spec: publicv1.FabricDomainSpec_builder{Type: publicv1.FabricDomainType_FABRIC_DOMAIN_TYPE_INFINIBAND_EW}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.type"}},
		}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument), "FabricDomain type is immutable")

		listA, err := clientA.List(ctx, &publicv1.FabricDomainsListRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(fabricDomainIDs(listA.GetItems())).To(ContainElement(domainA))
		Expect(fabricDomainIDs(listA.GetItems())).ToNot(ContainElement(domainB))
		listB, err := clientB.List(ctx, &publicv1.FabricDomainsListRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(fabricDomainIDs(listB.GetItems())).To(ContainElement(domainB))
		Expect(fabricDomainIDs(listB.GetItems())).ToNot(ContainElement(domainA))

		own, err := clientA.Get(ctx, publicv1.FabricDomainsGetRequest_builder{Id: domainA}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(own.GetObject().GetMetadata().GetTenant()).To(Equal("a"))
		_, err = clientA.Get(ctx, publicv1.FabricDomainsGetRequest_builder{Id: domainB}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound))
		_, err = clientB.Get(ctx, publicv1.FabricDomainsGetRequest_builder{Id: domainA}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound))

		deleteAndWaitForFabricDomain(ctx, adminDomains, domainA)
		listA, err = clientA.List(ctx, &publicv1.FabricDomainsListRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(fabricDomainIDs(listA.GetItems())).ToNot(ContainElement(domainA))
		listB, err = clientB.List(ctx, &publicv1.FabricDomainsListRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(fabricDomainIDs(listB.GetItems())).To(ContainElement(domainB))
	})

	It("denies tenant users create, update, and delete", func() {
		vnetA := createVirtualNetwork("a")
		domainID := createDomain("a", fmt.Sprintf("fd-protected-%s", uuid.New()[24:]), vnetA)
		clientA, connA := clientForTenant("alice", "a")
		DeferCleanup(func() { Expect(connA.Close()).To(Succeed()) })

		created, err := clientA.Create(ctx, publicv1.FabricDomainsCreateRequest_builder{
			Object: publicv1.FabricDomain_builder{
				Metadata: publicv1.Metadata_builder{Name: fmt.Sprintf("fd-denied-%s", uuid.New()[24:])}.Build(),
				Spec: publicv1.FabricDomainSpec_builder{
					Type:    publicv1.FabricDomainType_FABRIC_DOMAIN_TYPE_ETHERNET_EW,
					Servers: []string{"server-b.example.test"}, VirtualNetwork: vnetA,
				}.Build(),
			}.Build(),
		}.Build())
		if err == nil {
			createdDomainIDs = append(createdDomainIDs, created.GetObject().GetId())
		}
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.PermissionDenied))

		_, err = clientA.Update(ctx, publicv1.FabricDomainsUpdateRequest_builder{
			Object: publicv1.FabricDomain_builder{
				Id:   domainID,
				Spec: publicv1.FabricDomainSpec_builder{Servers: []string{"server-b.example.test"}}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.servers"}},
		}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.PermissionDenied))
		_, err = clientA.Delete(ctx, publicv1.FabricDomainsDeleteRequest_builder{Id: domainID}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.PermissionDenied))

		persisted, err := adminDomains.Get(ctx, publicv1.FabricDomainsGetRequest_builder{Id: domainID}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(persisted.GetObject().GetSpec().GetServers()).To(Equal([]string{"server-a.example.test"}))
	})
})

func requireFabricDomainProvisioningDisabled(ctx context.Context) {
	GinkgoHelper()
	deployments := &appsv1.DeploymentList{}
	Expect(tool.KubeClient().List(ctx, deployments, client.InNamespace("osac"))).To(Succeed())
	for _, deployment := range deployments.Items {
		for _, container := range deployment.Spec.Template.Spec.Containers {
			for _, envVar := range container.Env {
				if envVar.Name == "OSAC_ENABLE_NETWORKING_PROVISIONING" && strings.EqualFold(envVar.Value, "true") {
					Skip(fmt.Sprintf("FabricDomain API integration requires networking provisioning disabled; %s/%s is enabled", deployment.Namespace, deployment.Name))
				}
			}
		}
	}
}

func deleteAndWaitForFabricDomain(ctx context.Context, domains publicv1.FabricDomainsClient, id string) {
	GinkgoHelper()
	_, err := domains.Delete(ctx, publicv1.FabricDomainsDeleteRequest_builder{Id: id}.Build())
	if err != nil && !isGRPCCode(err, grpccodes.NotFound) {
		Expect(err).ToNot(HaveOccurred())
	}
	Eventually(func(g Gomega) {
		_, getErr := domains.Get(ctx, publicv1.FabricDomainsGetRequest_builder{Id: id}.Build())
		g.Expect(getErr).To(HaveOccurred())
		g.Expect(grpcstatus.Code(getErr)).To(Equal(grpccodes.NotFound))
	}, time.Minute, time.Second).Should(Succeed())
}

func fabricDomainIDs(items []*publicv1.FabricDomain) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.GetId())
	}
	return ids
}

func isGRPCCode(err error, code grpccodes.Code) bool {
	return err != nil && grpcstatus.Code(err) == code
}
