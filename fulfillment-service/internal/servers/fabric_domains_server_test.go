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

	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Fabric domains public filter visibility", func() {
	It("filters whole metadata maps using the public view, including counts", func() {
		domainsDAO, err := dao.NewGenericDAO[*privatev1.FabricDomain]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		for _, object := range []struct {
			name        string
			labels      map[string]string
			annotations map[string]string
		}{
			{
				name: fmt.Sprintf("public-map-filter-%s", uuid.New()[24:32]),
				labels: map[string]string{
					"ordinary":                        "visible",
					"admin.osac.openshift.io/managed": "system",
				},
				annotations: map[string]string{"ordinary": "visible"},
			},
			{
				name:        fmt.Sprintf("public-map-filter-%s", uuid.New()[24:32]),
				labels:      map[string]string{"ordinary": "visible"},
				annotations: map[string]string{"ordinary": "visible"},
			},
		} {
			_, err = domainsDAO.Create().SetObject(privatev1.FabricDomain_builder{
				Metadata: privatev1.Metadata_builder{
					Name:        object.name,
					Tenant:      testTenant,
					Labels:      object.labels,
					Annotations: object.annotations,
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
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

		countOnly, err := publicServer.List(ctx, publicv1.FabricDomainsListRequest_builder{
			Filter: new(filter),
			Limit:  new(int32(0)),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(countOnly.GetTotal()).To(Equal(int32(2)))
		Expect(countOnly.GetItems()).To(BeEmpty())
	})
})
