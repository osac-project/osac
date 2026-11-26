/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package authz

import (
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/osac-project/osac/fulfillment-service/internal/authz/catalog"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Delegation coverage", func() {
	const clusters = "osac.public.clusters"
	var permissionCatalog *catalog.Catalog

	BeforeEach(func() {
		var err error
		permissionCatalog, err = catalog.Build(protoregistry.GlobalFiles)
		Expect(err).NotTo(HaveOccurred())
	})

	It("requires every requested method", func() {
		grants := &Grants{Methods: []string{"osac.public.v1.Clusters/Get"}}
		covered, err := grants.Covers(permissionCatalog, clusters, []string{"get"})
		Expect(err).NotTo(HaveOccurred())
		Expect(covered).To(BeTrue())
		Expect(grants.Allows(publicv1.Clusters_Get_FullMethodName)).To(BeTrue())

		covered, err = grants.Covers(permissionCatalog, clusters, []string{"get", "list"})
		Expect(err).NotTo(HaveOccurred())
		Expect(covered).To(BeFalse())
	})

	DescribeTable("requires wildcard provenance even when every current method is granted",
		func(heldService string, heldMethods []string, requestedService string, requestedMethods []string, want bool) {
			methods, err := permissionCatalog.Expand(requestedService, requestedMethods)
			Expect(err).NotTo(HaveOccurred())
			grants := &Grants{Methods: methods, Bindings: []BindingGrant{{Rules: []*privatev1.PermissionRule{
				privatev1.PermissionRule_builder{Service: heldService, Methods: heldMethods}.Build(),
			}}}}

			covered, err := grants.Covers(permissionCatalog, requestedService, requestedMethods)
			Expect(err).NotTo(HaveOccurred())
			Expect(covered).To(Equal(want))
		},
		Entry("explicit methods do not cover a method wildcard", clusters, []string{"get", "list"}, clusters, []string{"*"}, false),
		Entry("a service wildcard is confined to its service", clusters, []string{"*"}, catalog.PublicLayerWildcard, []string{"get"}, false),
		Entry("a layer wildcard covers a narrower service", catalog.PublicLayerWildcard, []string{"*"}, clusters, []string{"*"}, true),
		Entry("a layer wildcard is confined to its API layer", catalog.PublicLayerWildcard, []string{"*"}, catalog.PrivateLayerWildcard, []string{"*"}, false),
		Entry("a layer wildcard retains its method limits", catalog.PublicLayerWildcard, []string{"get", "list"}, catalog.PublicLayerWildcard, []string{"get", "create"}, false),
		Entry("a global wildcard covers a layer wildcard", catalog.AllServicesWildcard, []string{"*"}, catalog.PrivateLayerWildcard, []string{"*"}, true),
	)
})
