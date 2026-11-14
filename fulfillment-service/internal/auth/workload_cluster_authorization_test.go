/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package auth

import (
	"context"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = Describe("WorkloadCluster authorization", func() {
	It("grants only the four public operations to cloud-provider-admin", func(ctx context.Context) {
		evaluator, err := NewEvaluator().AddEmergencyServiceAccounts([]string{}).Build()
		Expect(err).ToNot(HaveOccurred())

		admin := &AuthContext{
			Username:     "provider-admin",
			AuthMethod:   "jwt",
			Organization: []any{"provider"},
			RealmAccess:  map[string]any{"roles": []any{"cloud-provider-admin"}},
		}
		tenantAdmin := &AuthContext{
			Username:     "tenant-admin",
			AuthMethod:   "jwt",
			Organization: []any{"tenant"},
			RealmAccess:  map[string]any{"roles": []any{"tenant-admin"}},
		}
		reader := &AuthContext{
			Username:     "provider-reader",
			AuthMethod:   "jwt",
			Organization: []any{"provider"},
			RealmAccess:  map[string]any{"roles": []any{"cloud-provider-reader"}},
		}

		methods := []string{
			"/osac.public.v1.WorkloadClusters/List",
			"/osac.public.v1.WorkloadClusters/Get",
			"/osac.public.v1.WorkloadClusters/Create",
			"/osac.public.v1.WorkloadClusters/Update",
		}
		for _, method := range methods {
			decision, evalErr := evaluator.Evaluate(ctx, admin, method)
			Expect(evalErr).ToNot(HaveOccurred())
			Expect(decision.Allowed).To(BeTrue(), method)

			for _, denied := range []*AuthContext{tenantAdmin, reader} {
				decision, evalErr = evaluator.Evaluate(ctx, denied, method)
				Expect(evalErr).ToNot(HaveOccurred())
				Expect(decision.Allowed).To(BeFalse(), method)
			}
		}

		decision, err := evaluator.Evaluate(ctx, admin, "/osac.public.v1.WorkloadClusters/Delete")
		Expect(err).ToNot(HaveOccurred())
		Expect(decision.Allowed).To(BeFalse())
	})
})
