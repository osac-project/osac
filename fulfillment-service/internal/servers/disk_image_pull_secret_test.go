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
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("isEmptySecretReference", func() {
	DescribeTable("emptiness",
		func(ref *privatev1.SecretReference, want bool) {
			Expect(isEmptySecretReference(ref)).To(Equal(want))
		},
		Entry("nil", nil, true),
		Entry("empty message", &privatev1.SecretReference{}, true),
		Entry("id only", privatev1.SecretReference_builder{Id: "sec-1"}.Build(), false),
		Entry("name only", privatev1.SecretReference_builder{Name: "pull"}.Build(), false),
		Entry("shared only", privatev1.SecretReference_builder{Shared: true}.Build(), false),
		Entry("project only", privatev1.SecretReference_builder{Project: "creds"}.Build(), false),
		Entry("tenant only", privatev1.SecretReference_builder{Tenant: "acme"}.Build(), false),
	)
})

var _ = Describe("rejectDiskImagePullSecretFilter", func() {
	DescribeTable("filter references",
		func(filter string, wantErr bool) {
			err := rejectDiskImagePullSecretFilter(filter)
			if !wantErr {
				Expect(err).ToNot(HaveOccurred())
				return
			}
			Expect(err).To(HaveOccurred())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("spec.pull_secret"))
		},
		Entry("empty", "", false),
		Entry("id only", "this.id == 'di-1'", false),
		Entry("name", "this.metadata.name == 'img'", false),
		Entry("pull_secret id", "this.spec.pull_secret.id == 'sec-1'", true),
		Entry("pull_secret presence", "this.spec.pull_secret != null", true),
		Entry("nested under and", "this.metadata.name == 'x' && this.spec.pull_secret.id == 's'", true),
	)
})

var _ = Describe("redactDiskImagePullSecret", func() {
	It("clears pull_secret when the Secret id is not discoverable", func() {
		secretsDao, err := dao.NewGenericDAO[*privatev1.Secret]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		image := privatev1.DiskImage_builder{
			Id: "disk-image-redact",
			Spec: privatev1.DiskImageSpec_builder{
				PullSecret: privatev1.SecretReference_builder{
					Id:   "missing-pull-secret",
					Name: "missing-pull-secret",
				}.Build(),
			}.Build(),
		}.Build()

		redactDiskImagePullSecret(ctx, logger, secretsDao, image)
		Expect(image.GetSpec().GetPullSecret()).To(BeNil())
	})

	It("keeps pull_secret when the Secret is discoverable", func() {
		secretsDao, err := dao.NewGenericDAO[*privatev1.Secret]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		created, err := secretsDao.Create().SetObject(privatev1.Secret_builder{
			Type: privatev1.SecretType_SECRET_TYPE_PULL_SECRET,
			Metadata: privatev1.Metadata_builder{
				Name:   "visible-pull",
				Tenant: testTenant,
			}.Build(),
			Data: map[string][]byte{".dockerconfigjson": []byte(`{"auths":{}}`)},
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		image := privatev1.DiskImage_builder{
			Id: "disk-image-keep",
			Spec: privatev1.DiskImageSpec_builder{
				PullSecret: privatev1.SecretReference_builder{
					Id:   created.GetObject().GetId(),
					Name: created.GetObject().GetMetadata().GetName(),
				}.Build(),
			}.Build(),
		}.Build()

		redactDiskImagePullSecret(ctx, logger, secretsDao, image)
		Expect(image.GetSpec().GetPullSecret().GetId()).To(Equal(created.GetObject().GetId()))
	})
})
