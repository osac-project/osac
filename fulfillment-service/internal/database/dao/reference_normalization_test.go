/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package dao

import (
	"context"
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Reference selector normalization", func() {
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

	It("normalizes legacy and explicit selectors while preserving owner scope", func() {
		testCases := []struct {
			name       string
			tenant     string
			shared     bool
			wantTenant string
			wantError  string
		}{
			{
				name:       "legacy shared selector",
				shared:     true,
				wantTenant: auth.SharedTenant,
			},
			{
				name:       "matching explicit shared tenant",
				tenant:     auth.SharedTenant,
				shared:     true,
				wantTenant: auth.SharedTenant,
			},
			{
				name:       "owner scope",
				wantTenant: "",
			},
			{
				name:       "explicit tenant",
				tenant:     "tenant-b",
				wantTenant: "tenant-b",
			},
			{
				name:      "conflicting selectors",
				tenant:    "tenant-b",
				shared:    true,
				wantError: "conflicting tenant and shared selectors",
			},
		}

		for _, testCase := range testCases {
			By(testCase.name)
			reference := privatev1.SecretReference_builder{
				Id:     "secret-id",
				Tenant: testCase.tenant,
				Shared: testCase.shared,
			}.Build()

			err := normalizeReferenceSelectors(reference.ProtoReflect())
			if testCase.wantError != "" {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(testCase.wantError))
				Expect(reference.GetTenant()).To(Equal(testCase.tenant))
				Expect(reference.GetShared()).To(BeTrue())
				continue
			}

			Expect(err).ToNot(HaveOccurred())
			Expect(reference.GetTenant()).To(Equal(testCase.wantTenant))
			Expect(reference.GetShared()).To(BeFalse())
		}
	})

	It("recurses through singular, repeated, and map-contained messages and skips local references", func() {
		localReference := privatev1.SecretLocalReference_builder{Id: "local-secret", Name: "pull-secret"}.Build()
		cluster := privatev1.Cluster_builder{
			Spec: privatev1.ClusterSpec_builder{
				SshKey: privatev1.SecretReference_builder{Id: "ssh-key", Shared: true}.Build(),
				AddOnOperators: []*privatev1.AddOnOperatorReference{
					privatev1.AddOnOperatorReference_builder{Id: "owner-operator"}.Build(),
					privatev1.AddOnOperatorReference_builder{Id: "shared-operator", Shared: true}.Build(),
				},
				NodeSets: map[string]*privatev1.ClusterNodeSet{
					"workers": privatev1.ClusterNodeSet_builder{
						BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{
							Id: "shared-instance-type", Shared: true,
						}.Build(),
					}.Build(),
				},
				PullSecretSecret: localReference,
			}.Build(),
		}.Build()

		err := normalizeReferenceSelectors(cluster.ProtoReflect())
		Expect(err).ToNot(HaveOccurred())
		Expect(cluster.GetSpec().GetSshKey().GetTenant()).To(Equal(auth.SharedTenant))
		Expect(cluster.GetSpec().GetSshKey().GetShared()).To(BeFalse())
		Expect(cluster.GetSpec().GetAddOnOperators()[0].GetTenant()).To(BeEmpty())
		Expect(cluster.GetSpec().GetAddOnOperators()[0].GetShared()).To(BeFalse())
		Expect(cluster.GetSpec().GetAddOnOperators()[1].GetTenant()).To(Equal(auth.SharedTenant))
		Expect(cluster.GetSpec().GetAddOnOperators()[1].GetShared()).To(BeFalse())
		nodeSet := cluster.GetSpec().GetNodeSets()["workers"]
		Expect(nodeSet.GetBaremetalInstanceType().GetTenant()).To(Equal(auth.SharedTenant))
		Expect(nodeSet.GetBaremetalInstanceType().GetShared()).To(BeFalse())
		Expect(proto.Equal(localReference, cluster.GetSpec().GetPullSecretSecret())).To(BeTrue())
	})

	It("restores shared selectors recursively for tenant-only references", func() {
		cluster := privatev1.Cluster_builder{
			Spec: privatev1.ClusterSpec_builder{
				SshKey: privatev1.SecretReference_builder{Id: "ssh-key", Tenant: auth.SharedTenant}.Build(),
				AddOnOperators: []*privatev1.AddOnOperatorReference{
					privatev1.AddOnOperatorReference_builder{Id: "operator", Tenant: auth.SharedTenant}.Build(),
				},
				NodeSets: map[string]*privatev1.ClusterNodeSet{
					"workers": privatev1.ClusterNodeSet_builder{
						BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{
							Id: "instance-type", Tenant: auth.SharedTenant,
						}.Build(),
					}.Build(),
				},
			}.Build(),
		}.Build()

		canonicalizeSharedTenantSelectors(cluster.ProtoReflect())

		Expect(cluster.GetSpec().GetSshKey().GetTenant()).To(Equal(auth.SharedTenant))
		Expect(cluster.GetSpec().GetSshKey().GetShared()).To(BeTrue())
		Expect(cluster.GetSpec().GetAddOnOperators()[0].GetShared()).To(BeTrue())
		Expect(cluster.GetSpec().GetNodeSets()["workers"].GetBaremetalInstanceType().GetShared()).To(BeTrue())
	})

	It("marshals a normalized clone without mutating the supplied object", func() {
		generic, err := NewGenericDAO[*privatev1.Cluster]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		object := privatev1.Cluster_builder{
			Metadata: privatev1.Metadata_builder{Tenant: "my-tenant"}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				SshKey: privatev1.SecretReference_builder{Id: "ssh-key", Shared: true}.Build(),
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
		Expect(object.GetSpec().GetSshKey().GetShared()).To(BeTrue())
		Expect(object.GetSpec().GetSshKey().GetTenant()).To(BeEmpty())
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

		assertStoredReference := func(id string) {
			var data []byte
			Expect(tx.QueryRow(ctx, "select data from objects where id = $1", id).Scan(&data)).To(Succeed())
			var stored map[string]any
			Expect(json.Unmarshal(data, &stored)).To(Succeed())
			spec := stored["spec"].(map[string]any)
			sshKey := spec["ssh_key"].(map[string]any)
			Expect(sshKey).To(HaveKeyWithValue("tenant", auth.SharedTenant))
			Expect(sshKey).ToNot(HaveKey("shared"))
		}

		It("stores tenant-only references on create", func() {
			object := privatev1.Cluster_builder{
				Metadata: privatev1.Metadata_builder{Tenant: "my-tenant"}.Build(),
				Spec: privatev1.ClusterSpec_builder{
					SshKey: privatev1.SecretReference_builder{Id: "ssh-key", Shared: true}.Build(),
				}.Build(),
			}.Build()
			created, err := clusters.Create().SetObject(object).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			assertStoredReference(created.GetObject().GetId())
			Expect(object.GetSpec().GetSshKey().GetShared()).To(BeTrue())
			Expect(object.GetSpec().GetSshKey().GetTenant()).To(BeEmpty())
			Expect(created.GetObject().GetSpec().GetSshKey().GetTenant()).To(Equal(auth.SharedTenant))
			Expect(created.GetObject().GetSpec().GetSshKey().GetShared()).To(BeTrue())

			loaded, err := clusters.Get().SetId(created.GetObject().GetId()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(loaded.GetObject().GetSpec().GetSshKey(), created.GetObject().GetSpec().GetSshKey())).To(BeTrue())
		})

		It("rejects conflicting selectors before inserting an object", func() {
			object := privatev1.Cluster_builder{
				Id:       "conflicting-reference",
				Metadata: privatev1.Metadata_builder{Tenant: "my-tenant"}.Build(),
				Spec: privatev1.ClusterSpec_builder{
					SshKey: privatev1.SecretReference_builder{
						Id: "ssh-key", Tenant: "tenant-b", Shared: true,
					}.Build(),
				}.Build(),
			}.Build()

			_, err := clusters.Create().SetObject(object).Do(ctx)
			Expect(err).To(MatchError(ContainSubstring("failed to normalize reference selectors")))

			var count int
			Expect(tx.QueryRow(ctx, "select count(*) from objects where id = $1", object.GetId()).Scan(&count)).To(Succeed())
			Expect(count).To(BeZero())
		})

		It("stores tenant-only references when updating a legacy-loaded object", func() {
			created, err := clusters.Create().SetObject(privatev1.Cluster_builder{
				Metadata: privatev1.Metadata_builder{Tenant: "my-tenant"}.Build(),
				Spec:     privatev1.ClusterSpec_builder{}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			id := created.GetObject().GetId()

			_, err = tx.Exec(ctx, `update objects set data = jsonb_set(
				data, '{spec,ssh_key}', '{"id":"ssh-key","shared":true}'::jsonb, true
			) where id = $1`, id)
			Expect(err).ToNot(HaveOccurred())

			loaded, err := clusters.Get().SetId(id).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			legacyObject := loaded.GetObject()
			Expect(legacyObject.GetSpec().GetSshKey().GetShared()).To(BeTrue())

			updated, err := clusters.Update().SetObject(legacyObject).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(updated.GetObject().GetSpec().GetSshKey(), legacyObject.GetSpec().GetSshKey())).To(BeTrue())
			assertStoredReference(id)
			Expect(legacyObject.GetSpec().GetSshKey().GetShared()).To(BeTrue())
		})
	})
})
