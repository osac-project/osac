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

package servers

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func computeInstanceCatalogItemWithSSHPolicy(tenant, policy string) (*privatev1.ComputeInstanceCatalogItem, error) {
	body := fmt.Sprintf(`{"metadata":{"tenant":%q},"fields":{"sshKey":%s}}`, tenant, policy)
	item := &privatev1.ComputeInstanceCatalogItem{}
	if err := protojson.Unmarshal([]byte(body), item); err != nil {
		return nil, err
	}
	return item, nil
}

func fullSecretReferencePolicyFromJSON(value string) (*privatev1.FullSecretReferenceFieldPolicy, error) {
	policy := &privatev1.FullSecretReferenceFieldPolicy{}
	if err := protojson.Unmarshal([]byte(value), policy); err != nil {
		return nil, err
	}
	return policy, nil
}

var _ = Describe("CatalogItem SSH key policy", func() {
	It("rejects shared Secret references before accessing the Secret DAO", func() {
		policy, err := fullSecretReferencePolicyFromJSON(
			`{"editable":{"defaultValue":{"name":"catalog-key","project":"apps","shared":true}}}`,
		)
		Expect(err).NotTo(HaveOccurred())

		err = validateCatalogItemSSHKeyPolicy(ctx, referenceScope{tenant: testTenant}, policy, nil)
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(status.Convert(err).Message()).To(ContainSubstring("tenant-scoped"))
	})

	It("rejects fixed SSH keys on shared CatalogItems before lookup", func() {
		for _, value := range []string{
			`{"locked":{"name":"catalog-key","project":"apps"}}`,
			`{"editable":{"defaultValue":{"name":"catalog-key","project":"apps"}}}`,
		} {
			policy, err := fullSecretReferencePolicyFromJSON(value)
			Expect(err).NotTo(HaveOccurred())
			Expect(status.Code(validateCatalogItemSSHKeyPolicy(ctx, referenceScope{tenant: auth.SharedTenant}, policy, nil))).
				To(Equal(codes.InvalidArgument))
		}
	})

	It("allows a shared CatalogItem to leave the SSH key editable without a default", func() {
		policy, err := fullSecretReferencePolicyFromJSON(`{"editable":{}}`)
		Expect(err).NotTo(HaveOccurred())

		Expect(validateCatalogItemSSHKeyPolicy(ctx, referenceScope{tenant: auth.SharedTenant}, policy, nil)).
			To(Succeed())
	})
})
