/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package servers

import (
	"context"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"time"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Private fabric domains server", func() {
	var server *PrivateFabricDomainsServer
	var object *privatev1.FabricDomain

	BeforeEach(func() {
		var err error
		server, err = NewPrivateFabricDomainsServer().SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		object = privatev1.FabricDomain_builder{
			Metadata: privatev1.Metadata_builder{Name: "test-domain"}.Build(),
			Spec: privatev1.FabricDomainSpec_builder{
				Type:           privatev1.FabricDomainType_FABRIC_DOMAIN_TYPE_ETHERNET_EW,
				VirtualNetwork: "test-network", Servers: []string{"server-a"},
			}.Build(),
		}.Build()
	})

	seedNetwork := func(manager string, ethernet bool, config *privatev1.EthernetEastWestConfig) {
		ncDAO, err := dao.NewGenericDAO[*privatev1.NetworkClass]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		_, err = ncDAO.Create().SetObject(privatev1.NetworkClass_builder{
			Id:            "test-class",
			Metadata:      privatev1.Metadata_builder{Name: "test-class", Tenant: auth.SharedTenant}.Build(),
			FabricManager: &manager,
			Capabilities:  privatev1.NetworkClassCapabilities_builder{SupportsEastWestEthernet: ethernet}.Build(),
			Spec: privatev1.NetworkClassSpec_builder{
				EastWestConfig: privatev1.EastWestConfig_builder{EthernetEw: config}.Build(),
			}.Build(),
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		vnDAO, err := dao.NewGenericDAO[*privatev1.VirtualNetwork]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		_, err = vnDAO.Create().SetObject(privatev1.VirtualNetwork_builder{
			Id:       "test-network",
			Metadata: privatev1.Metadata_builder{Name: "test-network", Tenant: testTenant, Finalizers: []string{"test/cleanup"}}.Build(),
			Spec: privatev1.VirtualNetworkSpec_builder{
				NetworkClass: privatev1.NetworkClassReference_builder{Id: "test-class"}.Build(),
				Region:       "region-a", Ipv4Cidr: new("10.0.0.0/16"),
			}.Build(),
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
	}

	DescribeTable("creates Ethernet domains without a NetworkClass template", func(config *privatev1.EthernetEastWestConfig) {
		seedNetwork("netris", true, config)
		response, err := server.Create(ctx, privatev1.FabricDomainsCreateRequest_builder{Object: object}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetObject().GetSpec().GetServers()).To(Equal([]string{"server-a"}))
		Expect(response.GetObject().GetStatus().GetConditions()).To(HaveLen(1))
		Expect(response.GetObject().GetStatus().GetConditions()[0].GetType()).To(Equal(privatev1.FabricDomainConditionType_FABRIC_DOMAIN_CONDITION_TYPE_PROGRESSING))
	},
		Entry("absent Ethernet config", (*privatev1.EthernetEastWestConfig)(nil)),
		Entry("empty future policy config", &privatev1.EthernetEastWestConfig{}),
	)

	It("requires the NetworkClass Ethernet capability", func() {
		seedNetwork("netris", false, nil)
		_, err := server.Create(ctx, privatev1.FabricDomainsCreateRequest_builder{Object: object}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err).To(MatchError(ContainSubstring("capability")))
	})

	DescribeTable("requires the Netris fabric manager", func(manager string) {
		seedNetwork(manager, true, nil)
		_, err := server.Create(ctx, privatev1.FabricDomainsCreateRequest_builder{Object: object}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
		Expect(err).To(MatchError(ContainSubstring("netris")))
	}, Entry("unset", ""), Entry("unsupported", "neutron"))

	DescribeTable("rejects unsupported fabric types", func(kind privatev1.FabricDomainType) {
		object.GetSpec().SetType(kind)
		_, err := server.Create(ctx, privatev1.FabricDomainsCreateRequest_builder{Object: object}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.Unimplemented))
	},
		Entry("InfiniBand", privatev1.FabricDomainType_FABRIC_DOMAIN_TYPE_INFINIBAND_EW),
		Entry("NVLink", privatev1.FabricDomainType_FABRIC_DOMAIN_TYPE_NVLINK),
	)

	It("requires servers", func() {
		object.GetSpec().SetServers(nil)
		_, err := server.Create(ctx, privatev1.FabricDomainsCreateRequest_builder{Object: object}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err).To(MatchError(ContainSubstring("servers")))
	})

	It("requires a VirtualNetwork", func() {
		object.GetSpec().SetVirtualNetwork("")
		_, err := server.Create(ctx, privatev1.FabricDomainsCreateRequest_builder{Object: object}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err).To(MatchError(ContainSubstring("virtual_network")))
	})

	It("updates membership without requiring a template on NetworkClass", func() {
		seedNetwork("netris", true, nil)
		created, err := server.Create(ctx, privatev1.FabricDomainsCreateRequest_builder{Object: object}.Build())
		Expect(err).ToNot(HaveOccurred())
		updated, err := server.Update(ctx, privatev1.FabricDomainsUpdateRequest_builder{
			Object: privatev1.FabricDomain_builder{
				Id:   created.GetObject().GetId(),
				Spec: privatev1.FabricDomainSpec_builder{Servers: []string{"server-a", "server-b"}}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.servers"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(updated.GetObject().GetSpec().GetServers()).To(Equal([]string{"server-a", "server-b"}))
		Expect(updated.GetObject().GetSpec().GetVirtualNetwork()).To(Equal("test-network"))
	})

	DescribeTable("retains immutable and nonempty update validation", func(path string) {
		seedNetwork("netris", true, nil)
		created, err := server.Create(ctx, privatev1.FabricDomainsCreateRequest_builder{Object: object}.Build())
		Expect(err).ToNot(HaveOccurred())
		_, err = server.Update(ctx, privatev1.FabricDomainsUpdateRequest_builder{
			Object:     privatev1.FabricDomain_builder{Id: created.GetObject().GetId()}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{path}},
		}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
	},
		Entry("type", "spec.type"),
		Entry("VirtualNetwork", "spec.virtual_network"),
		Entry("servers", "spec.servers"),
		Entry("status", "status"),
	)

	It("allows the fulfillment controller to update FabricDomain-owned status", func() {
		seedNetwork("netris", true, nil)
		created, err := server.Create(ctx, privatev1.FabricDomainsCreateRequest_builder{Object: object}.Build())
		Expect(err).NotTo(HaveOccurred())

		controllerCtx := auth.ContextWithSubject(ctx, &auth.Subject{User: "service-account-osac-controller"})
		updated, err := server.Update(controllerCtx, privatev1.FabricDomainsUpdateRequest_builder{
			Object: privatev1.FabricDomain_builder{
				Id:     created.GetObject().GetId(),
				Status: privatev1.FabricDomainStatus_builder{Hub: "hub-a"}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.hub"}},
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(updated.GetObject().GetStatus().GetHub()).To(Equal("hub-a"))

		updated, err = server.Update(controllerCtx, privatev1.FabricDomainsUpdateRequest_builder{
			Object: privatev1.FabricDomain_builder{
				Id: created.GetObject().GetId(),
				Status: privatev1.FabricDomainStatus_builder{
					BackendId: "cluster-42",
					VpcId:     "vpc-7",
					Members: []*privatev1.FabricDomainMemberStatus{privatev1.FabricDomainMemberStatus_builder{
						Server: "server-a",
						State:  privatev1.FabricDomainMemberState_FABRIC_DOMAIN_MEMBER_STATE_ACTIVE,
					}.Build()},
					Conditions: []*privatev1.FabricDomainCondition{privatev1.FabricDomainCondition_builder{
						Type:   privatev1.FabricDomainConditionType_FABRIC_DOMAIN_CONDITION_TYPE_READY,
						Status: privatev1.ConditionStatus_CONDITION_STATUS_TRUE,
					}.Build()},
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{
				"status.backend_id", "status.vpc_id", "status.members", "status.conditions",
			}},
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(updated.GetObject().GetStatus().GetBackendId()).To(Equal("cluster-42"))
		Expect(updated.GetObject().GetStatus().GetVpcId()).To(Equal("vpc-7"))
		Expect(updated.GetObject().GetStatus().GetMembers()).To(HaveLen(1))
		Expect(updated.GetObject().GetStatus().GetConditions()).To(HaveLen(1))

		_, err = server.Update(controllerCtx, privatev1.FabricDomainsUpdateRequest_builder{
			Object:     privatev1.FabricDomain_builder{Id: created.GetObject().GetId()}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status"}},
		}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
	})

	Describe("VirtualNetwork lifecycle", func() {
		var vnServer *PrivateVirtualNetworksServer
		BeforeEach(func() {
			var err error
			vnServer, err = NewPrivateVirtualNetworksServer().SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			seedNetwork("netris", true, nil)
		})

		It("rejects a cross-tenant VN even with administrator visibility", func() {
			object.GetMetadata().SetTenant("another-tenant")
			_, err := server.Create(ctx, privatev1.FabricDomainsCreateRequest_builder{Object: object}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(err).To(MatchError(ContainSubstring("same tenant")))
		})

		It("rejects an already deleting VN", func() {
			_, err := vnServer.Delete(ctx, privatev1.VirtualNetworksDeleteRequest_builder{Id: "test-network"}.Build())
			Expect(err).ToNot(HaveOccurred())
			_, err = server.Create(ctx, privatev1.FabricDomainsCreateRequest_builder{Object: object}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
			Expect(err).To(MatchError(ContainSubstring("being deleted")))
		})

		DescribeTable("blocks VN deletion until a referencing domain is archived", func(deleting bool) {
			object.GetMetadata().SetFinalizers([]string{"test/cleanup"})
			created, err := server.Create(ctx, privatev1.FabricDomainsCreateRequest_builder{Object: object}.Build())
			Expect(err).ToNot(HaveOccurred())
			if deleting {
				_, err = server.Delete(ctx, privatev1.FabricDomainsDeleteRequest_builder{Id: created.GetObject().GetId()}.Build())
				Expect(err).ToNot(HaveOccurred())
			}
			_, err = vnServer.Delete(ctx, privatev1.VirtualNetworksDeleteRequest_builder{Id: "test-network"}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
			Expect(err).To(MatchError(ContainSubstring("FabricDomain")))

			if !deleting {
				_, err = server.Delete(ctx, privatev1.FabricDomainsDeleteRequest_builder{Id: created.GetObject().GetId()}.Build())
				Expect(err).ToNot(HaveOccurred())
			}
			current, err := server.Get(ctx, privatev1.FabricDomainsGetRequest_builder{Id: created.GetObject().GetId()}.Build())
			Expect(err).ToNot(HaveOccurred())
			domain := current.GetObject()
			domain.GetMetadata().SetFinalizers(nil)
			_, err = server.generic.dao.Update().SetObject(domain).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			// Removing the finalizer archives the domain, releasing the VN dependency.
			_, err = vnServer.Delete(ctx, privatev1.VirtualNetworksDeleteRequest_builder{Id: "test-network"}.Build())
			Expect(err).ToNot(HaveOccurred())
		}, Entry("active domain", false), Entry("deleting domain", true))

		It("does not block an unrelated VN", func() {
			_, err := server.Create(ctx, privatev1.FabricDomainsCreateRequest_builder{Object: object}.Build())
			Expect(err).ToNot(HaveOccurred())
			_, err = server.virtualNetworkDao.Create().SetObject(privatev1.VirtualNetwork_builder{
				Id: "unrelated", Metadata: privatev1.Metadata_builder{Name: "unrelated", Tenant: testTenant}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = vnServer.Delete(ctx, privatev1.VirtualNetworksDeleteRequest_builder{Id: "unrelated"}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		DescribeTable("serializes domain creation with VN deletion across transactions", func(createFirst bool) {
			// Publish the fixture so both independent request transactions can see it.
			Expect(suiteTx.End(ctx)).To(Succeed())
			suiteTx = nil
			raceCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			firstTx, err := tm.Begin(raceCtx)
			Expect(err).ToNot(HaveOccurred())
			defer func() {
				if firstTx != nil {
					_ = firstTx.End(raceCtx)
				}
			}()
			firstCtx := database.TxIntoContext(raceCtx, firstTx)
			secondTx, err := tm.Begin(raceCtx)
			Expect(err).ToNot(HaveOccurred())
			defer func() { _ = secondTx.End(raceCtx) }()
			secondCtx := database.TxIntoContext(raceCtx, secondTx)
			var secondPID int
			Expect(secondTx.QueryRow(secondCtx, "select pg_backend_pid()").Scan(&secondPID)).To(Succeed())

			createDomain := func(callCtx context.Context) error {
				_, callErr := server.Create(callCtx, privatev1.FabricDomainsCreateRequest_builder{Object: object}.Build())
				return callErr
			}
			deleteVN := func(callCtx context.Context) error {
				_, callErr := vnServer.Delete(callCtx, privatev1.VirtualNetworksDeleteRequest_builder{Id: "test-network"}.Build())
				return callErr
			}
			first, second := createDomain, deleteVN
			if !createFirst {
				first, second = deleteVN, createDomain
			}
			Expect(first(firstCtx)).To(Succeed())
			result := make(chan error, 1)
			go func() { result <- second(secondCtx) }()
			// Observe the database lock directly instead of relying on a scheduling delay.
			Eventually(func() bool {
				var waiting bool
				queryErr := firstTx.QueryRow(firstCtx, "select cardinality(pg_blocking_pids($1)) > 0", secondPID).Scan(&waiting)
				return queryErr == nil && waiting
			}, 5*time.Second, 10*time.Millisecond).Should(BeTrue())
			Expect(firstTx.End(firstCtx)).To(Succeed())
			firstTx = nil
			var secondErr error
			Eventually(result, 5*time.Second).Should(Receive(&secondErr))
			Expect(grpcstatus.Code(secondErr)).To(Equal(grpccodes.FailedPrecondition))
		}, Entry("domain create wins", true), Entry("VN delete wins", false))
	})

})
