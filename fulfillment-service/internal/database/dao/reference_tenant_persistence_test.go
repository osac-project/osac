/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package dao

import (
	"context"
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Full-reference tenant persistence", func() {
	var (
		ctrl    *gomock.Controller
		tenancy *auth.MockTenancyLogic
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
		tenancy = auth.NewMockTenancyLogic(ctrl)
		tenancy.EXPECT().DetermineVisibility(gomock.Any()).
			Return(auth.TotalVisibility(), nil).
			AnyTimes()
	})

	It("marshals the supplied tenant selector as-is", func() {
		generic, err := NewGenericDAO[*privatev1.Cluster]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		object := privatev1.Cluster_builder{
			Metadata: privatev1.Metadata_builder{Tenant: "my-tenant"}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				SshKey: privatev1.SecretReference_builder{Id: "ssh-key", Tenant: auth.SharedTenant}.Build(),
			}.Build(),
		}.Build()
		data, err := (&request[*privatev1.Cluster]{dao: generic}).marshalData(object)
		Expect(err).ToNot(HaveOccurred())
		var stored map[string]any
		Expect(json.Unmarshal(data, &stored)).To(Succeed())
		spec := stored["spec"].(map[string]any)
		sshKey := spec["ssh_key"].(map[string]any)
		Expect(sshKey).To(HaveKeyWithValue("tenant", auth.SharedTenant))
		Expect(sshKey).ToNot(HaveKey("shared"))
		Expect(object.GetSpec().GetSshKey().GetTenant()).To(Equal(auth.SharedTenant))
	})

	Describe("JSONB persistence", func() {
		var (
			ctx      context.Context
			tx       database.Tx
			clusters *GenericDAO[*privatev1.Cluster]
		)

		BeforeEach(func() {
			ctx = context.Background()
			db, err := server.NewInstance().Build()
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(db.Close)
			pool, err := db.Pool(ctx)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(pool.Close)
			txManager, err := database.NewTxManager().SetLogger(logger).SetPool(pool).Build()
			Expect(err).ToNot(HaveOccurred())
			tx, err = txManager.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { Expect(tx.End(ctx)).To(Succeed()) })
			ctx = database.TxIntoContext(ctx, tx)

			tenants, err := NewGenericDAO[*privatev1.Tenant]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = tenants.Create().SetObject(privatev1.Tenant_builder{
				Id:       "my-tenant",
				Metadata: privatev1.Metadata_builder{Name: "my-tenant", Tenant: "my-tenant"}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			clusters, err = NewGenericDAO[*privatev1.Cluster]().
				SetTableName("objects").
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		assertStoredReference := func(id, tenant string) {
			var data []byte
			Expect(tx.QueryRow(ctx, "select data from objects where id = $1", id).Scan(&data)).To(Succeed())
			var stored map[string]any
			Expect(json.Unmarshal(data, &stored)).To(Succeed())
			spec := stored["spec"].(map[string]any)
			sshKey := spec["ssh_key"].(map[string]any)
			if tenant == "" {
				Expect(sshKey).ToNot(HaveKey("tenant"))
			} else {
				Expect(sshKey).To(HaveKeyWithValue("tenant", tenant))
			}
			Expect(sshKey).ToNot(HaveKey("shared"))
		}

		It("stores tenant selectors on create and update", func() {
			object := privatev1.Cluster_builder{
				Metadata: privatev1.Metadata_builder{Tenant: "my-tenant"}.Build(),
				Spec: privatev1.ClusterSpec_builder{
					SshKey: privatev1.SecretReference_builder{Id: "ssh-key", Tenant: auth.SharedTenant}.Build(),
				}.Build(),
			}.Build()
			created, err := clusters.Create().SetObject(object).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			id := created.GetObject().GetId()
			assertStoredReference(id, auth.SharedTenant)
			loaded, err := clusters.Get().SetId(id).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(loaded.GetObject().GetSpec().GetSshKey().GetTenant()).To(Equal(auth.SharedTenant))

			updated, err := clusters.Update().SetObject(loaded.GetObject()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			assertStoredReference(id, auth.SharedTenant)
			Expect(updated.GetObject().GetSpec().GetSshKey().GetTenant()).To(Equal(auth.SharedTenant))
		})

		It("preserves an omitted tenant for owner-scoped references", func() {
			created, err := clusters.Create().SetObject(privatev1.Cluster_builder{
				Metadata: privatev1.Metadata_builder{Tenant: "my-tenant"}.Build(),
				Spec: privatev1.ClusterSpec_builder{
					SshKey: privatev1.SecretReference_builder{Id: "owner-key"}.Build(),
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			assertStoredReference(created.GetObject().GetId(), "")
		})
	})
})
