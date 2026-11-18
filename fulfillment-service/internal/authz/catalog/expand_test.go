/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package catalog

import (
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
)

// The expansion tests use a small controlled catalog so that the expected results don't depend on
// the real API surface:
//
//   - `osac.public.alphas` provides `create`, `get` and `list`.
//   - `osac.public.betas` provides `delete` and `get`.
//   - `osac.private.gammas` provides `get`.
var _ = Describe("Rule expansion", func() {
	var permissionCatalog *Catalog

	BeforeEach(func() {
		var err error
		permissionCatalog, err = Build(newStubRegistry(
			stubFile("alphas.proto", "osac.public.v1", map[string][]string{
				"Alphas": {"Create", "Get", "List"},
			}),
			stubFile("betas.proto", "osac.public.v1", map[string][]string{
				"Betas": {"Delete", "Get"},
			}),
			stubFile("gammas.proto", "osac.private.v1", map[string][]string{
				"Gammas": {"Get"},
			}),
		))
		Expect(err).ToNot(HaveOccurred())
	})

	DescribeTable("Expands rules",
		func(service string, methods []string, expected []string) {
			granted, err := permissionCatalog.Expand(service, methods)
			Expect(err).ToNot(HaveOccurred())
			Expect(granted).To(Equal(expected))
		},
		Entry("The full wildcard",
			AllServicesWildcard, []string{AllMethodsWildcard}, []string{
				"osac.private.v1.Gammas/Get",
				"osac.public.v1.Alphas/Create",
				"osac.public.v1.Alphas/Get",
				"osac.public.v1.Alphas/List",
				"osac.public.v1.Betas/Delete",
				"osac.public.v1.Betas/Get",
			}),
		Entry("The public layer wildcard",
			PublicLayerWildcard, []string{AllMethodsWildcard}, []string{
				"osac.public.v1.Alphas/Create",
				"osac.public.v1.Alphas/Get",
				"osac.public.v1.Alphas/List",
				"osac.public.v1.Betas/Delete",
				"osac.public.v1.Betas/Get",
			}),
		Entry("The private layer wildcard",
			PrivateLayerWildcard, []string{AllMethodsWildcard}, []string{
				"osac.private.v1.Gammas/Get",
			}),
		Entry("The method wildcard",
			"osac.public.alphas", []string{AllMethodsWildcard}, []string{
				"osac.public.v1.Alphas/Create",
				"osac.public.v1.Alphas/Get",
				"osac.public.v1.Alphas/List",
			}),
		Entry("Explicit methods",
			"osac.public.alphas", []string{"get", "create"}, []string{
				"osac.public.v1.Alphas/Create",
				"osac.public.v1.Alphas/Get",
			}),
		Entry("A method across a layer wildcard",
			PublicLayerWildcard, []string{"get"}, []string{
				"osac.public.v1.Alphas/Get",
				"osac.public.v1.Betas/Get",
			}),
		Entry("A method that only some services of a layer provide",
			PublicLayerWildcard, []string{"delete"}, []string{
				"osac.public.v1.Betas/Delete",
			}),
		Entry("A method wildcard mixed with explicit methods",
			"osac.public.betas", []string{AllMethodsWildcard, "get"}, []string{
				"osac.public.v1.Betas/Delete",
				"osac.public.v1.Betas/Get",
			}),
	)

	DescribeTable("Rejects invalid rules",
		func(service string, methods []string, expectedError string) {
			_, err := permissionCatalog.Expand(service, methods)
			Expect(err).To(MatchError(expectedError))
		},
		Entry("Unknown service",
			"osac.public.nope", []string{"get"}, `unknown service`),
		Entry("Malformed service wildcard",
			"osac.public.alph*", []string{"get"}, `unknown service`),
		Entry("Capitalized service name",
			"osac.public.Alphas", []string{"get"}, `unknown service`),
		Entry("Unknown method",
			"osac.public.alphas", []string{"fake"}, `unknown method`),
		Entry("A method of a different service",
			"osac.public.alphas", []string{"delete"}, `unknown method`),
		Entry("A method that no service of the layer provides",
			PublicLayerWildcard, []string{"fake"}, `unknown method`),
		Entry("An empty method list",
			"osac.public.alphas", []string{}, "at least one method is required"),
	)

	Describe("Rule validation", func() {
		DescribeTable("Accepts valid rules",
			func(service string, methods []string) {
				err := permissionCatalog.ValidateRule(service, methods)
				Expect(err).ToNot(HaveOccurred())
			},
			Entry("A concrete service and explicit methods", "osac.public.alphas", []string{"get", "list"}),
			Entry("The full wildcard", AllServicesWildcard, []string{AllMethodsWildcard}),
			Entry("A layer wildcard", PublicLayerWildcard, []string{"get"}),
			Entry("A method that only some services provide", PublicLayerWildcard, []string{"delete"}),
		)

		DescribeTable("Rejects invalid rules",
			func(service string, methods []string, expectedError string) {
				err := permissionCatalog.ValidateRule(service, methods)
				Expect(err).To(MatchError(expectedError))
			},
			Entry("Unknown service",
				"osac.public.nope", []string{"get"}, `unknown service`),
			Entry("Unknown method",
				"osac.public.alphas", []string{"fake"}, `unknown method`),
			Entry("An empty method list",
				"osac.public.alphas", []string{}, "at least one method is required"),
		)
	})
})
