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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("ONTAP tier registration", func() {
	var tierServer *PrivateStorageTiersServer
	var ontapID, otherID string
	BeforeEach(func() {
		backends, err := dao.NewGenericDAO[*privatev1.StorageBackend]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).NotTo(HaveOccurred())
		for _, provider := range []string{"ontap", "pure"} {
			response, err := backends.Create().SetObject(privatev1.StorageBackend_builder{
				Metadata: privatev1.Metadata_builder{Name: provider, Tenant: auth.SharedTenant}.Build(),
				Spec: privatev1.StorageBackendSpec_builder{Provider: provider, Endpoint: "https://array.example.com",
					Credentials: privatev1.StorageBackendCredentials_builder{Username: "discovery", Password: testPassword}.Build(),
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			if provider == "ontap" {
				ontapID = response.GetObject().GetId()
			} else {
				otherID = response.GetObject().GetId()
			}
		}
		tierServer, err = NewPrivateStorageTiersServer().SetLogger(logger).SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).SetStorageBackendsDAO(backends).Build()
		Expect(err).NotTo(HaveOccurred())
	})
	object := func() *privatev1.StorageTier {
		return privatev1.StorageTier_builder{Metadata: privatev1.Metadata_builder{Name: "netapp-tier"}.Build(),
			Spec: privatev1.StorageTierSpec_builder{Protocol: privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
				Backends: []*privatev1.BackendAssociation{privatev1.BackendAssociation_builder{BackendId: ontapID,
					Ontap: privatev1.OntapAssociationConfig_builder{MaxIops: 5000}.Build(),
				}.Build()},
			}.Build(),
		}.Build()
	}
	create := func(obj *privatev1.StorageTier) (*privatev1.StorageTiersCreateResponse, error) {
		return tierServer.Create(ctx, privatev1.StorageTiersCreateRequest_builder{Object: obj}.Build())
	}
	It("retains the private IOPS/encryption association through persistence", func() {
		obj := object()
		obj.GetSpec().GetBackends()[0].SetEncryptionEnabled(true)
		response, err := create(obj)
		Expect(err).NotTo(HaveOccurred())
		current, err := tierServer.Get(ctx, privatev1.StorageTiersGetRequest_builder{Id: response.GetObject().GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(proto.Equal(response.GetObject(), current.GetObject())).To(BeTrue())
		Expect(current.GetObject().GetSpec().GetBackends()[0].GetOntap().GetMaxIops()).To(Equal(int64(5000)))
		Expect(current.GetObject().GetSpec().GetBackends()[0].GetEncryptionEnabled()).To(BeTrue())
	})
	DescribeTable("accepts optional and bounded IOPS", func(config *privatev1.OntapAssociationConfig) {
		obj := object()
		obj.GetSpec().GetBackends()[0].SetOntap(config)
		response, err := create(obj)
		Expect(err).NotTo(HaveOccurred())
		Expect(response.GetObject().GetSpec().GetBackends()[0].GetOntap().GetMaxIops()).To(Equal(config.GetMaxIops()))
	},
		Entry("absent", (*privatev1.OntapAssociationConfig)(nil)),
		Entry("zero", privatev1.OntapAssociationConfig_builder{}.Build()),
		Entry("maximum", privatev1.OntapAssociationConfig_builder{MaxIops: 2147483647}.Build()),
	)
	It("keeps other providers' protocol and bandwidth behavior", func() {
		obj := object()
		obj.GetSpec().SetProtocol(privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS)
		association := obj.GetSpec().GetBackends()[0]
		association.SetBackendId(otherID)
		association.ClearOntap()
		association.SetMaxReadBandwidthMbs(100)
		_, err := create(obj)
		Expect(err).NotTo(HaveOccurred())
	})
	DescribeTable("rejects incompatible associations", func(change func(*privatev1.StorageTier), code codes.Code) {
		obj := object()
		change(obj)
		_, err := create(obj)
		Expect(status.Code(err)).To(Equal(code))
		list, err := tierServer.List(ctx, &privatev1.StorageTiersListRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(list.GetItems()).To(BeEmpty())
	},
		Entry("NFS", func(obj *privatev1.StorageTier) {
			obj.GetSpec().SetProtocol(privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS)
		}, codes.InvalidArgument),
		Entry("read bandwidth", func(obj *privatev1.StorageTier) { obj.GetSpec().GetBackends()[0].SetMaxReadBandwidthMbs(1) }, codes.InvalidArgument),
		Entry("write bandwidth", func(obj *privatev1.StorageTier) { obj.GetSpec().GetBackends()[0].SetMaxWriteBandwidthMbs(1) }, codes.InvalidArgument),
		Entry("other provider with ONTAP QoS", func(obj *privatev1.StorageTier) { obj.GetSpec().GetBackends()[0].SetBackendId(otherID) }, codes.InvalidArgument),
		Entry("missing backend", func(obj *privatev1.StorageTier) { obj.GetSpec().GetBackends()[0].SetBackendId("missing") }, codes.NotFound),
		Entry("multiple backends", func(obj *privatev1.StorageTier) {
			obj.GetSpec().SetBackends(append(obj.GetSpec().GetBackends(), privatev1.BackendAssociation_builder{BackendId: otherID}.Build()))
		}, codes.InvalidArgument),
		Entry("negative IOPS", func(obj *privatev1.StorageTier) { obj.GetSpec().GetBackends()[0].GetOntap().SetMaxIops(-1) }, codes.InvalidArgument),
		Entry("excessive IOPS", func(obj *privatev1.StorageTier) { obj.GetSpec().GetBackends()[0].GetOntap().SetMaxIops(2147483648) }, codes.InvalidArgument),
	)
	DescribeTable("requires replacement for effective binding changes", func(change func(*privatev1.StorageTier), path string) {
		response, err := create(object())
		Expect(err).NotTo(HaveOccurred())
		saved := response.GetObject()
		obj := proto.Clone(saved).(*privatev1.StorageTier)
		change(obj)
		_, err = tierServer.Update(ctx, privatev1.StorageTiersUpdateRequest_builder{Object: obj,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{path}},
		}.Build())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		current, err := tierServer.Get(ctx, privatev1.StorageTiersGetRequest_builder{Id: saved.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(proto.Equal(saved, current.GetObject())).To(BeTrue())
	},
		Entry("IOPS", func(obj *privatev1.StorageTier) { obj.GetSpec().GetBackends()[0].GetOntap().SetMaxIops(6000) }, "spec.backends"),
		Entry("encryption", func(obj *privatev1.StorageTier) { obj.GetSpec().GetBackends()[0].SetEncryptionEnabled(true) }, "spec"),
		Entry("protocol", func(obj *privatev1.StorageTier) {
			obj.GetSpec().SetProtocol(privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS)
		}, "spec.protocol"),
		Entry("other provider", func(obj *privatev1.StorageTier) {
			obj.GetSpec().GetBackends()[0].SetBackendId(otherID)
			obj.GetSpec().GetBackends()[0].ClearOntap()
		}, "spec.backends"),
	)
	It("accepts masked description and no-op updates", func() {
		response, err := create(object())
		Expect(err).NotTo(HaveOccurred())
		partial := privatev1.StorageTier_builder{Id: response.GetObject().GetId(),
			Spec: privatev1.StorageTierSpec_builder{Description: "Updated"}.Build(),
		}.Build()
		updated, err := tierServer.Update(ctx, privatev1.StorageTiersUpdateRequest_builder{Object: partial,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.description"}},
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(updated.GetObject().GetSpec().GetBackends()[0].GetOntap().GetMaxIops()).To(Equal(int64(5000)))
		noop, err := tierServer.Update(ctx, privatev1.StorageTiersUpdateRequest_builder{Object: updated.GetObject()}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(noop.GetObject().GetMetadata().GetVersion()).To(Equal(updated.GetObject().GetMetadata().GetVersion()))
	})
})
