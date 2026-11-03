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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("FabricDomain tenant API component integration", Label("networking", "multitenancy"), func() {
	var (
		networkClassID       string
		tenantA              string
		tenantB              string
		networkClasses       privatev1.NetworkClassesClient
		virtualNetworks      privatev1.VirtualNetworksClient
		adminDomains         publicv1.FabricDomainsClient
		createdDomainIDs     []string
		createdVirtualNetIDs []string
	)

	BeforeEach(func(setupCtx context.Context) {
		createdDomainIDs = nil
		createdVirtualNetIDs = nil

		adminConn := tool.InternalView().AdminConn()
		tenants := privatev1.NewTenantsClient(adminConn)
		projects := privatev1.NewProjectsClient(adminConn)
		networkClasses = privatev1.NewNetworkClassesClient(adminConn)
		virtualNetworks = privatev1.NewVirtualNetworksClient(adminConn)
		adminDomains = publicv1.NewFabricDomainsClient(tool.ExternalView().AdminConn())
		tenantA = fmt.Sprintf("fd-it-a-%s", uuid.New()[24:])
		tenantB = fmt.Sprintf("fd-it-b-%s", uuid.New()[24:])
		for _, fixture := range []struct {
			tenantID       string
			serviceAccount string
		}{
			{tenantID: tenantA, serviceAccount: "alice"},
			{tenantID: tenantB, serviceAccount: "carol"},
		} {
			response, err := tenants.Create(setupCtx, privatev1.TenantsCreateRequest_builder{
				Object: privatev1.Tenant_builder{
					Metadata: privatev1.Metadata_builder{Name: fixture.tenantID}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := response.GetObject().GetId()
			DeferCleanup(func(cleanupCtx context.Context) {
				deleteTenantUsers(cleanupCtx, fixture.tenantID)
				deleteTenant(cleanupCtx, tenants, projects, id, fixture.tenantID)
			})

			// Service-account tenant identity comes from the Kubernetes namespace.
			// Use a test-only namespace matching this unique OSAC tenant so the JWT
			// groups carry the tenant being exercised by the API assertions.
			identityMetadata := metav1.ObjectMeta{
				Name: fixture.tenantID,
				Annotations: map[string]string{
					"osac.openshift.io/tenant":          fixture.tenantID,
					"osac.openshift.io/owner-reference": id,
				},
			}
			Expect(tool.KubeClient().Create(setupCtx, &corev1.Namespace{ObjectMeta: identityMetadata})).To(Succeed())
			DeferCleanup(func(cleanupCtx context.Context) {
				deleteErr := tool.KubeClient().Delete(cleanupCtx, &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{Name: fixture.tenantID},
				})
				Expect(client.IgnoreNotFound(deleteErr)).To(Succeed())
			})
			Expect(tool.KubeClient().Create(setupCtx, &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:        fixture.serviceAccount,
					Namespace:   fixture.tenantID,
					Annotations: identityMetadata.Annotations,
				},
			})).To(Succeed())
		}

		response, err := sharedNetworkClass(setupCtx, networkClasses)
		Expect(err).ToNot(HaveOccurred())
		networkClassID = response.GetObject().GetId()

		DeferCleanup(func(cleanupCtx context.Context) {
			for i := len(createdVirtualNetIDs) - 1; i >= 0; i-- {
				deleteAndWaitForVirtualNetwork(cleanupCtx, virtualNetworks, createdVirtualNetIDs[i])
			}
		})

		DeferCleanup(func(cleanupCtx context.Context) {
			for i := len(createdDomainIDs) - 1; i >= 0; i-- {
				deleteAndWaitForFabricDomain(cleanupCtx, adminDomains, createdDomainIDs[i])
			}
		})
	})

	createVirtualNetwork := func(ctx context.Context, tenant string) string {
		c := newNDClients()
		response, err := virtualNetworks.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
			Object: privatev1.VirtualNetwork_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("fd-it-%s-%s", tenant, uuid.New()[24:]),
					Tenant: tenant,
				}.Build(),
				Spec: privatev1.VirtualNetworkSpec_builder{
					Ipv4Cidr: new(ndVirtualNetworkCIDR()),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		id := response.GetObject().GetId()
		createdVirtualNetIDs = append(createdVirtualNetIDs, id)
		ndWaitVirtualNetworkPlaced(ctx, c, id)
		ndSetVirtualNetworkState(ctx, c, id, privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY)
		return id
	}

	createDomain := func(ctx context.Context, tenant, name, virtualNetworkID string) string {
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

	clientForTenant := func(ctx context.Context, user, tenant string) (publicv1.FabricDomainsClient, *grpc.ClientConn) {
		tokenSource, err := tool.makeKubernetesTokenSource(ctx, user, tenant)
		Expect(err).ToNot(HaveOccurred())
		conn, err := tool.makeGrpcConn(externalServiceAddr, tokenSource)
		Expect(err).ToNot(HaveOccurred())
		return publicv1.NewFabricDomainsClient(conn), conn
	}

	// requireEastWest skips the specs that store a FabricDomain: a create needs a NetworkClass with
	// supports_east_west_ethernet and servers that are BareMetalInstance hosts of the tenant. The installer's
	// NetworkClass of the dev profile doesn't enable east-west Ethernet, so in the SUITE=fulfillment target these
	// creates fail with INVALID_ARGUMENT, which TC-FD-01 checks.
	requireEastWest := func(ctx context.Context) {
		response, err := networkClasses.Get(ctx, privatev1.NetworkClassesGetRequest_builder{Id: networkClassID}.Build())
		Expect(err).ToNot(HaveOccurred())
		if !response.GetObject().GetSpec().GetEastWestCapabilities().GetSupportsEastWestEthernet() {
			Skip("FabricDomain creates need a NetworkClass with supports_east_west_ethernet and BareMetalInstance " +
				"hosts; this deployment's NetworkClass doesn't enable east-west Ethernet")
		}
	}

	It("TC-FD-01 validates type, servers, VirtualNetwork and the east-west gate through the public API",
		func(ctx context.Context) {
			vnetA := createVirtualNetwork(ctx, tenantA)
			response, err := networkClasses.Get(ctx, privatev1.NetworkClassesGetRequest_builder{
				Id: networkClassID,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			eastWest := response.GetObject().GetSpec().GetEastWestCapabilities().GetSupportsEastWestEthernet()
			for _, testCase := range []struct {
				name       string
				fabricType publicv1.FabricDomainType
				servers    []string
				vnet       string
				wantCode   grpccodes.Code
				fragment   string
			}{
				{name: "unknown type", fabricType: publicv1.FabricDomainType(999), servers: []string{"server-a"},
					vnet: vnetA, wantCode: grpccodes.InvalidArgument},
				{name: "InfiniBand type", fabricType: publicv1.FabricDomainType_FABRIC_DOMAIN_TYPE_INFINIBAND_EW,
					servers: []string{"server-a"}, vnet: vnetA, wantCode: grpccodes.Unimplemented},
				{name: "empty server list", fabricType: publicv1.FabricDomainType_FABRIC_DOMAIN_TYPE_ETHERNET_EW,
					vnet: vnetA, wantCode: grpccodes.InvalidArgument, fragment: "servers"},
				{name: "missing virtual network", fabricType: publicv1.FabricDomainType_FABRIC_DOMAIN_TYPE_ETHERNET_EW,
					servers: []string{"server-a"}, vnet: "missing", wantCode: grpccodes.InvalidArgument,
					fragment: "does not exist"},
				{name: "server that is not a bare-metal host of the tenant (or east-west not enabled)",
					fabricType: publicv1.FabricDomainType_FABRIC_DOMAIN_TYPE_ETHERNET_EW,
					servers:    []string{"server-a.example.test"}, vnet: vnetA, wantCode: grpccodes.InvalidArgument,
					fragment: map[bool]string{
						true:  "is not a bare-metal host of this tenant",
						false: "does not set east_west_capabilities.supports_east_west_ethernet",
					}[eastWest]},
			} {
				By(testCase.name)
				name := fmt.Sprintf("fd-invalid-%s", uuid.New()[24:])
				_, err := adminDomains.Create(ctx, publicv1.FabricDomainsCreateRequest_builder{
					Object: publicv1.FabricDomain_builder{
						Metadata: publicv1.Metadata_builder{Name: name, Tenant: tenantA}.Build(),
						Spec: publicv1.FabricDomainSpec_builder{
							Type: testCase.fabricType, Servers: testCase.servers, VirtualNetwork: testCase.vnet,
						}.Build(),
					}.Build(),
				}.Build())
				ndExpectCode(err, testCase.wantCode, testCase.fragment)
			}

			By("checking that no FabricDomain was stored")
			list, err := adminDomains.List(ctx, &publicv1.FabricDomainsListRequest{})
			Expect(err).ToNot(HaveOccurred())
			for _, item := range list.GetItems() {
				Expect(item.GetMetadata().GetName()).ToNot(HavePrefix("fd-invalid-"), "stored: %s", ndText(item))
			}
		})

	It("TC-FD-02 denies tenant users the FabricDomain create", func(ctx context.Context) {
		vnetA := createVirtualNetwork(ctx, tenantA)
		clientA, connA := clientForTenant(ctx, "alice", tenantA)
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
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.PermissionDenied), "error: %v", err)
	})

	It("limits tenant reads to owned domains while allowing platform-admin CRUD", func(ctx context.Context) {
		requireEastWest(ctx)
		vnetA := createVirtualNetwork(ctx, tenantA)
		vnetB := createVirtualNetwork(ctx, tenantB)
		domainA := createDomain(ctx, tenantA, fmt.Sprintf("fd-a-%s", uuid.New()[24:]), vnetA)
		domainB := createDomain(ctx, tenantB, fmt.Sprintf("fd-b-%s", uuid.New()[24:]), vnetB)

		clientA, connA := clientForTenant(ctx, "alice", tenantA)
		DeferCleanup(func() { Expect(connA.Close()).To(Succeed()) })
		clientB, connB := clientForTenant(ctx, "carol", tenantB)
		DeferCleanup(func() { Expect(connB.Close()).To(Succeed()) })

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
		Expect(own.GetObject().GetMetadata().GetTenant()).To(Equal(tenantA))
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

	It("denies tenant users create and delete", func(ctx context.Context) {
		requireEastWest(ctx)
		vnetA := createVirtualNetwork(ctx, tenantA)
		domainID := createDomain(ctx, tenantA, fmt.Sprintf("fd-protected-%s", uuid.New()[24:]), vnetA)
		clientA, connA := clientForTenant(ctx, "alice", tenantA)
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

		_, err = clientA.Delete(ctx, publicv1.FabricDomainsDeleteRequest_builder{Id: domainID}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.PermissionDenied))

		persisted, err := adminDomains.Get(ctx, publicv1.FabricDomainsGetRequest_builder{Id: domainID}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(persisted.GetObject().GetSpec().GetServers()).To(Equal([]string{"server-a.example.test"}))
	})
})

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

// deleteAndWaitForVirtualNetwork deletes a VirtualNetwork and polls
// until the resource reaches NotFound. On each iteration it calls
// Signal to wake the reconciler so any controller finalizer is removed
// promptly instead of waiting for the next watch/sync cycle. This
// matches the pattern used by deleteTenantUsers and deleteTenant.
func deleteAndWaitForVirtualNetwork(ctx context.Context, client privatev1.VirtualNetworksClient, id string) {
	GinkgoHelper()
	_, err := client.Delete(ctx, privatev1.VirtualNetworksDeleteRequest_builder{Id: id}.Build())
	if err != nil && !isGRPCCode(err, grpccodes.NotFound) {
		Expect(err).ToNot(HaveOccurred(), "deleting test VirtualNetwork %s", id)
	}
	Eventually(func(g Gomega) bool {
		_, getErr := client.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: id}.Build())
		if isGRPCCode(getErr, grpccodes.NotFound) {
			return true
		}
		g.Expect(getErr).ToNot(HaveOccurred())
		// Wake the reconciler so it processes the deletion and
		// removes the controller finalizer without delay.
		_, signalErr := client.Signal(ctx, privatev1.VirtualNetworksSignalRequest_builder{Id: id}.Build())
		if isGRPCCode(signalErr, grpccodes.NotFound) {
			return true
		}
		g.Expect(signalErr).ToNot(HaveOccurred())
		return false
	}, time.Minute, time.Second).Should(BeTrue())
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
