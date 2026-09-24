/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package sim

import (
	"context"
	"fmt"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var runID = strconv.FormatInt(time.Now().UnixMilli(), 36)

var _ = Describe("BareMetalInstance via real fulfillment-service", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("creates a BMI and reads it back (smoke test)", func() {
		name := "sim-smoke-" + runID
		bmi := createBMI(ctx, name)

		got, err := fulfillmentClient.GetBareMetalInstance(ctx, bmi.GetId())
		Expect(err).NotTo(HaveOccurred())
		Expect(got.GetMetadata().GetName()).To(Equal(name))
		Expect(got.GetMetadata().GetTenant()).To(Equal("system"))
	})

	It("enforces AlreadyExists on duplicate BMI create (OSAC-3266 idempotency)", func() {
		name := "sim-dup-" + runID
		createBMI(ctx, name)

		_, err := fulfillmentClient.CreateBareMetalInstance(ctx, privatev1.BareMetalInstance_builder{
			Metadata: privatev1.Metadata_builder{
				Name:   name,
				Tenant: "system",
			}.Build(),
			Spec: privatev1.BareMetalInstanceSpec_builder{
				CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{
					Name: "sim-test-catalog-item",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.AlreadyExists),
			fmt.Sprintf("expected AlreadyExists, got %v: %v", status.Code(err), err))
	})
})

func createBMI(ctx context.Context, name string) *privatev1.BareMetalInstance {
	bmi, err := fulfillmentClient.CreateBareMetalInstance(ctx, privatev1.BareMetalInstance_builder{
		Metadata: privatev1.Metadata_builder{
			Name:   name,
			Tenant: "system",
		}.Build(),
		Spec: privatev1.BareMetalInstanceSpec_builder{
			CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{
				Name: "sim-test-catalog-item",
			}.Build(),
		}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred(), "failed to create BMI %q", name)
	Expect(bmi.GetId()).NotTo(BeEmpty())
	return bmi
}
