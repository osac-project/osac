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
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	"google.golang.org/protobuf/proto"
)

var _ = Describe("Private fabric domains server", func() {
	var server *PrivateFabricDomainsServer
	var object *privatev1.FabricDomain

	BeforeEach(func() {
		var err error
		server, err = NewPrivateFabricDomainsServer().SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		object = privatev1.FabricDomain_builder{
			Metadata: privatev1.Metadata_builder{Name: "test-domain", Project: "project-a"}.Build(),
			Spec: privatev1.FabricDomainSpec_builder{
				Type:           privatev1.FabricDomainType_FABRIC_DOMAIN_TYPE_ETHERNET_EW,
				VirtualNetwork: "test-network", Servers: []string{"server-a"},
			}.Build(),
		}.Build()
	})

	seedNetwork := func(ethernet bool) {
		seedProject("project-a")
		_, err := fixtureDAO[*privatev1.NetworkClass]().Create().SetObject(privatev1.NetworkClass_builder{
			Id:            "test-class",
			Metadata:      privatev1.Metadata_builder{Name: "test-class", Tenant: auth.SharedTenant}.Build(),
			Title:         "OSAC NetworkClass",
			FabricManager: "netris",
			Spec: privatev1.NetworkClassSpec_builder{
				EastWestCapabilities: privatev1.NetworkClassEastWestCapabilities_builder{
					SupportsEastWestEthernet: ethernet,
				}.Build(),
				AddressPlan: fixtureAddressPlan(),
			}.Build(),
			Status: privatev1.NetworkClassStatus_builder{
				State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
				Hub:   "hub-a",
			}.Build(),
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		_, err = fixtureDAO[*privatev1.VirtualNetwork]().Create().SetObject(privatev1.VirtualNetwork_builder{
			Id: "test-network",
			Metadata: privatev1.Metadata_builder{
				Name: "test-network", Tenant: testTenant, Project: "project-a", Finalizers: []string{"test/cleanup"},
			}.Build(),
			Spec: privatev1.VirtualNetworkSpec_builder{Ipv4Cidr: new("10.0.0.0/16")}.Build(),
			Status: privatev1.VirtualNetworkStatus_builder{
				State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
				Hub:   "hub-a",
			}.Build(),
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		_, err = fixtureDAO[*privatev1.BareMetalInstance]().Create().SetObject(privatev1.BareMetalInstance_builder{
			Id:       "fabric-domain-server-a",
			Metadata: privatev1.Metadata_builder{Name: "fabric-domain-server-a", Tenant: testTenant, Project: "project-a"}.Build(),
			Status: privatev1.BareMetalInstanceStatus_builder{
				ExternalHostName: "server-a",
			}.Build(),
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
	}

	It("creates an Ethernet domain", func() {
		seedNetwork(true)
		response, err := server.Create(ctx, privatev1.FabricDomainsCreateRequest_builder{Object: object}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetObject().GetSpec().GetServers()).To(Equal([]string{"server-a"}))
		Expect(response.GetObject().GetStatus().GetConditions()).To(HaveLen(1))
		Expect(response.GetObject().GetStatus().GetConditions()[0].GetType()).To(Equal(privatev1.FabricDomainConditionType_FABRIC_DOMAIN_CONDITION_TYPE_PROGRESSING))
	})

	Describe("BMI host validation", func() {
		BeforeEach(func() {
			seedNetwork(true)
		})

		It("rejects a server that is not a same-tenant BMI host", func() {
			const host = "unknown-host"
			object.GetSpec().SetServers([]string{host})
			_, err := server.Create(ctx, privatev1.FabricDomainsCreateRequest_builder{Object: object}.Build())
			Expect(err).To(MatchError(grpcstatus.Errorf(grpccodes.InvalidArgument,
				"FabricDomain server %s is not a bare-metal host of this project", host)))
			domains, err := fixtureDAO[*privatev1.FabricDomain]().List().Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(domains.GetItems()).To(BeEmpty())
		})

		It("does not accept a host owned by another tenant", func() {
			const host = "foreign-host"
			createTenant("other-tenant")
			_, err := fixtureDAO[*privatev1.BareMetalInstance]().Create().SetObject(
				privatev1.BareMetalInstance_builder{
					Id:       "foreign-bmi",
					Metadata: privatev1.Metadata_builder{Name: "foreign-bmi", Tenant: "other-tenant"}.Build(),
					Status: privatev1.BareMetalInstanceStatus_builder{
						ExternalHostName: host,
					}.Build(),
				}.Build(),
			).Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			object.GetSpec().SetServers([]string{host})
			_, err = server.Create(ctx, privatev1.FabricDomainsCreateRequest_builder{Object: object}.Build())
			Expect(err).To(MatchError(grpcstatus.Errorf(grpccodes.InvalidArgument,
				"FabricDomain server %s is not a bare-metal host of this project", host)))
		})

		It("does not accept a BMI that has started deletion", func() {
			bmiServer, err := NewPrivateBareMetalInstancesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).NotTo(HaveOccurred())
			_, err = bmiServer.Delete(ctx, privatev1.BareMetalInstancesDeleteRequest_builder{
				Id: "fabric-domain-server-a",
			}.Build())
			Expect(err).NotTo(HaveOccurred())

			_, err = server.Create(ctx, privatev1.FabricDomainsCreateRequest_builder{Object: object}.Build())
			Expect(err).To(MatchError(grpcstatus.Errorf(grpccodes.InvalidArgument,
				"FabricDomain server server-a is not a bare-metal host of this project")))
		})

		It("does not accept a host from another project in the same tenant", func() {
			seedProject("project-b")
			const host = "other-project-host"
			_, err := fixtureDAO[*privatev1.BareMetalInstance]().Create().SetObject(
				privatev1.BareMetalInstance_builder{
					Id: "other-project-bmi",
					Metadata: privatev1.Metadata_builder{
						Name: "other-project-bmi", Tenant: testTenant, Project: "project-b",
					}.Build(),
					Status: privatev1.BareMetalInstanceStatus_builder{
						ExternalHostName: host,
					}.Build(),
				}.Build(),
			).Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			object.GetSpec().SetServers([]string{host})
			_, err = server.Create(ctx, privatev1.FabricDomainsCreateRequest_builder{Object: object}.Build())
			Expect(err).To(MatchError(grpcstatus.Errorf(grpccodes.InvalidArgument,
				"FabricDomain server %s is not a bare-metal host of this project", host)))
		})

		It("rejects a Cluster worker host with the design message", func() {
			const host = "worker-host"
			_, err := fixtureDAO[*privatev1.Cluster]().Create().SetObject(privatev1.Cluster_builder{
				Id: "worker-cluster",
				Metadata: privatev1.Metadata_builder{
					Name: "worker-cluster", Tenant: testTenant, Project: "project-a",
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			_, err = fixtureDAO[*privatev1.BareMetalInstance]().Create().SetObject(
				privatev1.BareMetalInstance_builder{
					Id: "worker-bmi",
					Metadata: privatev1.Metadata_builder{
						Name: "worker-bmi", Tenant: testTenant, Project: "project-a",
					}.Build(),
					Status: privatev1.BareMetalInstanceStatus_builder{
						ExternalHostName: host,
						Cluster:          privatev1.ClusterLocalReference_builder{Id: "worker-cluster"}.Build(),
						NodeSet:          new("compute"),
					}.Build(),
				}.Build(),
			).Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			object.GetSpec().SetServers([]string{host})
			_, err = server.Create(ctx, privatev1.FabricDomainsCreateRequest_builder{Object: object}.Build())
			Expect(err).To(MatchError(grpcstatus.Errorf(grpccodes.InvalidArgument,
				"FabricDomain server %s is a Cluster worker", host)))
			domains, err := fixtureDAO[*privatev1.FabricDomain]().List().Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(domains.GetItems()).To(BeEmpty())
		})
	})

	Describe("BMI deletion guard", func() {
		var bmiServer *PrivateBareMetalInstancesServer

		BeforeEach(func() {
			var err error
			bmiServer, err = NewPrivateBareMetalInstancesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).NotTo(HaveOccurred())
			seedNetwork(true)
		})

		It("blocks deletion while an active FabricDomain references the assigned host", func() {
			created, err := server.Create(ctx, privatev1.FabricDomainsCreateRequest_builder{Object: object}.Build())
			Expect(err).NotTo(HaveOccurred())
			_, err = bmiServer.Delete(ctx, privatev1.BareMetalInstancesDeleteRequest_builder{
				Id: "fabric-domain-server-a",
			}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
			Expect(err).To(MatchError(ContainSubstring(created.GetObject().GetId())))
			bmi, err := fixtureDAO[*privatev1.BareMetalInstance]().Get().SetId("fabric-domain-server-a").Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(bmi.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeFalse())
			Expect(bmi.GetObject().GetStatus().GetState()).NotTo(
				Equal(privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_DELETING),
			)
		})

		It("accepts deletion before host projection so the reconciler can check the live CR", func() {
			_, err := fixtureDAO[*privatev1.FabricDomain]().Create().SetObject(privatev1.FabricDomain_builder{
				Id:       "race-host-domain",
				Metadata: privatev1.Metadata_builder{Name: "race-host-domain", Tenant: testTenant}.Build(),
				Spec:     privatev1.FabricDomainSpec_builder{Servers: []string{"server-a"}}.Build(),
			}.Build()).Do(ctx)
			Expect(err).NotTo(HaveOccurred())

			bmiDAO := fixtureDAO[*privatev1.BareMetalInstance]()
			bmi, err := bmiDAO.Get().SetId("fabric-domain-server-a").Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			bmi.GetObject().GetStatus().SetExternalHostName("")
			bmi.GetObject().GetMetadata().SetFinalizers([]string{"test/finalizer"})
			_, err = bmiDAO.Update().SetObject(bmi.GetObject()).Do(ctx)
			Expect(err).NotTo(HaveOccurred())

			_, err = bmiServer.Delete(ctx, privatev1.BareMetalInstancesDeleteRequest_builder{
				Id: "fabric-domain-server-a",
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			bmi, err = bmiDAO.Get().SetId("fabric-domain-server-a").Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(bmi.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeTrue())
		})

		It("allows deletion with no projected host so the controller can cancel allocation", func() {
			bmiDAO := fixtureDAO[*privatev1.BareMetalInstance]()
			bmi, err := bmiDAO.Get().SetId("fabric-domain-server-a").Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			bmi.GetObject().GetStatus().SetExternalHostName("")
			_, err = bmiDAO.Update().SetObject(bmi.GetObject()).Do(ctx)
			Expect(err).NotTo(HaveOccurred())

			_, err = bmiServer.Delete(ctx, privatev1.BareMetalInstancesDeleteRequest_builder{
				Id: "fabric-domain-server-a",
			}.Build())
			Expect(err).NotTo(HaveOccurred())
		})

		It("keeps a deleting FabricDomain as a dependency until its record is removed", func() {
			object.GetMetadata().SetFinalizers([]string{"test/finalizer"})
			created, err := server.Create(ctx, privatev1.FabricDomainsCreateRequest_builder{Object: object}.Build())
			Expect(err).NotTo(HaveOccurred())
			_, err = server.Delete(ctx, privatev1.FabricDomainsDeleteRequest_builder{
				Id: created.GetObject().GetId(),
			}.Build())
			Expect(err).NotTo(HaveOccurred())

			_, err = bmiServer.Delete(ctx, privatev1.BareMetalInstancesDeleteRequest_builder{
				Id: "fabric-domain-server-a",
			}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
			Expect(err).To(MatchError(ContainSubstring(created.GetObject().GetId())))

			domain, err := fixtureDAO[*privatev1.FabricDomain]().Get().SetId(created.GetObject().GetId()).Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			domain.GetObject().GetMetadata().SetFinalizers(nil)
			_, err = fixtureDAO[*privatev1.FabricDomain]().Update().SetObject(domain.GetObject()).Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			_, err = bmiServer.Delete(ctx, privatev1.BareMetalInstancesDeleteRequest_builder{
				Id: "fabric-domain-server-a",
			}.Build())
			Expect(err).NotTo(HaveOccurred())
		})

		It("ignores references from another tenant", func() {
			createTenant("other-tenant")
			_, err := fixtureDAO[*privatev1.FabricDomain]().Create().SetObject(
				privatev1.FabricDomain_builder{
					Id: "foreign-domain",
					Metadata: privatev1.Metadata_builder{
						Name: "foreign-domain", Tenant: "other-tenant", Project: "project-b",
					}.Build(),
					Spec: privatev1.FabricDomainSpec_builder{Servers: []string{"server-a"}}.Build(),
				}.Build(),
			).Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			_, err = bmiServer.Delete(ctx, privatev1.BareMetalInstancesDeleteRequest_builder{
				Id: "fabric-domain-server-a",
			}.Build())
			Expect(err).NotTo(HaveOccurred())
		})

		It("ignores references from another project in the same tenant", func() {
			seedProject("project-b")
			_, err := fixtureDAO[*privatev1.FabricDomain]().Create().SetObject(
				privatev1.FabricDomain_builder{
					Id: "other-project-domain",
					Metadata: privatev1.Metadata_builder{
						Name: "other-project-domain", Tenant: testTenant, Project: "project-b",
					}.Build(),
					Spec: privatev1.FabricDomainSpec_builder{Servers: []string{"server-a"}}.Build(),
				}.Build(),
			).Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			_, err = bmiServer.Delete(ctx, privatev1.BareMetalInstancesDeleteRequest_builder{
				Id: "fabric-domain-server-a",
			}.Build())
			Expect(err).NotTo(HaveOccurred())
		})

		It("returns success when deletion was already accepted", func() {
			bmiDAO := fixtureDAO[*privatev1.BareMetalInstance]()
			bmi, err := bmiDAO.Get().SetId("fabric-domain-server-a").Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			bmi.GetObject().GetMetadata().SetFinalizers([]string{"test/finalizer"})
			_, err = bmiDAO.Update().SetObject(bmi.GetObject()).Do(ctx)
			Expect(err).NotTo(HaveOccurred())

			_, err = bmiServer.Delete(ctx, privatev1.BareMetalInstancesDeleteRequest_builder{
				Id: "fabric-domain-server-a",
			}.Build())
			Expect(err).NotTo(HaveOccurred())

			// Simulate the reconciler projecting the live host after delete was accepted.
			bmi, err = bmiDAO.Get().SetId("fabric-domain-server-a").Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(bmi.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeTrue())
			bmi.GetObject().GetStatus().SetExternalHostName("server-a")
			_, err = bmiDAO.Update().SetObject(bmi.GetObject()).Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			_, err = fixtureDAO[*privatev1.FabricDomain]().Create().SetObject(
				privatev1.FabricDomain_builder{
					Id: "late-domain",
					Metadata: privatev1.Metadata_builder{
						Name: "late-domain", Tenant: testTenant, Project: "project-a",
					}.Build(),
					Spec: privatev1.FabricDomainSpec_builder{Servers: []string{"server-a"}}.Build(),
				}.Build(),
			).Do(ctx)
			Expect(err).NotTo(HaveOccurred())

			_, err = bmiServer.Delete(ctx, privatev1.BareMetalInstancesDeleteRequest_builder{
				Id: "fabric-domain-server-a",
			}.Build())
			Expect(err).NotTo(HaveOccurred())
		})
	})

	It("requires the NetworkClass Ethernet capability", func() {
		seedNetwork(false)
		_, err := server.Create(ctx, privatev1.FabricDomainsCreateRequest_builder{Object: object}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err).To(MatchError(ContainSubstring("supports_east_west_ethernet")))
	})

	It("requires BMaaS", func() {
		seedNetwork(true)
		disabled, err := NewPrivateFabricDomainsServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			SetNetworkingSettings(NetworkingSettings{ProvisioningEnabled: true}).
			Build()
		Expect(err).ToNot(HaveOccurred())
		_, err = disabled.Create(ctx, privatev1.FabricDomainsCreateRequest_builder{Object: object}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
		Expect(err).To(MatchError(ContainSubstring("BMaaS")))
	})

	DescribeTable("rejects unsupported fabric types", func(kind privatev1.FabricDomainType) {
		object.GetSpec().SetType(kind)
		_, err := server.Create(ctx, privatev1.FabricDomainsCreateRequest_builder{Object: object}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.Unimplemented))
	},
		Entry("InfiniBand", privatev1.FabricDomainType_FABRIC_DOMAIN_TYPE_INFINIBAND_EW),
		Entry("NVLink", privatev1.FabricDomainType_FABRIC_DOMAIN_TYPE_NVLINK),
	)

	It("rejects unknown fabric domain types", func() {
		object.GetSpec().SetType(privatev1.FabricDomainType(999))
		_, err := server.Create(ctx, privatev1.FabricDomainsCreateRequest_builder{Object: object}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
	})

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

	It("rejects a servers change", func() {
		seedNetwork(true)
		created, err := server.Create(ctx, privatev1.FabricDomainsCreateRequest_builder{Object: object}.Build())
		Expect(err).ToNot(HaveOccurred())
		_, err = server.Update(ctx, privatev1.FabricDomainsUpdateRequest_builder{
			Object: privatev1.FabricDomain_builder{
				Id:   created.GetObject().GetId(),
				Spec: privatev1.FabricDomainSpec_builder{Servers: []string{"server-a", "server-b"}}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.servers"}},
		}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err).To(MatchError(ContainSubstring("FabricDomain servers cannot change in this release")))
	})

	DescribeTable("retains immutable and nonempty update validation", func(path string) {
		seedNetwork(true)
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
		seedNetwork(true)
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
				"status.backend_id", "status.members", "status.conditions",
			}},
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(updated.GetObject().GetStatus().GetBackendId()).To(Equal("cluster-42"))
		Expect(updated.GetObject().GetStatus().GetMembers()).To(HaveLen(1))
		Expect(updated.GetObject().GetStatus().GetConditions()).To(HaveLen(1))

		operatorCtx := auth.ContextWithSubject(ctx, &auth.Subject{User: "osac-operator"})
		operatorCtx = auth.ContextWithAuthenticationMethod(operatorCtx, "serviceaccount")
		updated, err = server.Update(operatorCtx, privatev1.FabricDomainsUpdateRequest_builder{
			Object: privatev1.FabricDomain_builder{
				Id:     created.GetObject().GetId(),
				Status: privatev1.FabricDomainStatus_builder{Hub: "hub-b"}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.hub"}},
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(updated.GetObject().GetStatus().GetHub()).To(Equal("hub-b"))

		jwtOperatorCtx := auth.ContextWithSubject(ctx, &auth.Subject{User: "osac-operator"})
		_, err = server.Update(jwtOperatorCtx, privatev1.FabricDomainsUpdateRequest_builder{
			Object:     privatev1.FabricDomain_builder{Id: created.GetObject().GetId()}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.hub"}},
		}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))

		_, err = server.Update(controllerCtx, privatev1.FabricDomainsUpdateRequest_builder{
			Object:     privatev1.FabricDomain_builder{Id: created.GetObject().GetId()}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status"}},
		}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
	})

	DescribeTable("serializes FabricDomain creation and BMI deletion", func(createFirst bool) {
		seedNetwork(true)
		bmiServer, err := NewPrivateBareMetalInstancesServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).NotTo(HaveOccurred())

		// Publish fixtures so the independent request transactions can observe them.
		Expect(suiteTx.End(ctx)).To(Succeed())
		suiteTx = nil
		raceCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		firstTx, err := tm.Begin(raceCtx)
		Expect(err).NotTo(HaveOccurred())
		defer func() {
			if firstTx != nil {
				Expect(firstTx.End(context.Background())).To(Succeed())
			}
		}()
		firstCtx := database.TxIntoContext(raceCtx, firstTx)
		secondTx, err := tm.Begin(raceCtx)
		Expect(err).NotTo(HaveOccurred())
		defer func() { Expect(secondTx.End(context.Background())).To(Succeed()) }()
		secondCtx := database.TxIntoContext(raceCtx, secondTx)
		var secondPID int
		Expect(secondTx.QueryRow(secondCtx, "select pg_backend_pid()").Scan(&secondPID)).To(Succeed())

		createDomain := func(callCtx context.Context) error {
			_, callErr := server.Create(callCtx, privatev1.FabricDomainsCreateRequest_builder{Object: object}.Build())
			return callErr
		}
		deleteBMI := func(callCtx context.Context) error {
			_, callErr := bmiServer.Delete(callCtx, privatev1.BareMetalInstancesDeleteRequest_builder{
				Id: "fabric-domain-server-a",
			}.Build())
			return callErr
		}
		first, second := createDomain, deleteBMI
		if !createFirst {
			first, second = deleteBMI, createDomain
		}
		Expect(first(firstCtx)).To(Succeed())
		result := make(chan error, 1)
		go func() { result <- second(secondCtx) }()
		Eventually(func() bool {
			var waiting bool
			queryErr := firstTx.QueryRow(firstCtx,
				"select cardinality(pg_blocking_pids($1)) > 0", secondPID).Scan(&waiting)
			return queryErr == nil && waiting
		}, 5*time.Second, 10*time.Millisecond).Should(BeTrue())
		Expect(firstTx.End(firstCtx)).To(Succeed())
		firstTx = nil
		var secondErr error
		Eventually(result, 5*time.Second).Should(Receive(&secondErr))
		if createFirst {
			Expect(grpcstatus.Code(secondErr)).To(Equal(grpccodes.FailedPrecondition))
		} else {
			Expect(grpcstatus.Code(secondErr)).To(Equal(grpccodes.InvalidArgument))
		}
	},
		Entry("domain create wins", true),
		Entry("BMI delete wins", false),
	)

	Describe("VirtualNetwork lifecycle", func() {
		var vnServer *PrivateVirtualNetworksServer
		BeforeEach(func() {
			var err error
			vnServer, err = NewPrivateVirtualNetworksServer().SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			seedNetwork(true)
		})

		It("rejects a cross-tenant VN even with administrator visibility", func() {
			object.GetMetadata().SetTenant("another-tenant")
			_, err := server.Create(ctx, privatev1.FabricDomainsCreateRequest_builder{Object: object}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(err).To(MatchError(ContainSubstring("does not exist")))
		})

		It("keeps a deleting domain unchanged on a repeated delete", func() {
			object.GetMetadata().SetFinalizers([]string{"test/cleanup"})
			created, err := server.Create(ctx, privatev1.FabricDomainsCreateRequest_builder{Object: object}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := created.GetObject().GetId()

			first, err := server.Delete(ctx, privatev1.FabricDomainsDeleteRequest_builder{Id: id}.Build())
			Expect(err).ToNot(HaveOccurred())
			before, err := server.Get(ctx, privatev1.FabricDomainsGetRequest_builder{Id: id}.Build())
			Expect(err).ToNot(HaveOccurred())

			second, err := server.Delete(ctx, privatev1.FabricDomainsDeleteRequest_builder{Id: id}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(second.GetObject().GetId()).To(Equal(id))
			Expect(proto.Equal(first.GetObject(), second.GetObject())).To(BeTrue())

			after, err := server.Get(ctx, privatev1.FabricDomainsGetRequest_builder{Id: id}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(before.GetObject(), after.GetObject())).To(BeTrue())
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
			_, err = fixtureDAO[*privatev1.VirtualNetwork]().Create().SetObject(privatev1.VirtualNetwork_builder{
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
					Expect(firstTx.End(context.Background())).To(Succeed())
				}
			}()
			firstCtx := database.TxIntoContext(raceCtx, firstTx)
			secondTx, err := tm.Begin(raceCtx)
			Expect(err).ToNot(HaveOccurred())
			defer func() { Expect(secondTx.End(context.Background())).To(Succeed()) }()
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
