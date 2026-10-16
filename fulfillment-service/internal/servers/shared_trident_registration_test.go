/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package servers

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Shared Trident registration", func() {
	var backends *PrivateStorageBackendsServer
	var tiers *PrivateStorageTiersServer
	var backend *privatev1.StorageBackend
	BeforeEach(func() {
		var err error
		backends, err = NewPrivateStorageBackendsServer().SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
		Expect(err).NotTo(HaveOccurred())
		backend = &privatev1.StorageBackend{}
		Expect(protojson.Unmarshal([]byte(`{
   "metadata":{"name":"shared-ontap"},
   "spec":{"provider":"ontap","endpoint":"https://array.example.com",
    "credentials":{"username":"admin","password":"fixture-password"},
    "ontap":{"profiles":[{"name":"shared-fc","shared":{"tbc_namespace":"trident","tbc_name":"customer-fc"}}]}}
  }`), backend)).To(Succeed())
		backendDAO, err := dao.NewGenericDAO[*privatev1.StorageBackend]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).NotTo(HaveOccurred())
		tiers, err = NewPrivateStorageTiersServer().SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).SetStorageBackendsDAO(backendDAO).Build()
		Expect(err).NotTo(HaveOccurred())
	})
	create := func() (*privatev1.StorageBackend, error) {
		response, err := backends.Create(ctx, privatev1.StorageBackendsCreateRequest_builder{Object: backend}.Build())
		return response.GetObject(), err
	}
	tier := func(id string) *privatev1.StorageTier {
		obj := &privatev1.StorageTier{}
		Expect(protojson.Unmarshal([]byte(`{"metadata":{"name":"shared-tier"},"spec":{"protocol":"STORAGE_PROTOCOL_BLOCK","backends":[{"ontap":{"profile":"shared-fc","pool_selector":{"performance":"extreme"}}}]}}`), obj)).To(Succeed())
		obj.GetSpec().GetBackends()[0].SetBackendId(id)
		return obj
	}
	It("persists the shared TBC and per-tier virtual pool selection", func() {
		saved, err := create()
		Expect(err).NotTo(HaveOccurred())
		got, err := backends.Get(ctx, privatev1.StorageBackendsGetRequest_builder{Id: saved.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(proto.Equal(saved, got.GetObject())).To(BeTrue())
		obj := tier(saved.GetId())
		obj.GetSpec().GetBackends()[0].SetEncryptionEnabled(true)
		response, err := tiers.Create(ctx, privatev1.StorageTiersCreateRequest_builder{Object: obj}.Build())
		Expect(err).NotTo(HaveOccurred())
		gotTier, err := tiers.Get(ctx, privatev1.StorageTiersGetRequest_builder{Id: response.GetObject().GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(proto.Equal(response.GetObject(), gotTier.GetObject())).To(BeTrue())
	})
	It("preserves mandatory connection fields for shared registration", func() {
		backend.GetSpec().SetEndpoint("")
		_, err := create()
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		backend.GetSpec().SetEndpoint("https://array.example.com")
		backend.GetSpec().ClearCredentials()
		_, err = create()
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
	})
	It("rejects an ONTAP configuration on another provider", func() {
		backend.GetSpec().SetProvider("vast")
		_, err := create()
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
	})
	DescribeTable("rejects invalid shared backend definitions", func(config string) {
		Expect(protojson.Unmarshal([]byte(config), backend.GetSpec().GetOntap())).To(Succeed())
		_, err := create()
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
	},
		Entry("no profiles", `{}`),
		Entry("duplicate names", `{"profiles":[{"name":"shared-fc","shared":{"tbc_namespace":"trident","tbc_name":"one"}},{"name":"shared-fc","shared":{"tbc_namespace":"trident","tbc_name":"two"}}]}`),
		Entry("missing management mode", `{"profiles":[{"name":"shared-fc"}]}`),
		Entry("invalid TBC DNS subdomain", `{"profiles":[{"name":"shared-fc","shared":{"tbc_namespace":"trident","tbc_name":"a..b"}}]}`),
	)
	It("does not allow replacing a registered shared backend profile", func() {
		saved, err := create()
		Expect(err).NotTo(HaveOccurred())
		candidate := proto.Clone(saved).(*privatev1.StorageBackend)
		Expect(protojson.Unmarshal([]byte(`{"profiles":[{"name":"shared-fc","shared":{"tbc_namespace":"trident","tbc_name":"another-backend"}}]}`), candidate.GetSpec().GetOntap())).To(Succeed())
		_, err = backends.Update(ctx, privatev1.StorageBackendsUpdateRequest_builder{Object: candidate, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.ontap"}}}.Build())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
	})
	DescribeTable("rejects incomplete or incompatible shared tier configuration", func(config string) {
		saved, err := create()
		Expect(err).NotTo(HaveOccurred())
		obj := tier(saved.GetId())
		Expect(protojson.Unmarshal([]byte(config), obj.GetSpec().GetBackends()[0].GetOntap())).To(Succeed())
		_, err = tiers.Create(ctx, privatev1.StorageTiersCreateRequest_builder{Object: obj}.Build())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
	},
		Entry("unknown profile", `{"profile":"missing","pool_selector":{"performance":"extreme"}}`),
		Entry("missing profile", `{"pool_selector":{"performance":"extreme"}}`),
		Entry("empty selector", `{"profile":"shared-fc"}`),
		Entry("selector expression injection", `{"profile":"shared-fc","pool_selector":{"performance":"extreme;other=value"}}`),
		Entry("unmanaged IOPS limit", `{"profile":"shared-fc","max_iops":5000,"pool_selector":{"performance":"extreme"}}`),
	)
})
