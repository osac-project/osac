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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// These component-integration cases use real PostgreSQL, triggers, handlers and committed transactions.
// Tenancy/attribution are suite doubles; billing intervals are seeded locally (no provider or Keycloak).
var _ = Describe("Billed tenant domain lifecycle", func() {
	var tenants *PrivateTenantsServer

	BeforeEach(func() {
		var err error
		tenants, err = NewPrivateTenantsServer().SetLogger(logger).
			SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		// Exercise request-sized commits and rollback, rather than inspecting only an uncommitted suite transaction.
		Expect(suiteTx.End(ctx)).To(Succeed())
		suiteTx = nil
		ctx = database.TxManagerIntoContext(context.Background(), tm)
	})

	run := func(body func(context.Context) error) error {
		GinkgoHelper()
		_, err := database.WithNewTx(ctx, func(txCtx context.Context) (struct{}, error) {
			return struct{}{}, body(txCtx)
		})
		return err
	}
	create := func(name string, finalizers []string) (*privatev1.Tenant, error) {
		GinkgoHelper()
		var result *privatev1.Tenant
		err := run(func(ctx context.Context) error {
			response, err := tenants.Create(ctx, privatev1.TenantsCreateRequest_builder{
				Object: privatev1.Tenant_builder{
					Metadata: privatev1.Metadata_builder{Name: name, Finalizers: finalizers}.Build(),
					Spec:     privatev1.TenantSpec_builder{Domains: []string{"reusable.example.com"}}.Build(),
				}.Build(),
			}.Build())
			result = response.GetObject()
			return err
		})
		return result, err
	}
	get := func() *privatev1.Tenant {
		GinkgoHelper()
		var result *privatev1.Tenant
		Expect(run(func(ctx context.Context) error {
			response, err := tenants.Get(ctx, privatev1.TenantsGetRequest_builder{Id: "domain-history"}.Build())
			result = response.GetObject()
			return err
		})).To(Succeed())
		return result
	}
	removeFinalizers := func(finalizers []string) error {
		GinkgoHelper()
		// A finalizer write must carry the version read in this same request/transaction.
		return run(func(ctx context.Context) error {
			response, err := tenants.Get(ctx, privatev1.TenantsGetRequest_builder{Id: "domain-history"}.Build())
			if err != nil {
				return err
			}
			object := response.GetObject()
			object.GetMetadata().SetFinalizers(finalizers)
			_, err = tenants.Update(ctx, privatev1.TenantsUpdateRequest_builder{
				Object: object, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}},
			}.Build())
			return err
		})
	}
	deleteTenant := func() error {
		GinkgoHelper()
		return run(func(ctx context.Context) error {
			_, err := tenants.Delete(ctx, privatev1.TenantsDeleteRequest_builder{Id: "domain-history"}.Build())
			return err
		})
	}
	removeProjects := func() {
		GinkgoHelper()
		projects, err := NewPrivateProjectsServer().SetLogger(logger).
			SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(run(func(ctx context.Context) error {
			response, err := projects.List(ctx, privatev1.ProjectsListRequest_builder{
				Filter: new(`this.metadata.tenant == "domain-history"`),
			}.Build())
			if err != nil {
				return err
			}
			for _, project := range response.GetItems() {
				_, err = projects.Delete(ctx, privatev1.ProjectsDeleteRequest_builder{Id: project.GetId()}.Build())
				if err != nil {
					return err
				}
			}
			return nil
		})).To(Succeed())
	}
	owner := func() string {
		GinkgoHelper()
		var result string
		Expect(testPool.QueryRow(ctx, `select coalesce((select tenant from tenant_domains
			where domain = 'reusable.example.com'), '')`).Scan(&result)).To(Succeed())
		return result
	}
	assignment := func(id string) database.TenantBillingAssignmentRow {
		GinkgoHelper()
		var result database.TenantBillingAssignmentRow
		Expect(run(func(ctx context.Context) error {
			tx, err := database.TxFromContext(ctx)
			if err != nil {
				return err
			}
			result, err = database.GetTenantBillingAssignment(ctx, tx, id)
			return err
		})).To(Succeed())
		return result
	}

	DescribeTable("releases domains only after successful cleanup while retaining billing identity", func(withFinalizers bool) {
		var finalizers []string
		if withFinalizers {
			finalizers = []string{"cleanup-one", "cleanup-two"}
		}
		_, err := create("domain-history", finalizers)
		Expect(err).ToNot(HaveOccurred())
		var closedID, activeID string
		Expect(run(func(ctx context.Context) error {
			tx, err := database.TxFromContext(ctx)
			if err != nil {
				return err
			}
			for i := range 2 {
				account, err := database.UpsertBillingAccount(ctx, tx, database.BillingAccountUpsert{
					ID: uuid.New(), ProviderKey: "m360", ProviderAccountReference: uuid.New(), State: "ACTIVE",
				})
				if err != nil {
					return err
				}
				id, err := database.CloseAndInsertTenantBillingAssignment(ctx, tx, closedID,
					database.TenantBillingAssignmentInsert{
						ID: uuid.New(), Name: uuid.New(), TenantID: "domain-history",
						BillingAccountID: account, ConnectionState: "CONNECTED",
					})
				if err != nil {
					return err
				}
				if i == 0 {
					closedID = id
				} else {
					activeID = id
				}
			}
			return nil
		})).To(Succeed())
		closedBefore := assignment(closedID)
		Expect(closedBefore.EffectiveTo).NotTo(BeNil())
		Expect(assignment(activeID).EffectiveTo).To(BeNil())
		_, err = create("live-collision", nil)
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.AlreadyExists))
		Expect(owner()).To(Equal("domain-history"))

		if !withFinalizers {
			Expect(grpcstatus.Code(deleteTenant())).To(Equal(grpccodes.FailedPrecondition))
			Expect(get().GetMetadata().GetDeletionTimestamp()).To(BeNil())
			Expect(owner()).To(Equal("domain-history"))
			Expect(assignment(activeID).EffectiveTo).To(BeNil())
			removeProjects()
		}
		Expect(deleteTenant()).To(Succeed())
		closedOnDelete := assignment(activeID)
		Expect(closedOnDelete.EffectiveTo).NotTo(BeNil())
		Expect(closedOnDelete.Message).To(Equal(database.TenantBillingAssignmentDeletedMessage))
		Expect(assignment(closedID)).To(Equal(closedBefore))

		if withFinalizers {
			Expect(owner()).To(Equal("domain-history"), "Delete acceptance is not finalization")
			Expect(deleteTenant()).To(Succeed())
			Expect(removeFinalizers([]string{"cleanup-two"})).To(Succeed())
			Expect(owner()).To(Equal("domain-history"), "one remaining finalizer still protects the domain")
			_, err = create("pending-collision", nil)
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.AlreadyExists))
			// The trigger releases helpers before archival; failed cleanup must roll that release back.
			Expect(grpcstatus.Code(removeFinalizers(nil))).To(Equal(grpccodes.FailedPrecondition))
			Expect(get().GetMetadata().GetFinalizers()).To(Equal([]string{"cleanup-two"}))
			Expect(owner()).To(Equal("domain-history"))
			Expect(assignment(activeID)).To(Equal(closedOnDelete))
			removeProjects()
			Expect(removeFinalizers(nil)).To(Succeed())
		}
		Expect(owner()).To(BeEmpty())
		retained := get()
		Expect(retained.GetMetadata().GetFinalizers()).To(BeEmpty())
		Expect(retained.GetMetadata().GetDeletionTimestamp()).NotTo(BeNil())
		Expect(retained.GetMetadata().GetName()).To(Equal("domain-history"))
		Expect(retained.GetSpec().GetDomains()).To(Equal([]string{"reusable.example.com"}))
		Expect(assignment(closedID)).To(Equal(closedBefore))
		Expect(assignment(activeID)).To(Equal(closedOnDelete))

		_, err = create("different-domain-owner", nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(owner()).To(Equal("different-domain-owner"))
		Expect(deleteTenant()).To(Succeed())
		Expect(run(func(ctx context.Context) error {
			_, err := tenants.Update(ctx, privatev1.TenantsUpdateRequest_builder{
				Object: privatev1.Tenant_builder{
					Id: "domain-history", Status: privatev1.TenantStatus_builder{Message: new("cleanup completed")}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.message"}},
			}.Build())
			return err
		})).To(Succeed())
		Expect(owner()).To(Equal("different-domain-owner"))
		Expect(get().GetSpec().GetDomains()).To(Equal([]string{"reusable.example.com"}))
		Expect(assignment(closedID)).To(Equal(closedBefore))
		Expect(assignment(activeID)).To(Equal(closedOnDelete))
		_, err = create("replacement-collision", nil)
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.AlreadyExists))

		// Historical domain text must not make public identity discovery ambiguous. Public List already applies
		// liveness filtering; the private cleanup/history view intentionally retains the original tenant.
		public, err := NewTenantsServer().SetLogger(logger).
			SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(run(func(ctx context.Context) error {
			response, err := public.List(ctx, publicv1.TenantsListRequest_builder{
				Filter: new(`"reusable.example.com" in this.spec.domains`),
			}.Build())
			if err != nil {
				return err
			}
			Expect(response.GetTotal()).To(Equal(int32(1)))
			Expect(response.GetItems()).To(HaveLen(1))
			Expect(response.GetItems()[0].GetMetadata().GetName()).To(Equal("different-domain-owner"))
			return nil
		})).To(Succeed())
	}, Entry("direct deletion with no finalizers", false), Entry("deletion followed by finalizer completion", true))
})
