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

package it

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	metadataPrivateLabelKey       = "admin.osac.openshift.io/integration"
	metadataPrivateAnnotationKey  = "admin.osac.openshift.io/integration"
	metadataOrdinaryLabelKey      = "example.com/integration"
	metadataOrdinaryAnnotationKey = "example.com/integration"
)

var _ = Describe("reserved metadata privacy fixtures", Label("metadata"), func() {
	It("keeps private metadata stored and hides it from tenant REST and CLI reads", func(ctx context.Context) {
		tenantAdmin := createCatalogItemTenantAdminIdentityFixture(ctx)
		tenantUser := createCatalogItemMemberIdentityFixture(ctx, tenantAdmin.TenantID)
		secondTenantUser, err := metadataIdentityForExistingUser(ctx, "charles", usersPassword, "sales")
		Expect(err).NotTo(HaveOccurred())
		accounts := metadataAccountsFromTool(tool, tenantAdmin, tenantUser, secondTenantUser)

		fixture, err := newComputeInstanceMetadataFixture(ctx, accounts)
		Expect(err).NotTo(HaveOccurred())
		before, err := fixture.privateSnapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(before.GetMetadata().GetTenant()).To(Equal(accounts.tenantUser.TenantID))

		ownerReference, hasOwnerReference := before.GetMetadata().GetAnnotations()["osac.openshift.io/owner-reference"]
		By("writing reserved and ordinary metadata through the private API")
		_, err = fixture.updateMetadata(ctx,
			map[string]string{
				metadataPrivateLabelKey:  "stored-label",
				metadataOrdinaryLabelKey: "visible-label",
			},
			map[string]string{
				metadataPrivateAnnotationKey:  "stored-annotation",
				metadataOrdinaryAnnotationKey: "visible-annotation",
			},
		)
		Expect(err).NotTo(HaveOccurred())

		By("checking both tenant identities through the deployed REST gateway")
		for _, reader := range []struct {
			name     string
			identity metadataTestIdentity
		}{
			{name: "Tenant Admin", identity: accounts.tenantAdmin},
			{name: "Tenant User", identity: accounts.tenantUser},
		} {
			publicObject, statusCode, body, readErr := readComputeInstanceREST(ctx, reader.identity.HTTPClient, fixture.id)
			Expect(readErr).NotTo(HaveOccurred(), reader.name)
			Expect(statusCode).To(Equal(http.StatusOK), reader.name+": "+string(body))
			expectMetadataProjection(publicObject, before, accounts.tenantUser.TenantID)
		}

		By("checking the second tenant cannot read the instance")
		_, statusCode, body, err := readComputeInstanceREST(ctx, accounts.secondTenantUser.HTTPClient, fixture.id)
		Expect(err).NotTo(HaveOccurred())
		Expect(statusCode).To(Equal(http.StatusNotFound), string(body))

		By("checking both tenant identities through the deployed CLI")
		for _, reader := range []struct {
			name     string
			identity metadataTestIdentity
		}{
			{name: "Tenant Admin", identity: accounts.tenantAdmin},
			{name: "Tenant User", identity: accounts.tenantUser},
		} {
			publicObject := readComputeInstanceCLI(ctx, reader.identity, fixture.id)
			expectMetadataProjection(publicObject, before, accounts.tenantUser.TenantID)
		}

		By("confirming private storage and fixture ownership metadata are unchanged")
		after, err := fixture.privateSnapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(after.GetMetadata().GetTenant()).To(Equal(before.GetMetadata().GetTenant()))
		afterOwnerReference, afterHasOwnerReference := after.GetMetadata().GetAnnotations()["osac.openshift.io/owner-reference"]
		Expect(afterHasOwnerReference).To(Equal(hasOwnerReference))
		Expect(afterOwnerReference).To(Equal(ownerReference))
		Expect(after.GetMetadata().GetLabels()).To(HaveKeyWithValue(metadataPrivateLabelKey, "stored-label"))
		Expect(after.GetMetadata().GetAnnotations()).To(HaveKeyWithValue(metadataPrivateAnnotationKey, "stored-annotation"))
	})
})

func readComputeInstanceREST(
	ctx context.Context,
	client *http.Client,
	id string,
) (*publicv1.ComputeInstance, int, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"/api/fulfillment/v1/compute_instances/"+id, nil)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("build ComputeInstance REST request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("send ComputeInstance REST request: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, response.StatusCode, nil, fmt.Errorf("read ComputeInstance REST response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return nil, response.StatusCode, body, nil
	}
	object := &publicv1.ComputeInstance{}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(body, object); err != nil {
		return nil, response.StatusCode, body, fmt.Errorf("decode ComputeInstance REST response: %w", err)
	}
	return object, response.StatusCode, body, nil
}

func readComputeInstanceCLI(ctx context.Context, identity metadataTestIdentity, id string) *publicv1.ComputeInstance {
	GinkgoHelper()
	homeDir, err := tool.NewCLIHomeDir()
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { Expect(os.RemoveAll(homeDir)).To(Succeed()) })
	_, stderr, exitCode := tool.LoginCLI(ctx, homeDir, identity.Username, identity.Password)
	Expect(exitCode).To(Equal(0), "CLI login failed: %s", stderr)
	output, stderr, exitCode := tool.RunCLI(ctx, homeDir, "get", "computeinstance", id, "-o", "json")
	Expect(exitCode).To(Equal(0), "CLI get failed: %s", stderr)
	object := &publicv1.ComputeInstance{}
	Expect((protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal([]byte(output), object)).To(Succeed())
	return object
}

func expectMetadataProjection(object *publicv1.ComputeInstance, privateObject *privatev1.ComputeInstance, tenantID string) {
	Expect(object).NotTo(BeNil())
	Expect(object.GetMetadata().GetTenant()).To(Equal(tenantID))
	Expect(object.GetMetadata().GetLabels()).To(HaveKeyWithValue(metadataOrdinaryLabelKey, "visible-label"))
	Expect(object.GetMetadata().GetAnnotations()).To(HaveKeyWithValue(metadataOrdinaryAnnotationKey, "visible-annotation"))
	Expect(object.GetMetadata().GetLabels()).NotTo(HaveKey(metadataPrivateLabelKey))
	Expect(object.GetMetadata().GetAnnotations()).NotTo(HaveKey(metadataPrivateAnnotationKey))
	privateOwnerReference := privateObject.GetMetadata().GetAnnotations()["osac.openshift.io/owner-reference"]
	publicOwnerReference, hasPublicOwnerReference := object.GetMetadata().GetAnnotations()["osac.openshift.io/owner-reference"]
	if privateOwnerReference == "" {
		Expect(hasPublicOwnerReference).To(BeFalse())
	} else {
		Expect(publicOwnerReference).To(Equal(privateOwnerReference))
	}
}
