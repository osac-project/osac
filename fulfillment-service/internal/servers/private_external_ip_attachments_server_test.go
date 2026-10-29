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
	"fmt"
	"time"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func createExternalIPInState(
	ctx context.Context,
	externalIPDao *dao.GenericDAO[*privatev1.ExternalIP],
	poolID string,
	state privatev1.ExternalIPState,
	attached bool,
	hubIDs ...string,
) *privatev1.ExternalIP {
	hubID := "network-hub-a"
	if len(hubIDs) > 0 {
		hubID = hubIDs[0]
	}
	resp, err := externalIPDao.Create().SetObject(
		privatev1.ExternalIP_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: testTenant,
				Name:   fmt.Sprintf("test-%s", uuid.NewString()[:8]),
			}.Build(),
			Spec: privatev1.ExternalIPSpec_builder{
				Pool: privatev1.ExternalIPPoolReference_builder{Id: poolID}.Build(),
			}.Build(),
			Status: privatev1.ExternalIPStatus_builder{
				State:    state,
				Address:  "203.0.113.1",
				Attached: attached,
				Hub:      hubID,
			}.Build(),
		}.Build(),
	).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return resp.GetObject()
}

func createClusterInState(
	ctx context.Context,
	clusterDao *dao.GenericDAO[*privatev1.Cluster],
) *privatev1.Cluster {
	resp, err := clusterDao.Create().SetObject(
		privatev1.Cluster_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: testTenant,
				Name:   fmt.Sprintf("test-%s", uuid.NewString()[:8]),
			}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				Template: privatev1.ClusterTemplateReference_builder{Id: "ocp_small"}.Build(),
			}.Build(),
			Status: privatev1.ClusterStatus_builder{
				State:           privatev1.ClusterState_CLUSTER_STATE_READY,
				Hub:             "network-hub-a",
				ApiEndpoint:     "10.0.0.20",
				IngressEndpoint: "10.0.0.21",
			}.Build(),
		}.Build(),
	).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return resp.GetObject()
}

func createBareMetalInstanceInState(
	ctx context.Context,
	bareMetalInstanceDao *dao.GenericDAO[*privatev1.BareMetalInstance],
) *privatev1.BareMetalInstance {
	resp, err := bareMetalInstanceDao.Create().SetObject(
		privatev1.BareMetalInstance_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: testTenant,
				Name:   fmt.Sprintf("test-%s", uuid.NewString()[:8]),
			}.Build(),
			Spec: privatev1.BareMetalInstanceSpec_builder{
				CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: "bcm_h100"}.Build(),
			}.Build(),
			Status: privatev1.BareMetalInstanceStatus_builder{
				State: privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_RUNNING,
				Hub:   "network-hub-a",
				Conditions: []*privatev1.BareMetalInstanceCondition{privatev1.BareMetalInstanceCondition_builder{
					Type:   privatev1.BareMetalInstanceConditionType_BARE_METAL_INSTANCE_CONDITION_TYPE_READY,
					Status: privatev1.ConditionStatus_CONDITION_STATUS_TRUE,
				}.Build()},
				NetworkAttachmentStatuses: []*privatev1.BareMetalNetworkAttachmentStatus{privatev1.BareMetalNetworkAttachmentStatus_builder{
					IpAddress: "10.0.0.30",
					Primary:   true,
				}.Build()},
			}.Build(),
		}.Build(),
	).Do(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return resp.GetObject()
}

var _ = Describe("Private external IP attachments server", func() {
	var (
		server               *PrivateExternalIPAttachmentsServer
		externalIPPoolDao    *dao.GenericDAO[*privatev1.ExternalIPPool]
		externalIPDao        *dao.GenericDAO[*privatev1.ExternalIP]
		computeInstanceDao   *dao.GenericDAO[*privatev1.ComputeInstance]
		clusterDao           *dao.GenericDAO[*privatev1.Cluster]
		bareMetalInstanceDao *dao.GenericDAO[*privatev1.BareMetalInstance]
		sharedPool           *privatev1.ExternalIPPool
	)

	BeforeEach(func() {
		var err error

		networkClassesDao, err := dao.NewGenericDAO[*privatev1.NetworkClass]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		_, err = networkClassesDao.Create().SetObject(privatev1.NetworkClass_builder{
			Metadata: privatev1.Metadata_builder{Tenant: testTenant, Name: "test-network-class"}.Build(),
			Status: privatev1.NetworkClassStatus_builder{
				State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
				Hub:   "network-hub-a",
			}.Build(),
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		externalIPPoolDao, err = dao.NewGenericDAO[*privatev1.ExternalIPPool]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		externalIPDao, err = dao.NewGenericDAO[*privatev1.ExternalIP]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		computeInstanceDao, err = dao.NewGenericDAO[*privatev1.ComputeInstance]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		clusterDao, err = dao.NewGenericDAO[*privatev1.Cluster]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		bareMetalInstanceDao, err = dao.NewGenericDAO[*privatev1.BareMetalInstance]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		poolResp, err := externalIPPoolDao.Create().SetObject(
			privatev1.ExternalIPPool_builder{
				Metadata: privatev1.Metadata_builder{
					Tenant: auth.SharedTenant,
				}.Build(),
				Spec: privatev1.ExternalIPPoolSpec_builder{
					Cidrs: []string{"203.0.113.0/24"},
				}.Build(),
				Status: privatev1.ExternalIPPoolStatus_builder{
					State:     privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY,
					Total:     100,
					Allocated: 0,
					Available: 100,
				}.Build(),
			}.Build(),
		).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		sharedPool = poolResp.GetObject()

		server, err = NewPrivateExternalIPAttachmentsServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("Creation", func() {
		It("Can be built if all required parameters are set", func() {
			s, err := NewPrivateExternalIPAttachmentsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(s).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			s, err := NewPrivateExternalIPAttachmentsServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(s).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			s, err := NewPrivateExternalIPAttachmentsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(s).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		It("resolves attachment uniqueness canonically and preserves the exact child exclusion", func() {
			tenantsDAO, err := dao.NewGenericDAO[*privatev1.Tenant]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = tenantsDAO.Create().SetObject(privatev1.Tenant_builder{Id: "attachment-tenant", Metadata: privatev1.Metadata_builder{Name: "attachment-tenant", Tenant: "attachment-tenant"}.Build()}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			makeEIP := func(tenant string) *privatev1.ExternalIP {
				response, createErr := externalIPDao.Create().SetObject(privatev1.ExternalIP_builder{
					Metadata: privatev1.Metadata_builder{Name: "shared-display-name", Tenant: tenant, Creator: systemCreator}.Build(),
					Spec:     privatev1.ExternalIPSpec_builder{Pool: privatev1.ExternalIPPoolReference_builder{Id: sharedPool.GetId()}.Build()}.Build(),
					Status:   privatev1.ExternalIPStatus_builder{State: privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, Address: "203.0.113.1", Hub: "network-hub-a"}.Build(),
				}.Build()).Do(ctx)
				Expect(createErr).ToNot(HaveOccurred())
				return response.GetObject()
			}
			first, second := makeEIP(testTenant), makeEIP("attachment-tenant")
			ci := createComputeInstanceInState(ctx, computeInstanceDao, privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)
			created, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{Object: privatev1.ExternalIPAttachment_builder{
				Metadata: privatev1.Metadata_builder{Name: "canonical-attachment", Tenant: testTenant}.Build(),
				Spec:     privatev1.ExternalIPAttachmentSpec_builder{ExternalIp: privatev1.ExternalIPLocalReference_builder{Id: first.GetId(), Name: "shared-display-name"}.Build(), ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: ci.GetId()}.Build()}.Build(),
			}.Build()}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(server.validateUniqueness(ctx, second.GetId(), "unused-target", "shared-display-name")).To(Succeed())
			Expect(grpcstatus.Code(server.validateUniqueness(ctx, first.GetId(), ci.GetId(), "misleading-name"))).To(Equal(grpccodes.AlreadyExists))
			Expect(server.lifecycle.ensureExternalIPAvailableExcept(ctx, first.GetId(), "ignored-name", created.GetObject().GetId())).To(Succeed())
			for _, parent := range []*privatev1.ExternalIP{first, second} {
				parent.GetMetadata().SetLabels(map[string]string{autoCreatedLabel: "true", autoCreatedForLabel: ci.GetId()})
				parent.GetMetadata().SetFinalizers([]string{"test-hold"})
				_, err = externalIPDao.Update().SetObject(parent).Do(ctx)
				Expect(err).ToNot(HaveOccurred())
			}
			Expect(server.lifecycle.deleteAutoCreatedExternalIPs(ctx, ci.GetId())).To(Succeed())
			for _, parent := range []*privatev1.ExternalIP{first, second} {
				fresh, getErr := externalIPDao.Get().SetId(parent.GetId()).Do(ctx)
				Expect(getErr).ToNot(HaveOccurred())
				Expect(fresh.GetObject().GetMetadata().HasDeletionTimestamp()).To(Equal(parent.GetId() == second.GetId()))
			}
			Expect(grpcstatus.Code(server.lifecycle.ensureExternalIPAvailableExcept(ctx, first.GetId(), "ignored-name", "unrelated-child"))).To(Equal(grpccodes.FailedPrecondition))
			_, err = suiteTx.Exec(ctx, "update external_ip_attachments set deletion_timestamp=now() where id=$1", created.GetObject().GetId())
			Expect(err).ToNot(HaveOccurred())
			Expect(grpcstatus.Code(server.lifecycle.ensureExternalIPAvailable(ctx, first.GetId(), "ignored-name"))).To(Equal(grpccodes.FailedPrecondition))
			Expect(server.lifecycle.ensureExternalIPAvailableExcept(ctx, first.GetId(), "ignored-name", created.GetObject().GetId())).To(Succeed())
		})

		DescribeTable("scopes attachment candidate capacity before collecting foreign owners", func(foreignProjects bool) {
			tenants, err := dao.NewGenericDAO[*privatev1.Tenant]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			projects, err := dao.NewGenericDAO[*privatev1.Project]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			pool, err := externalIPPoolDao.Create().SetObject(privatev1.ExternalIPPool_builder{
				Metadata: privatev1.Metadata_builder{Tenant: auth.SharedTenant, Name: "attachment-capacity-pool"}.Build(),
				Spec:     privatev1.ExternalIPPoolSpec_builder{Cidrs: []string{"198.18.0.0/15"}}.Build(),
				Status:   privatev1.ExternalIPPoolStatus_builder{State: privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY, Total: 2000, Available: 2000}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			createdParent, err := externalIPDao.Create().SetObject(privatev1.ExternalIP_builder{
				Metadata: privatev1.Metadata_builder{Tenant: testTenant, Name: "attachment-default"}.Build(),
				Spec:     privatev1.ExternalIPSpec_builder{Pool: privatev1.ExternalIPPoolReference_builder{Id: pool.GetObject().GetId()}.Build()}.Build(),
				Status:   privatev1.ExternalIPStatus_builder{State: privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, Address: "198.19.0.1", Hub: "network-hub-a"}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			parent := createdParent.GetObject()
			for ordinal := range 1001 {
				tenant, project := testTenant, ""
				if foreignProjects {
					project = fmt.Sprintf("attachment-project-%d", ordinal)
					_, err = projects.Create().SetObject(privatev1.Project_builder{
						Metadata: privatev1.Metadata_builder{Tenant: tenant, Name: project}.Build(),
					}.Build()).Do(ctx)
				} else {
					tenant = fmt.Sprintf("attachment-tenant-%d", ordinal)
					_, err = tenants.Create().SetObject(privatev1.Tenant_builder{
						Id: tenant, Metadata: privatev1.Metadata_builder{Tenant: tenant, Name: tenant}.Build(),
					}.Build()).Do(ctx)
				}
				Expect(err).ToNot(HaveOccurred())
				foreign, createErr := externalIPDao.Create().SetObject(privatev1.ExternalIP_builder{
					Metadata: privatev1.Metadata_builder{Tenant: tenant, Project: project, Name: parent.GetMetadata().GetName()}.Build(),
					Spec:     privatev1.ExternalIPSpec_builder{Pool: privatev1.ExternalIPPoolReference_builder{Id: pool.GetObject().GetId()}.Build()}.Build(),
					Status:   privatev1.ExternalIPStatus_builder{State: privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, Address: fmt.Sprintf("198.18.%d.%d", ordinal/256, ordinal%256), Hub: "network-hub-a"}.Build(),
				}.Build()).Do(ctx)
				Expect(createErr).ToNot(HaveOccurred())
				compute, createErr := computeInstanceDao.Create().SetObject(privatev1.ComputeInstance_builder{
					Metadata: privatev1.Metadata_builder{Tenant: tenant, Project: project, Name: "attachment-target"}.Build(),
					Spec:     privatev1.ComputeInstanceSpec_builder{Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "general.small"}.Build()}.Build(),
					Status:   privatev1.ComputeInstanceStatus_builder{State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, Hub: "network-hub-a"}.Build(),
				}.Build()).Do(ctx)
				Expect(createErr).ToNot(HaveOccurred())
				_, err = server.lifecycle.externalIPAttachmentDao.Create().SetObject(privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{Tenant: tenant, Project: project, Name: "attachment-consumer"}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: foreign.GetObject().GetId(), Name: parent.GetMetadata().GetName()}.Build(),
						ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: compute.GetObject().GetId()}.Build(),
					}.Build(),
				}.Build()).Do(ctx)
				Expect(err).ToNot(HaveOccurred())
			}
			population, err := server.lifecycle.externalIPAttachmentDao.List().SetLimit(1).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(population.GetTotal()).To(Equal(int32(1001)))
			Expect(server.lifecycle.ensureExternalIPAvailable(ctx, parent.GetId(), "")).To(Succeed())
			compute := createComputeInstanceInState(ctx, computeInstanceDao, privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)
			created, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{Object: privatev1.ExternalIPAttachment_builder{
				Metadata: privatev1.Metadata_builder{Tenant: testTenant, Name: "target-consumer"}.Build(),
				Spec: privatev1.ExternalIPAttachmentSpec_builder{
					ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: parent.GetId()}.Build(),
					ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: compute.GetId()}.Build(),
				}.Build(),
			}.Build()}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(grpcstatus.Code(server.lifecycle.ensureExternalIPAvailable(ctx, parent.GetId(), ""))).To(Equal(grpccodes.FailedPrecondition))
			Expect(server.lifecycle.ensureExternalIPAvailableExcept(ctx, parent.GetId(), "", created.GetObject().GetId())).To(Succeed())
			Expect(grpcstatus.Code(server.lifecycle.ensureExternalIPAvailableExcept(ctx, parent.GetId(), "", "different-child"))).To(Equal(grpccodes.FailedPrecondition))
		}, Entry("foreign tenants with empty projects", false), Entry("foreign projects in the same tenant", true))

		It("rejects ownerless DAO attachments and stores nonnullable owner columns", func() {
			_, err := server.lifecycle.externalIPAttachmentDao.Create().SetObject(privatev1.ExternalIPAttachment_builder{
				Metadata: privatev1.Metadata_builder{Name: "ownerless-attachment"}.Build(),
			}.Build()).Do(ctx)
			Expect(err).To(MatchError("cannot create object with empty tenant"))
			for _, table := range []string{"external_ip_attachments", "nat_gateways"} {
				var columns int
				Expect(suiteTx.QueryRow(ctx, `select count(*) from information_schema.columns where table_schema='public' and table_name=$1 and column_name in ('tenant','project') and is_nullable='NO'`, table).Scan(&columns)).To(Succeed())
				Expect(columns).To(Equal(2))
				var ownerConstraints int
				Expect(suiteTx.QueryRow(ctx, `select count(*) from pg_constraint where conrelid=$1::regclass and contype='f' and confrelid in ('tenants'::regclass,'projects'::regclass)`, table).Scan(&ownerConstraints)).To(Succeed())
				Expect(ownerConstraints).To(Equal(2))
			}
		})

		DescribeTable("checks migrated attachment owner projection and reference admissibility", func(tenant, project, referenceKind string) {
			if project != "" {
				projects, err := dao.NewGenericDAO[*privatev1.Project]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
				Expect(err).ToNot(HaveOccurred())
				_, err = projects.Create().SetObject(privatev1.Project_builder{Metadata: privatev1.Metadata_builder{Tenant: tenant, Name: project}.Build()}.Build()).Do(ctx)
				Expect(err).ToNot(HaveOccurred())
			}
			parent, err := externalIPDao.Create().SetObject(privatev1.ExternalIP_builder{
				Metadata: privatev1.Metadata_builder{Tenant: tenant, Project: project, Name: "legacy-attachment-parent"}.Build(),
				Spec:     privatev1.ExternalIPSpec_builder{Pool: privatev1.ExternalIPPoolReference_builder{Id: sharedPool.GetId()}.Build()}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			var reference *privatev1.ExternalIPLocalReference
			switch referenceKind {
			case "canonical":
				reference = privatev1.ExternalIPLocalReference_builder{Id: parent.GetObject().GetId(), Name: "misleading-name"}.Build()
			case "name":
				reference = privatev1.ExternalIPLocalReference_builder{Name: parent.GetObject().GetMetadata().GetName()}.Build()
			case "id-alias":
				reference = privatev1.ExternalIPLocalReference_builder{Id: parent.GetObject().GetMetadata().GetName()}.Build()
			case "empty":
				reference = privatev1.ExternalIPLocalReference_builder{}.Build()
			}
			compute := createComputeInstanceInState(ctx, computeInstanceDao, privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)
			created, err := server.lifecycle.externalIPAttachmentDao.Create().SetObject(privatev1.ExternalIPAttachment_builder{
				Metadata: privatev1.Metadata_builder{Tenant: tenant, Project: project, Name: "legacy-attachment"}.Build(),
				Spec: privatev1.ExternalIPAttachmentSpec_builder{
					ExternalIp: reference, ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: compute.GetId()}.Build(),
				}.Build(),
			}.Build()).Do(ctx)
			if referenceKind != "canonical" {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("does not exist or has been deleted"))
				return
			}
			Expect(err).ToNot(HaveOccurred())
			stored, err := server.lifecycle.externalIPAttachmentDao.Get().SetId(created.GetObject().GetId()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(stored.GetObject().GetMetadata().GetTenant()).To(Equal(tenant))
			Expect(stored.GetObject().GetMetadata().GetProject()).To(Equal(project))
			matches, err := server.lifecycle.externalIPAttachments(ctx, parent.GetObject())
			Expect(err).ToNot(HaveOccurred())
			Expect(matches).To(HaveLen(1))
			Expect(matches[0].GetId()).To(Equal(created.GetObject().GetId()))
		}, Entry("default project canonical", testTenant, "", "canonical"),
			Entry("shared owner canonical", auth.SharedTenant, "", "canonical"),
			Entry("nonempty project canonical", testTenant, "legacy-project", "canonical"),
			Entry("default project name-only", testTenant, "", "name"),
			Entry("default project ID alias", testTenant, "", "id-alias"),
			Entry("shared owner name-only", auth.SharedTenant, "", "name"),
			Entry("nonempty project name-only", testTenant, "legacy-project", "name"),
			Entry("empty imported reference", testTenant, "", "empty"))

		DescribeTable("keeps imported cross-owner canonical attachment blockers", func(terminating bool) {
			tenants, err := dao.NewGenericDAO[*privatev1.Tenant]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = tenants.Create().SetObject(privatev1.Tenant_builder{
				Id: "import-owner", Metadata: privatev1.Metadata_builder{Tenant: "import-owner", Name: "import-owner"}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			parent := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(), privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			compute := createComputeInstanceInState(ctx, computeInstanceDao, privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)
			imported, err := server.lifecycle.externalIPAttachmentDao.Create().SetObject(privatev1.ExternalIPAttachment_builder{
				Metadata: privatev1.Metadata_builder{Tenant: "import-owner", Name: "imported-consumer"}.Build(),
				Spec: privatev1.ExternalIPAttachmentSpec_builder{
					ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: parent.GetId(), Name: "misleading-name"}.Build(),
					ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: compute.GetId()}.Build(),
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			if terminating {
				_, err = suiteTx.Exec(ctx, "update external_ip_attachments set deletion_timestamp=now() where id=$1", imported.GetObject().GetId())
				Expect(err).ToNot(HaveOccurred())
			}
			Expect(grpcstatus.Code(server.lifecycle.ensureExternalIPAvailable(ctx, parent.GetId(), "ignored-name"))).To(Equal(grpccodes.FailedPrecondition))
			Expect(server.lifecycle.ensureExternalIPAvailableExcept(ctx, parent.GetId(), "ignored-name", imported.GetObject().GetId())).To(Succeed())
			Expect(grpcstatus.Code(server.lifecycle.ensureExternalIPAvailableExcept(ctx, parent.GetId(), "ignored-name", "different-child"))).To(Equal(grpccodes.FailedPrecondition))
		}, Entry("live", false), Entry("terminating", true))

		It("rejects caller-supplied output status on Create", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)
			message := "malicious status"
			_, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{Name: "output-on-create"}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
						ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: ci.GetId()}.Build(),
					}.Build(),
					Status: privatev1.ExternalIPAttachmentStatus_builder{
						State:               privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_READY,
						ExternalIpAddress:   "198.51.100.11",
						Message:             &message,
						Hub:                 "hub-1",
						StateTransitionTime: timestamppb.New(time.Now().Truncate(time.Second)),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		})

		It("rejects an ExternalIP already claimed by a NATGateway", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			_, err := server.lifecycle.natGatewayDao.Create().SetObject(privatev1.NATGateway_builder{
				Metadata: privatev1.Metadata_builder{Name: "claimed-by-nat", Tenant: testTenant}.Build(),
				Spec: privatev1.NATGatewaySpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: "virtual-network-id"}.Build(),
					ExternalIp:     privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
				}.Build(),
				Status: privatev1.NATGatewayStatus_builder{
					State: privatev1.NATGatewayState_NAT_GATEWAY_STATE_PENDING,
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)
			_, err = server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{Name: "attachment-after-nat"}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
						ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: ci.GetId()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
		})

		It("rolls back the child when parent settlement validation fails", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)
			response, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{Name: "settlement-rollback"}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
						ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: ci.GetId()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			ready := response.GetObject()
			ready.GetStatus().SetState(privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_READY)
			tx, txErr := database.TxFromContext(ctx)
			Expect(txErr).ToNot(HaveOccurred())
			err = tx.Savepoint(ctx, func(savepointCtx context.Context) error {
				_, updateErr := server.Update(savepointCtx, privatev1.ExternalIPAttachmentsUpdateRequest_builder{
					Object:     ready,
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
				}.Build())
				return updateErr
			})
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))

			childResponse, getErr := server.externalIPAttachmentDao.Get().SetId(ready.GetId()).Do(ctx)
			Expect(getErr).ToNot(HaveOccurred())
			Expect(childResponse.GetObject().GetStatus().GetState()).To(Equal(
				privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_PENDING))
			parentResponse, getErr := externalIPDao.Get().SetId(eip.GetId()).Do(ctx)
			Expect(getErr).ToNot(HaveOccurred())
			Expect(parentResponse.GetObject().GetStatus().GetAttached()).To(BeFalse())
		})

		It("builds attribution for compute, cluster, and bare-metal targets", func() {
			compute := privatev1.ExternalIPAttachment_builder{
				Spec: privatev1.ExternalIPAttachmentSpec_builder{
					ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: "compute-id"}.Build(),
				}.Build(),
			}.Build()
			attribution, err := buildExternalIPAttribution(compute)
			Expect(err).ToNot(HaveOccurred())
			Expect(attribution.GetComputeInstance().GetId()).To(Equal("compute-id"))

			cluster := privatev1.ExternalIPAttachment_builder{
				Spec: privatev1.ExternalIPAttachmentSpec_builder{
					Cluster:        privatev1.ClusterLocalReference_builder{Id: "cluster-id"}.Build(),
					TargetEndpoint: privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
				}.Build(),
			}.Build()
			attribution, err = buildExternalIPAttribution(cluster)
			Expect(err).ToNot(HaveOccurred())
			Expect(attribution.GetCluster().GetId()).To(Equal("cluster-id"))
			Expect(attribution.GetEndpoint()).To(Equal(privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API))

			bareMetal := privatev1.ExternalIPAttachment_builder{
				Spec: privatev1.ExternalIPAttachmentSpec_builder{
					BaremetalInstance: privatev1.BareMetalInstanceLocalReference_builder{Id: "bmi-id"}.Build(),
				}.Build(),
			}.Build()
			attribution, err = buildExternalIPAttribution(bareMetal)
			Expect(err).ToNot(HaveOccurred())
			Expect(attribution.GetBaremetalInstance().GetId()).To(Equal("bmi-id"))
		})

		It("rejects an update without an explicit mask", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)
			response, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{Name: "nil-mask-attachment"}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
						ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: ci.GetId()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			_, err = server.Update(ctx, privatev1.ExternalIPAttachmentsUpdateRequest_builder{
				Object: response.GetObject(),
			}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		})

		It("accepts DELETING to FAILED transition", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)
			createResponse, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{Name: "deleting-to-failed"}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
						ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: ci.GetId()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			object := createResponse.GetObject()
			object.GetStatus().SetState(privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_DELETING)
			_, err = server.Update(ctx, privatev1.ExternalIPAttachmentsUpdateRequest_builder{
				Object:     object,
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			object.GetStatus().SetState(privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_FAILED)
			_, err = server.Update(ctx, privatev1.ExternalIPAttachmentsUpdateRequest_builder{
				Object:     object,
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(object.GetStatus().GetState()).To(Equal(privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_FAILED))
		})

		It("settles and clears the ExternalIP parent from child transitions", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)
			createResponse, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{Name: "settled-attachment"}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
						ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: ci.GetId()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// The first attachment time must be after the parent's creation: the source
			// contract clamps a first time on or before it.
			readyTime := timestamppb.New(time.Now().Truncate(time.Second))
			ready := createResponse.GetObject()
			ready.GetStatus().SetState(privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_READY)
			ready.GetStatus().SetStateTransitionTime(readyTime)
			_, err = server.Update(ctx, privatev1.ExternalIPAttachmentsUpdateRequest_builder{
				Object: ready,
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{
					"status.state", "status.state_transition_time",
				}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			parentResponse, err := externalIPDao.Get().SetId(eip.GetId()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			parent := parentResponse.GetObject()
			Expect(parent.GetStatus().GetAttached()).To(BeTrue())
			Expect(parent.GetStatus().GetAttribution().GetComputeInstance().GetId()).To(Equal(ci.GetId()))
			Expect(parent.GetStatus().GetAttachmentTransitionTime().AsTime()).To(Equal(readyTime.AsTime()))

			detachTime := timestamppb.New(readyTime.AsTime().Add(time.Minute))
			ready.GetStatus().SetState(privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_DELETING)
			ready.GetStatus().SetStateTransitionTime(detachTime)
			_, err = server.Update(ctx, privatev1.ExternalIPAttachmentsUpdateRequest_builder{
				Object: ready,
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{
					"status.state", "status.state_transition_time",
				}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			parentResponse, err = externalIPDao.Get().SetId(eip.GetId()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(parentResponse.GetObject().GetStatus().GetAttached()).To(BeFalse())
			Expect(parentResponse.GetObject().GetStatus().GetAttribution()).To(BeNil())
			Expect(parentResponse.GetObject().GetStatus().GetAttachmentTransitionTime().AsTime()).To(Equal(detachTime.AsTime()))
		})

		It("Creates an attachment with a ComputeInstance target", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)

			response, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "ci-attachment",
					}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
						ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: ci.GetId()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetId()).ToNot(BeEmpty())
			Expect(response.GetObject().GetSpec().GetExternalIp().GetId()).To(Equal(eip.GetId()))
			Expect(response.GetObject().GetSpec().GetComputeInstance().GetId()).To(Equal(ci.GetId()))
			Expect(response.GetObject().GetStatus().GetState()).To(
				Equal(privatev1.ExternalIPAttachmentState_EXTERNAL_IP_ATTACHMENT_STATE_PENDING))
		})

		It("rejects an attachment when the ExternalIP and ComputeInstance belong to different Hubs", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false, "network-hub-a")
			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, "workload-hub-b")
			name := fmt.Sprintf("test-%s", uuid.NewString()[:8])

			response, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{Name: name}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
						ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: ci.GetId()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(response).To(BeNil())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring("different Hub than the canonical networking Hub"))
			Expect(err.Error()).ToNot(ContainSubstring("network-hub-a"))
			Expect(err.Error()).ToNot(ContainSubstring("workload-hub-b"))

			stored, err := server.externalIPAttachmentDao.List().
				SetFilter(fmt.Sprintf("this.metadata.name == %q", name)).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(stored.GetItems()).To(BeEmpty())
		})

		It("Creates an attachment with a Cluster target and API endpoint", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			cluster := createClusterInState(ctx, clusterDao)

			response, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "cluster-api-attachment",
					}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:     privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
						Cluster:        privatev1.ClusterLocalReference_builder{Id: cluster.GetId()}.Build(),
						TargetEndpoint: privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetSpec().GetCluster().GetId()).To(Equal(cluster.GetId()))
			Expect(response.GetObject().GetSpec().GetTargetEndpoint()).To(
				Equal(privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API))
		})

		It("Creates an attachment with a Cluster target and ingress endpoint", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			cluster := createClusterInState(ctx, clusterDao)

			response, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:     privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
						Cluster:        privatev1.ClusterLocalReference_builder{Id: cluster.GetId()}.Build(),
						TargetEndpoint: privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetSpec().GetTargetEndpoint()).To(
				Equal(privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS))
		})

		It("Creates an attachment with a BareMetalInstance target", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			bmi := createBareMetalInstanceInState(ctx, bareMetalInstanceDao)

			response, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:        privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
						BaremetalInstance: privatev1.BareMetalInstanceLocalReference_builder{Id: bmi.GetId()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetSpec().GetBaremetalInstance().GetId()).To(Equal(bmi.GetId()))
		})

		It("Lists external IP attachments", func() {
			eip1 := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			ci1 := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)
			eip2 := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			ci2 := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)

			_, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: eip1.GetId()}.Build(),
						ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: ci1.GetId()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: eip2.GetId()}.Build(),
						ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: ci2.GetId()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			listResponse, err := server.List(ctx, privatev1.ExternalIPAttachmentsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(listResponse.GetSize()).To(Equal(int32(2)))
			Expect(listResponse.GetItems()).To(HaveLen(2))
		})

		It("Gets an external IP attachment", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)

			createResponse, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "my-attachment",
					}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
						ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: ci.GetId()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			getResponse, err := server.Get(ctx, privatev1.ExternalIPAttachmentsGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetId()).To(Equal(createResponse.GetObject().GetId()))
			Expect(getResponse.GetObject().GetSpec().GetExternalIp().GetId()).To(Equal(eip.GetId()))
			Expect(getResponse.GetObject().GetSpec().GetComputeInstance().GetId()).To(Equal(ci.GetId()))
		})

		It("Updates metadata of an external IP attachment", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)

			createResponse, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "my-attachment",
					}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
						ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: ci.GetId()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			updateResponse, err := server.Update(ctx, privatev1.ExternalIPAttachmentsUpdateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Id: createResponse.GetObject().GetId(),
					Metadata: privatev1.Metadata_builder{
						Labels: map[string]string{"env": "test"},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"metadata.labels"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetMetadata().GetLabels()).To(HaveKeyWithValue("env", "test"))
		})

		It("allows trusted lifecycle updates to status.message and status.hub", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)
			createResponse, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{Name: "trusted-status-update"}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
						ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: ci.GetId()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			message := "trusted message"
			object := privatev1.ExternalIPAttachment_builder{
				Id: createResponse.GetObject().GetId(),
				Status: privatev1.ExternalIPAttachmentStatus_builder{
					Message: &message,
					Hub:     "hub-1",
				}.Build(),
			}.Build()
			response, err := server.Update(ctx, privatev1.ExternalIPAttachmentsUpdateRequest_builder{
				Object: object,
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"status.message", "status.hub"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetStatus().GetMessage()).To(Equal("trusted message"))
			Expect(response.GetObject().GetStatus().GetHub()).To(Equal("hub-1"))
		})

		It("Deletes an external IP attachment", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)

			createResponse, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
						ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: ci.GetId()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Delete(ctx, privatev1.ExternalIPAttachmentsDeleteRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Signals an external IP attachment", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)

			createResponse, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
						ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: ci.GetId()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Signal(ctx, privatev1.ExternalIPAttachmentsSignalRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Describe("Validation", func() {
		It("Rejects Create with nil object", func() {
			_, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("is mandatory"))
		})

		It("Rejects Create with nil spec", func() {
			_, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "my-attachment",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("spec is mandatory"))
		})

		It("Rejects Create with empty spec.external_ip", func() {
			_, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: "some-ci"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("spec.external_ip"))
		})

		It("Rejects Create with no target set", func() {
			_, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp: privatev1.ExternalIPLocalReference_builder{Id: "some-ip"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("exactly one target must be set"))
		})

		It("Rejects Create when cluster target has no target_endpoint", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			cluster := createClusterInState(ctx, clusterDao)

			_, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp: privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
						Cluster:    privatev1.ClusterLocalReference_builder{Id: cluster.GetId()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("target_endpoint"))
			Expect(err.Error()).To(ContainSubstring("required when target is cluster"))
		})

		It("Rejects Create when non-cluster target has target_endpoint set", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)

			_, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
						ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: ci.GetId()}.Build(),
						TargetEndpoint:  privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("target_endpoint"))
			Expect(err.Error()).To(ContainSubstring("UNSPECIFIED for non-cluster"))
		})
	})

	Describe("ExternalIP reference validation", func() {
		It("Rejects Create when ExternalIP does not exist", func() {
			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)

			_, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: "nonexistent-external-ip-id"}.Build(),
						ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: ci.GetId()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("does not exist"))
		})

		It("Rejects Create when ExternalIP is not in ALLOCATED state", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_PENDING, false)
			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)

			_, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
						ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: ci.GetId()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring("not in ALLOCATED state"))
		})

		It("Rejects Create when the ALLOCATED ExternalIP is being deleted", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			eip.GetMetadata().SetFinalizers([]string{"test-finalizer"})
			_, err := externalIPDao.Update().SetObject(eip).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = externalIPDao.Delete().SetId(eip.GetId()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			eipResponse, err := externalIPDao.Get().SetId(eip.GetId()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(eipResponse.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeTrue())

			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)
			_, err = server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
						ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: ci.GetId()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue(), "unexpected error: %v", err)
			Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition), "unexpected error: %v", err)
			Expect(err.Error()).To(ContainSubstring("is being deleted"))
		})

		It("does not use the parent attached output as an exclusivity guard", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, true)
			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)

			_, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
						ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: ci.GetId()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Describe("Target reference validation", func() {
		It("rejects an ExternalIPAttachment until its ComputeInstance is ready", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING)

			_, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{Name: "attachment-before-ready"}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
						ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: ci.GetId()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring("must be READY"))
		})

		It("Rejects Create when ComputeInstance does not exist", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)

			_, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
						ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: "nonexistent-ci-id"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("ComputeInstance"))
			Expect(err.Error()).To(ContainSubstring("does not exist"))
		})

		It("Rejects Create when the target ComputeInstance is being deleted", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)
			ci.GetMetadata().SetFinalizers([]string{"test-finalizer"})
			_, err := computeInstanceDao.Update().SetObject(ci).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = computeInstanceDao.Delete().SetId(ci.GetId()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			ciResponse, err := computeInstanceDao.Get().SetId(ci.GetId()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(ciResponse.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeTrue())

			_, err = server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
						ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: ci.GetId()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue(), "unexpected error: %v", err)
			Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition), "unexpected error: %v", err)
			Expect(err.Error()).To(ContainSubstring("is being deleted"))
		})

		It("Rejects Create when Cluster does not exist", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)

			_, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:     privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
						Cluster:        privatev1.ClusterLocalReference_builder{Id: "nonexistent-cluster-id"}.Build(),
						TargetEndpoint: privatev1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("Cluster"))
			Expect(err.Error()).To(ContainSubstring("does not exist"))
		})

		It("Rejects Create when BareMetalInstance does not exist", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)

			_, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:        privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
						BaremetalInstance: privatev1.BareMetalInstanceLocalReference_builder{Id: "nonexistent-bmi-id"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("BareMetalInstance"))
			Expect(err.Error()).To(ContainSubstring("does not exist"))
		})
	})

	Describe("Uniqueness constraints", func() {
		It("Rejects Create when ExternalIP already has an attachment", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			ci1 := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)
			ci2 := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)

			_, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
						ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: ci1.GetId()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
						ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: ci2.GetId()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.AlreadyExists))
			Expect(err.Error()).To(ContainSubstring("ExternalIP"))
		})

		It("Rejects Create when ExternalIP already has an attachment (uniqueness check)", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			ci1 := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)
			ci2 := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)

			_, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
						ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: ci1.GetId()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
						ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: ci2.GetId()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.AlreadyExists))
			Expect(err.Error()).To(ContainSubstring("ExternalIP"))
		})
	})

	It("keeps a terminating Attachment exclusive until archive", func() {
		eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
			privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
		firstTarget := createComputeInstanceInState(ctx, computeInstanceDao,
			privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)
		secondTarget := createComputeInstanceInState(ctx, computeInstanceDao,
			privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)
		first, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
			Object: privatev1.ExternalIPAttachment_builder{
				Metadata: privatev1.Metadata_builder{
					Name:       fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					Finalizers: []string{"test-hold"},
				}.Build(),
				Spec: privatev1.ExternalIPAttachmentSpec_builder{
					ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
					ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: firstTarget.GetId()}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		_, err = server.Delete(ctx, privatev1.ExternalIPAttachmentsDeleteRequest_builder{Id: first.GetObject().GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())

		_, err = server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
			Object: privatev1.ExternalIPAttachment_builder{
				Metadata: privatev1.Metadata_builder{Name: fmt.Sprintf("test-%s", uuid.NewString()[:8])}.Build(),
				Spec: privatev1.ExternalIPAttachmentSpec_builder{
					ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
					ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: secondTarget.GetId()}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.AlreadyExists))
	})

	Describe("Immutable fields", func() {
		It("Rejects update of spec.external_ip", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)

			createResponse, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
						ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: ci.GetId()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			eip2 := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			_, err = server.Update(ctx, privatev1.ExternalIPAttachmentsUpdateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Id: createResponse.GetObject().GetId(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp: privatev1.ExternalIPLocalReference_builder{Id: eip2.GetId()}.Build(),
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.external_ip"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("spec.external_ip"))
			Expect(err.Error()).To(ContainSubstring("immutable"))
		})

		It("Rejects update of spec.compute_instance", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)

			createResponse, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
						ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: ci.GetId()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			ci2 := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)
			_, err = server.Update(ctx, privatev1.ExternalIPAttachmentsUpdateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Id: createResponse.GetObject().GetId(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
						ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: ci2.GetId()}.Build(),
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.compute_instance"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("spec.compute_instance"))
			Expect(err.Error()).To(ContainSubstring("immutable"))
		})

		It("rejects moving a stored ExternalIPAttachment into a reserved tenant and preserves the stored object", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)

			createResponse, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						Tenant: testTenant,
					}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
						ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: ci.GetId()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()

			_, err = server.Update(ctx, privatev1.ExternalIPAttachmentsUpdateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Id:       id,
					Metadata: privatev1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.tenant"}},
			}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.PermissionDenied))
			Expect(err.Error()).To(ContainSubstring("objects cannot be placed in the 'shared' tenant"))

			stored, getErr := server.Get(ctx, privatev1.ExternalIPAttachmentsGetRequest_builder{Id: id}.Build())
			Expect(getErr).ToNot(HaveOccurred())
			Expect(stored.GetObject().GetMetadata().GetTenant()).To(Equal(testTenant))
		})
	})

	Describe("Attached flag management", func() {
		It("does not settle ExternalIP status on Create", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)

			_, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
						ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: ci.GetId()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			ipResp, err := externalIPDao.Get().SetId(eip.GetId()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(ipResp.GetObject().GetStatus().GetAttached()).To(BeFalse())
		})

		It("Sets ExternalIP.status.attached to false on Delete", func() {
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)

			createResp, err := server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
						ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: ci.GetId()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Delete(ctx, privatev1.ExternalIPAttachmentsDeleteRequest_builder{
				Id: createResp.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			ipResp, err := externalIPDao.Get().SetId(eip.GetId()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(ipResp.GetObject().GetStatus().GetAttached()).To(BeFalse())
		})
	})

	Describe("Delete behaviour", func() {
		It("Returns error for empty ID on Delete", func() {
			_, err := server.Delete(ctx, privatev1.ExternalIPAttachmentsDeleteRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("identifier is mandatory"))
		})
	})

	Describe("Tenant isolation", func() {
		It("rejects ExternalIPAttachment referencing ExternalIP from a different tenant", func() {
			tenantsDao, err := dao.NewGenericDAO[*privatev1.Tenant]().
				SetLogger(logger).
				SetTableName("tenants").
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = tenantsDao.Create().
				SetObject(privatev1.Tenant_builder{
					Id: "tenant-b",
					Metadata: privatev1.Metadata_builder{
						Name:   "tenant-b",
						Tenant: "tenant-b",
					}.Build(),
				}.Build()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
			ci := createComputeInstanceInState(ctx, computeInstanceDao,
				privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING)
			_, err = server.Create(ctx, privatev1.ExternalIPAttachmentsCreateRequest_builder{
				Object: privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "cross-tenant-attachment",
						Tenant: "tenant-b",
					}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: eip.GetId()}.Build(),
						ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: ci.GetId()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(err).To(MatchError(ContainSubstring("belongs to tenant")))
		})
	})
})
