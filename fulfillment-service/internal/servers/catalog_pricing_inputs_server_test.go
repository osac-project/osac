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
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Catalog pricing inputs server", func() {
	build := func() *CatalogPricingInputsServer {
		private, err := NewPrivateCatalogPricingInputsServer().SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		server, err := NewCatalogPricingInputsServer().SetLogger(logger).SetDelegate(private).Build()
		Expect(err).ToNot(HaveOccurred())
		return server
	}

	It("requires a delegate", func() {
		_, err := NewCatalogPricingInputsServer().SetLogger(logger).Build()
		Expect(err).To(MatchError("delegate is mandatory"))
	})

	It("works with an empty inventory", func() {
		response, err := build().List(ctx, &publicv1.CatalogPricingInputsListRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetItems()).To(BeEmpty())
	})

	It("maps the aggregated inventory to the public type, with no price, provider rule, account id, or "+
		"correlation id field", func() {
		computeItemsDao, err := dao.NewGenericDAO[*privatev1.ComputeInstanceCatalogItem]().
			SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		_, err = computeItemsDao.Create().SetObject(privatev1.ComputeInstanceCatalogItem_builder{
			Id:        uuid.NewString(),
			Metadata:  privatev1.Metadata_builder{Name: "vmaas-1", Tenant: testTenant}.Build(),
			Title:     "VMaaS item",
			Published: true,
			Fields: privatev1.ComputeInstanceCatalogItemFields_builder{
				InstanceType: privatev1.InstanceTypeReferenceFieldPolicy_builder{
					Locked: privatev1.InstanceTypeReference_builder{Id: "it1", Name: "m1.small"}.Build(),
				}.Build(),
			}.Build(),
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		response, err := build().List(ctx, &publicv1.CatalogPricingInputsListRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetItems()).To(HaveLen(1))
		item := response.GetItems()[0]
		Expect(item.GetService()).To(Equal("compute_instance"))
		Expect(item.GetDimension()).To(Equal("instance_type"))
		Expect(item.GetValue()).To(Equal("m1.small"))
		Expect(item.GetAcceptsAnyValue()).To(BeFalse())
		Expect(item.GetCatalogItems()).To(HaveLen(1))
		Expect(item.GetCatalogItems()[0].GetName()).To(Equal("vmaas-1"))
		Expect(item.GetCatalogItems()[0].GetTitle()).To(Equal("VMaaS item"))
		Expect(item.GetCatalogItems()[0].GetPublished()).To(BeTrue())

		// AC6: the public message has no price, provider rule, account id, or correlation id field.
		descriptor := item.ProtoReflect().Descriptor()
		for i := range descriptor.Fields().Len() {
			name := string(descriptor.Fields().Get(i).Name())
			Expect(name).ToNot(ContainSubstring("price"))
			Expect(name).ToNot(ContainSubstring("account"))
			Expect(name).ToNot(ContainSubstring("correlation"))
		}
	})

	It("forwards offset and limit to the delegate", func() {
		computeItemsDao, err := dao.NewGenericDAO[*privatev1.ComputeInstanceCatalogItem]().
			SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		for _, name := range []string{"m1.small", "m1.large"} {
			_, err = computeItemsDao.Create().SetObject(privatev1.ComputeInstanceCatalogItem_builder{
				Id:        uuid.NewString(),
				Metadata:  privatev1.Metadata_builder{Name: uuid.NewString(), Tenant: testTenant}.Build(),
				Published: true,
				Fields: privatev1.ComputeInstanceCatalogItemFields_builder{
					InstanceType: privatev1.InstanceTypeReferenceFieldPolicy_builder{
						Locked: privatev1.InstanceTypeReference_builder{Id: "it1", Name: name}.Build(),
					}.Build(),
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
		}
		response, err := build().List(ctx, publicv1.CatalogPricingInputsListRequest_builder{
			Offset: proto.Int32(0),
			Limit:  proto.Int32(1),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetSize()).To(Equal(int32(1)))
		Expect(response.GetTotal()).To(Equal(int32(2)))
		Expect(response.GetItems()[0].GetValue()).To(Equal("m1.large")) // sorted ascending by value
	})

	It("forwards the filter to the delegate", func() {
		computeItemsDao, err := dao.NewGenericDAO[*privatev1.ComputeInstanceCatalogItem]().
			SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		_, err = computeItemsDao.Create().SetObject(privatev1.ComputeInstanceCatalogItem_builder{
			Id:        uuid.NewString(),
			Metadata:  privatev1.Metadata_builder{Name: "vmaas-1", Tenant: testTenant}.Build(),
			Published: true,
			Fields: privatev1.ComputeInstanceCatalogItemFields_builder{
				InstanceType: privatev1.InstanceTypeReferenceFieldPolicy_builder{
					Locked: privatev1.InstanceTypeReference_builder{Id: "it1", Name: "m1.small"}.Build(),
				}.Build(),
			}.Build(),
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		response, err := build().List(ctx, publicv1.CatalogPricingInputsListRequest_builder{
			Filter: proto.String(`this.service == "cluster"`),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetItems()).To(BeEmpty())
	})
})
