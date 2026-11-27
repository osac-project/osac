/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

   http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package servers

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Documents server", func() {
	var (
		privateServer *PrivateDocumentsServer
		publicServer  *DocumentsServer
	)

	ptr := func(value string) *string {
		return &value
	}

	BeforeEach(func() {
		var err error
		privateServer, err = NewPrivateDocumentsServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		publicServer, err = NewDocumentsServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	newDocument := func(title string) *privatev1.Document {
		fallback := "en-US"
		return privatev1.Document_builder{
			Metadata: privatev1.Metadata_builder{
				Name:        "guide",
				Tenant:      testTenant,
				Description: ptr("document-id"),
			}.Build(),
			Spec: privatev1.DocumentSpec_builder{
				Owner: privatev1.DocumentOwner_builder{
					Project: privatev1.DocumentProjectOwner_builder{
						Id:   "project-id",
						Name: "project",
					}.Build(),
				}.Build(),
				Fallback: &fallback,
				Variants: map[string]*privatev1.DocumentVariant{
					"en-US": privatev1.DocumentVariant_builder{
						Title:   title,
						Content: []byte("# Guide"),
					}.Build(),
				},
			}.Build(),
		}.Build()
	}

	It("supports private CRUD and Signal", func() {
		createResponse, err := privateServer.Create(ctx, privatev1.DocumentsCreateRequest_builder{
			Object: newDocument("Guide"),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		created := createResponse.GetObject()
		Expect(created.GetId()).ToNot(BeEmpty())
		Expect(created.GetMetadata().GetDescription()).To(Equal("document-id"))
		Expect(created.GetSpec().GetFallback()).To(Equal("en-US"))
		Expect(created.GetSpec().GetVariants()["en-US"].GetTitle()).To(Equal("Guide"))

		getResponse, err := privateServer.Get(ctx, privatev1.DocumentsGetRequest_builder{
			Id: created.GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetSpec().GetVariants()["en-US"].GetTitle()).To(Equal("Guide"))

		listResponse, err := privateServer.List(ctx, privatev1.DocumentsListRequest_builder{
			Filter: ptr("this.metadata.name == 'guide'"),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse.GetItems()).To(HaveLen(1))
		Expect(listResponse.GetItems()[0].GetId()).To(Equal(created.GetId()))

		updated := newDocument("Updated guide")
		updated.SetId(created.GetId())
		updateResponse, err := privateServer.Update(ctx, privatev1.DocumentsUpdateRequest_builder{
			Object: updated,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(updateResponse.GetObject().GetSpec().GetVariants()["en-US"].GetTitle()).To(Equal("Updated guide"))
		Expect(updateResponse.GetObject().GetMetadata().GetVersion()).To(BeNumerically(">", created.GetMetadata().GetVersion()))

		deleteResponse, err := privateServer.Delete(ctx, privatev1.DocumentsDeleteRequest_builder{
			Id: created.GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(deleteResponse).ToNot(BeNil())

		_, err = privateServer.Signal(ctx, privatev1.DocumentsSignalRequest_builder{
			Id: created.GetId(),
		}.Build())
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
	})

	It("supports public List and Get", func() {
		createResponse, err := privateServer.Create(ctx, privatev1.DocumentsCreateRequest_builder{
			Object: newDocument("Guide"),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		created := createResponse.GetObject()

		getResponse, err := publicServer.Get(ctx, publicv1.DocumentsGetRequest_builder{
			Id: created.GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetSpec().GetVariants()["en-US"].GetTitle()).To(Equal("Guide"))

		listResponse, err := publicServer.List(ctx, publicv1.DocumentsListRequest_builder{
			Filter: ptr("this.metadata.name == 'guide'"),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse.GetItems()).To(HaveLen(1))
		Expect(listResponse.GetItems()[0].GetId()).To(Equal(created.GetId()))
	})
})
