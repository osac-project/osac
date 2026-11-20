/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package it

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Scoped VAST NFS preparation Secrets", Label("secrets", "vault"), func() {
	It("reads the exact protected bundle with the controller identity", func(ctx context.Context) {
		admin := tool.InternalView().AdminConn()
		tenants := privatev1.NewTenantsClient(admin)
		secrets := privatev1.NewSecretsClient(admin)
		controller := privatev1.NewSecretsClient(tool.InternalView().ControllerConn())
		tenant := publicv1.NewSecretsClient(tool.ExternalView().UserConn())

		By("Reconciling the built-in system tenant's Vault namespace")
		_, err := tenants.Signal(ctx, privatev1.TenantsSignalRequest_builder{Id: auth.SystemTenant}.Build())
		Expect(err).NotTo(HaveOccurred())
		Eventually(func(g Gomega) {
			result, err := tenants.Get(ctx, privatev1.TenantsGetRequest_builder{Id: auth.SystemTenant}.Build())
			g.Expect(err).NotTo(HaveOccurred())
			condition := findTenantCondition(result.GetObject().GetStatus().GetConditions(),
				privatev1.TenantConditionType_TENANT_CONDITION_TYPE_VAULT_READY)
			g.Expect(condition.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
		}, time.Minute, time.Second).Should(Succeed())

		const backend = "vast-east"
		acme := "it-vast-acme-" + uuid.New()[24:]
		baz := "it-vast-baz-" + uuid.New()[24:]
		acmeData := []byte("schema_version: 1\nprovider: vast\nstorage_tenant: fixture-acme\n")
		bazData := []byte("schema_version: 1\nprovider: vast\nstorage_tenant: fixture-baz\n")
		acmeName := vastNFSFixtureName(acme, backend)
		bazName := vastNFSFixtureName(baz, backend)
		Expect(bazName).NotTo(Equal(acmeName))
		acmeID := createScopedVASTBundle(ctx, secrets, acmeName, acmeData)
		bazID := createScopedVASTBundle(ctx, secrets, bazName, bazData)

		lookup := func(name string) string {
			return fmt.Sprintf(
				"this.metadata.tenant == %q && this.metadata.project == %q && this.metadata.name == %q",
				auth.SystemTenant, "", name,
			)
		}

		for _, fixture := range []struct {
			name string
			id   string
			data []byte
		}{
			{name: acmeName, id: acmeID, data: acmeData},
			{name: bazName, id: bazID, data: bazData},
		} {
			filter := lookup(fixture.name)
			result, err := controller.List(ctx, privatev1.SecretsListRequest_builder{
				Filter: &filter,
				Limit:  new(int32(2)),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(result.GetTotal()).To(BeNumerically("==", 1))
			Expect(result.GetItems()).To(HaveLen(1))
			listed := result.GetItems()[0]
			Expect(listed.GetId()).To(Equal(fixture.id))
			Expect(listed.GetMetadata().GetTenant()).To(Equal(auth.SystemTenant))
			Expect(listed.GetMetadata().GetProject()).To(BeEmpty())
			Expect(listed.GetMetadata().GetName()).To(Equal(fixture.name))
			Expect(listed.GetType()).To(Equal(privatev1.SecretType_SECRET_TYPE_OPAQUE))
			hasData := len(listed.GetData()) != 0
			Expect(hasData).To(BeFalse(), "List must omit the bundle contents")

			got, err := controller.Get(ctx, privatev1.SecretsGetRequest_builder{Id: listed.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(got.GetObject().GetBackend()).To(Equal(privatev1.SecretBackend_SECRET_BACKEND_VAULT))
			Expect(bytes.Equal(got.GetObject().GetData()["config.yaml"], fixture.data)).To(
				BeTrue(), "Get must return the bytes from this exact Secret",
			)
		}

		absent := lookup(vastNFSFixtureName(acme, "vast-west"))
		missing, err := controller.List(ctx, privatev1.SecretsListRequest_builder{Filter: &absent}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(missing.GetTotal()).To(BeZero())
		Expect(missing.GetItems()).To(BeEmpty())

		filter := lookup(acmeName)
		visible, err := tenant.List(ctx, publicv1.SecretsListRequest_builder{Filter: &filter}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(visible.GetTotal()).To(BeZero())
		Expect(visible.GetItems()).To(BeEmpty())
		_, err = tenant.Get(ctx, publicv1.SecretsGetRequest_builder{Id: acmeID}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound))
	})
})

func vastNFSFixtureName(tenantName, backendName string) string {
	digest := sha256.Sum256([]byte(tenantName + "/" + backendName))
	return fmt.Sprintf("vast-nfs-%x", digest[:27])
}

func createScopedVASTBundle(ctx context.Context, secrets privatev1.SecretsClient, name string, data []byte) string {
	GinkgoHelper()

	created, err := secrets.Create(ctx, privatev1.SecretsCreateRequest_builder{
		Object: privatev1.Secret_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: auth.SystemTenant,
				Name:   name,
			}.Build(),
			Type:    privatev1.SecretType_SECRET_TYPE_OPAQUE,
			Backend: privatev1.SecretBackend_SECRET_BACKEND_VAULT,
			Data:    map[string][]byte{"config.yaml": data},
		}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred())
	id := created.GetObject().GetId()
	Expect(id).NotTo(BeEmpty())
	DeferCleanup(func(ctx context.Context) {
		_, err := secrets.Delete(ctx, privatev1.SecretsDeleteRequest_builder{Id: id}.Build())
		Expect(grpcstatus.Code(err)).To(Or(Equal(grpccodes.OK), Equal(grpccodes.NotFound)))
	})
	return id
}
