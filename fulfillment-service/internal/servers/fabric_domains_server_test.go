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
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Fabric domains public metadata visibility", func() {
	It("filters and returns metadata using the public view", func() {
		domainsDAO, err := dao.NewGenericDAO[*privatev1.FabricDomain]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		var reservedObject *privatev1.FabricDomain
		for _, object := range []struct {
			name        string
			labels      map[string]string
			annotations map[string]string
			reserved    bool
		}{
			{
				name: fmt.Sprintf("public-map-filter-%s", uuid.New()[24:32]),
				labels: map[string]string{
					"ordinary":                        "visible",
					"admin.osac.openshift.io/managed": "system",
				},
				annotations: map[string]string{
					"ordinary":                            "visible",
					"provider.admin.osac.openshift.io/id": "provider",
				},
				reserved: true,
			},
			{
				name:        fmt.Sprintf("public-map-filter-%s", uuid.New()[24:32]),
				labels:      map[string]string{"ordinary": "visible"},
				annotations: map[string]string{"ordinary": "visible"},
			},
		} {
			created, err := domainsDAO.Create().SetObject(privatev1.FabricDomain_builder{
				Metadata: privatev1.Metadata_builder{
					Name:        object.name,
					Tenant:      testTenant,
					Labels:      object.labels,
					Annotations: object.annotations,
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			if object.reserved {
				reservedObject = created.GetObject()
			}
		}

		publicServer, err := NewFabricDomainsServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		filter := "this.metadata.labels == this.metadata.annotations"
		response, err := publicServer.List(ctx, publicv1.FabricDomainsListRequest_builder{
			Filter: new(filter),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetTotal()).To(Equal(int32(2)))
		Expect(response.GetItems()).To(HaveLen(2))
		for _, item := range response.GetItems() {
			Expect(item.GetMetadata().GetLabels()).To(Equal(map[string]string{"ordinary": "visible"}))
			Expect(item.GetMetadata().GetAnnotations()).To(Equal(map[string]string{"ordinary": "visible"}))
		}

		getResponse, err := publicServer.Get(ctx, publicv1.FabricDomainsGetRequest_builder{
			Id: reservedObject.GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetMetadata().GetLabels()).To(Equal(map[string]string{"ordinary": "visible"}))
		Expect(getResponse.GetObject().GetMetadata().GetAnnotations()).To(Equal(map[string]string{"ordinary": "visible"}))

		stored, err := domainsDAO.Get().SetId(reservedObject.GetId()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(stored.GetObject().GetMetadata().GetLabels()).To(Equal(map[string]string{
			"ordinary":                        "visible",
			"admin.osac.openshift.io/managed": "system",
		}))
		Expect(stored.GetObject().GetMetadata().GetAnnotations()).To(Equal(map[string]string{
			"ordinary":                            "visible",
			"provider.admin.osac.openshift.io/id": "provider",
		}))

		countOnly, err := publicServer.List(ctx, publicv1.FabricDomainsListRequest_builder{
			Filter: new(filter),
			Limit:  new(int32(0)),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(countOnly.GetTotal()).To(Equal(int32(2)))
		Expect(countOnly.GetItems()).To(BeEmpty())
	})

	It("rejects reserved labels and annotations on public create", func() {
		domainsDAO, err := dao.NewGenericDAO[*privatev1.FabricDomain]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		publicServer, err := NewFabricDomainsServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		for _, testCase := range []struct {
			key    string
			labels bool
		}{
			{key: "admin.osac.openshift.io/managed", labels: true},
			{key: "provider.admin.osac.openshift.io/id"},
		} {
			createID := uuid.New()
			createName := fmt.Sprintf("public-reserved-create-%s", createID[24:32])
			labels := map[string]string{"ordinary": "visible"}
			annotations := map[string]string{"ordinary": "visible"}
			if testCase.labels {
				labels[testCase.key] = "system"
			} else {
				annotations[testCase.key] = "provider"
			}

			response, err := publicServer.Create(ctx, publicv1.FabricDomainsCreateRequest_builder{
				Object: publicv1.FabricDomain_builder{
					Id: createID,
					Metadata: publicv1.Metadata_builder{
						Name:        createName,
						Labels:      labels,
						Annotations: annotations,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(response).To(BeNil())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			Expect(status.Convert(err).Message()).To(Equal(reservedMetadataError))

			created, err := domainsDAO.List().SetFilter(fmt.Sprintf("this.metadata.name == %q", createName)).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(created.GetTotal()).To(BeZero())
		}
	})

	It("rejects reserved labels and annotations on public update without writing", func() {
		domainsDAO, err := dao.NewGenericDAO[*privatev1.FabricDomain]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		publicServer, err := NewFabricDomainsServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		storedResponse, err := domainsDAO.Create().SetObject(privatev1.FabricDomain_builder{
			Metadata: privatev1.Metadata_builder{
				Name:   fmt.Sprintf("public-reserved-update-%s", uuid.New()[24:32]),
				Tenant: testTenant,
				Labels: map[string]string{
					"ordinary":                        "visible",
					"admin.osac.openshift.io/managed": "system",
				},
				Annotations: map[string]string{
					"ordinary":                            "visible",
					"provider.admin.osac.openshift.io/id": "provider",
				},
			}.Build(),
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		stored := storedResponse.GetObject()
		before := proto.Clone(stored).(*privatev1.FabricDomain)

		for _, testCase := range []struct {
			key  string
			path string
		}{
			{key: "admin.osac.openshift.io/managed", path: "metadata.labels"},
			{key: "provider.admin.osac.openshift.io/id", path: "metadata.annotations"},
		} {
			labels := map[string]string{}
			annotations := map[string]string{}
			if testCase.path == "metadata.labels" {
				labels[testCase.key] = "changed"
			} else {
				annotations[testCase.key] = "changed"
			}

			response, err := publicServer.Update(ctx, publicv1.FabricDomainsUpdateRequest_builder{
				Object: publicv1.FabricDomain_builder{
					Id: stored.GetId(),
					Metadata: publicv1.Metadata_builder{
						Labels:      labels,
						Annotations: annotations,
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{testCase.path}},
			}.Build())
			Expect(response).To(BeNil())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			Expect(status.Convert(err).Message()).To(Equal(reservedMetadataError))

			updated, err := domainsDAO.Get().SetId(stored.GetId()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(updated.GetObject(), before)).To(BeTrue())
		}
	})
})
