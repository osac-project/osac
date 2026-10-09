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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("full resource reference resolution", func() {
	const ownerTenant = "tenant-a"

	var templatesDao *dao.GenericDAO[*privatev1.ClusterTemplate]
	var target *privatev1.ClusterTemplate

	BeforeEach(func() {
		createTenant("tenant-b")
		var err error
		templatesDao, err = dao.NewGenericDAO[*privatev1.ClusterTemplate]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		target = privatev1.ClusterTemplate_builder{
			Id: "target-id",
			Metadata: privatev1.Metadata_builder{
				Name:   "target",
				Tenant: ownerTenant,
			}.Build(),
		}.Build()
	})

	It("rejects an ID-only reference to another tenant", func() {
		target.GetMetadata().SetTenant("tenant-b")
		_, err := templatesDao.Create().SetObject(target).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		ref := privatev1.ClusterTemplateReference_builder{Id: target.GetId()}.Build()

		_, err = resolveFullResourceReference(
			ctx, templatesDao, referenceScope{tenant: ownerTenant}, ref,
			"cluster template", "", grpccodes.InvalidArgument,
		)

		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err).To(MatchError(ContainSubstring("owning tenant or shared tenant")))
	})

	It("rejects a name reference explicitly selecting another tenant", func() {
		target.GetMetadata().SetTenant("tenant-b")
		_, err := templatesDao.Create().SetObject(target).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		ref := privatev1.ClusterTemplateReference_builder{
			Name:   target.GetMetadata().GetName(),
			Tenant: "tenant-b",
		}.Build()

		_, err = resolveFullResourceReference(
			ctx, templatesDao, referenceScope{tenant: ownerTenant}, ref,
			"cluster template", "", grpccodes.InvalidArgument,
		)

		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err).To(MatchError(ContainSubstring("owning tenant or shared tenant")))
	})

	It("allows a shared ID-only reference", func() {
		target.GetMetadata().SetTenant(auth.SharedTenant)
		_, err := templatesDao.Create().SetObject(target).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		ref := privatev1.ClusterTemplateReference_builder{Id: target.GetId()}.Build()

		_, err = resolveFullResourceReference(
			ctx, templatesDao, referenceScope{tenant: ownerTenant}, ref,
			"cluster template", "", grpccodes.InvalidArgument,
		)

		Expect(err).ToNot(HaveOccurred())
	})
})
