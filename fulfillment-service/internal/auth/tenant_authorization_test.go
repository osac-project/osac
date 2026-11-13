/*
Copyright (c) 2026 Red Hat Inc.

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
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Tenant liveness authorization boundary", func() {
	It("fails closed when the authoritative resolver fails", func() {
		evaluator, err := NewEvaluator().SetTenantResolver(func(context.Context, []string) ([]string, error) {
			return nil, errors.New("database unavailable")
		}).Build()
		Expect(err).ToNot(HaveOccurred())
		decision, err := evaluator.Evaluate(context.Background(), &AuthContext{
			AuthMethod: "jwt", Username: "alice", Organization: []any{"tenant-a"},
		}, "/osac.public.v1.ComputeInstances/List")
		Expect(err).To(MatchError(ContainSubstring("database unavailable")))
		Expect(decision).To(BeNil())
	})

	It("does not resolve or revoke universal controller and administrator scope", func() {
		evaluator, err := NewEvaluator().SetTenantResolver(func(context.Context, []string) ([]string, error) {
			Fail("platform scope must not depend on tenant liveness")
			return nil, nil
		}).Build()
		Expect(err).ToNot(HaveOccurred())
		for _, user := range []string{"service-account-osac-admin", "service-account-osac-controller"} {
			decision, err := evaluator.Evaluate(context.Background(), &AuthContext{
				AuthMethod: "jwt", Username: user,
			}, "/osac.private.v1.Tenants/Update")
			Expect(err).ToNot(HaveOccurred())
			Expect(decision.Allowed).To(BeTrue())
			Expect(decision.SubjectTenants).To(Equal([]string{"*"}))
		}
	})

	It("denies targeted project authorization if that target was revoked", func() {
		evaluator, err := NewEvaluator().SetTenantResolver(func(_ context.Context, names []string) ([]string, error) {
			Expect(names).To(ConsistOf("tenant-a", "tenant-b"))
			return []string{"tenant-b"}, nil
		}).Build()
		Expect(err).ToNot(HaveOccurred())
		decision, err := evaluator.Evaluate(context.Background(), &AuthContext{
			AuthMethod: "jwt", Username: "alice", Tenant: "tenant-a", Name: "project",
			Organization: map[string]any{
				"tenant-a": map[string]any{"groups": []any{"/project/system:managers"}},
				"tenant-b": map[string]any{"groups": []any{}},
			},
		}, "/osac.public.v1.Projects/Update")
		Expect(err).ToNot(HaveOccurred())
		Expect(decision.Allowed).To(BeFalse())
		Expect(decision.SubjectTenants).To(Equal([]string{"tenant-b"}))
	})
})
