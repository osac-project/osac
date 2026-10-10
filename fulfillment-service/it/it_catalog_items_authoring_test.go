/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package it

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

var _ = Describe("Creating and updating Catalog Items", Label("catalog-items"), func() {
	DescribeTable("rejects invalid policies on Create and Update without persisting changes", func(ctx context.Context, name string, makePolicy func() *publicv1.TemplateParameterPolicy) {
		policy := makePolicy()
		template := createCatalogItemComputeInstanceTemplateFixture(ctx, nil, computeInstanceCatalogItemParameterDefinitions())
		client := publicv1.NewComputeInstanceCatalogItemsClient(tool.ExternalView().AdminConn())
		item := createComputeInstanceCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.ComputeInstanceCatalogItem_builder{
			Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
			Template: publicv1.ComputeInstanceTemplateReference_builder{Id: template}.Build(),
		}.Build())
		before, err := client.Get(ctx, publicv1.ComputeInstanceCatalogItemsGetRequest_builder{Id: item.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())

		createName := catalogItemFixtureName()
		_, err = client.Create(ctx, publicv1.ComputeInstanceCatalogItemsCreateRequest_builder{
			Object: publicv1.ComputeInstanceCatalogItem_builder{
				Metadata:           publicv1.Metadata_builder{Name: createName}.Build(),
				Template:           publicv1.ComputeInstanceTemplateReference_builder{Id: template}.Build(),
				TemplateParameters: map[string]*publicv1.TemplateParameterPolicy{name: policy},
			}.Build(),
		}.Build())
		expectCatalogItemStatusCode(err, codes.InvalidArgument)
		listed, err := client.List(ctx, publicv1.ComputeInstanceCatalogItemsListRequest_builder{
			Filter: new("this.metadata.name == '" + createName + "'"),
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(listed.GetItems()).To(BeEmpty())

		_, err = client.Update(ctx, publicv1.ComputeInstanceCatalogItemsUpdateRequest_builder{
			Object: publicv1.ComputeInstanceCatalogItem_builder{
				Id:                 item.GetId(),
				Title:              "should not persist",
				TemplateParameters: map[string]*publicv1.TemplateParameterPolicy{name: policy},
			}.Build(),
			UpdateMask: catalogItemUpdateMask("title", "template_parameters"),
		}.Build())
		expectCatalogItemStatusCode(err, codes.InvalidArgument)
		after, err := client.Get(ctx, publicv1.ComputeInstanceCatalogItemsGetRequest_builder{Id: item.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(proto.Equal(after.GetObject(), before.GetObject())).To(BeTrue())
	},
		Entry("unknown parameter", "unknown", func() *publicv1.TemplateParameterPolicy {
			return publicv1.TemplateParameterPolicy_builder{Locked: catalogItemParameterValue(wrapperspb.Bool(true))}.Build()
		}),
		Entry("string instead of the required boolean", "enabled", func() *publicv1.TemplateParameterPolicy {
			return publicv1.TemplateParameterPolicy_builder{Locked: catalogItemParameterValue(wrapperspb.String("wrong"))}.Build()
		}),
		Entry("invalid encoded boolean", "enabled", func() *publicv1.TemplateParameterPolicy {
			return publicv1.TemplateParameterPolicy_builder{Locked: &anypb.Any{
				TypeUrl: "type.googleapis.com/google.protobuf.BoolValue", Value: []byte{0xff},
			}}.Build()
		}),
		Entry("JSON-encoded string instead of the required boolean", "enabled", func() *publicv1.TemplateParameterPolicy {
			return publicv1.TemplateParameterPolicy_builder{
				Locked: catalogItemParameterValue(structpb.NewStringValue("value")),
			}.Build()
		}),
	)

	DescribeTable("rejects catalog items created without a Template", func(ctx context.Context, create func(context.Context) error) {
		expectCatalogItemStatusCode(create(ctx), codes.InvalidArgument)
	},
		Entry("compute instance", func(ctx context.Context) error {
			_, err := publicv1.NewComputeInstanceCatalogItemsClient(tool.ExternalView().AdminConn()).Create(ctx, publicv1.ComputeInstanceCatalogItemsCreateRequest_builder{
				Object: publicv1.ComputeInstanceCatalogItem_builder{Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build()}.Build(),
			}.Build())
			return err
		}),
		Entry("cluster", func(ctx context.Context) error {
			_, err := publicv1.NewClusterCatalogItemsClient(tool.ExternalView().AdminConn()).Create(ctx, publicv1.ClusterCatalogItemsCreateRequest_builder{
				Object: publicv1.ClusterCatalogItem_builder{Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build()}.Build(),
			}.Build())
			return err
		}),
		Entry("bare metal instance", func(ctx context.Context) error {
			_, err := publicv1.NewBareMetalInstanceCatalogItemsClient(tool.ExternalView().AdminConn()).Create(ctx, publicv1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: publicv1.BareMetalInstanceCatalogItem_builder{Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build()}.Build(),
			}.Build())
			return err
		}),
	)
})
