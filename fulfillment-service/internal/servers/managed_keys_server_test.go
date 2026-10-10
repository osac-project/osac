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
	"context"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Managed keys server", func() {
	var publicServer *ManagedKeysServer

	BeforeEach(func() {
		var err error
		publicServer, err = NewManagedKeysServer().
			SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
	})

	It("Lists an empty collection", func() {
		publicResponse, err := publicServer.List(ctx, &publicv1.ManagedKeysListRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(publicResponse.GetItems()).To(BeEmpty())
		Expect(publicResponse.GetTotal()).To(BeZero())
	})

	It("Omits private backend fields from public responses", func() {
		stored := seedManagedKey(ctx, "key-a")
		publicResponse, err := publicServer.Get(ctx, publicv1.ManagedKeysGetRequest_builder{Id: stored.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		object := publicResponse.GetObject()
		Expect(object.GetId()).To(Equal(stored.GetId()))
		Expect(object.GetMetadata().GetTenant()).To(Equal(testTenant))
		Expect(object.GetUsage()).To(Equal(publicv1.ManagedKeyUsage_MANAGED_KEY_USAGE_ENCRYPT_DECRYPT))
		Expect(object.GetState()).To(Equal(publicv1.ManagedKeyState_MANAGED_KEY_STATE_ACTIVE))
		Expect(object.GetVersions()).To(HaveLen(1))
		Expect(object.GetVersions()[0].GetGeneration()).To(Equal(uint64(1)))
		Expect(proto.Equal(object.GetVersions()[0].GetCreationTimestamp(), stored.GetVersions()[0].GetCreationTimestamp())).To(BeTrue())
		data, err := protojson.Marshal(object)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(data)).ToNot(ContainSubstring("backend"))
		Expect(string(data)).ToNot(ContainSubstring("private-object"))
		Expect(string(data)).ToNot(ContainSubstring("private-version"))

		list, err := publicServer.List(ctx, publicv1.ManagedKeysListRequest_builder{Filter: proto.String(`this.id == "key-a"`)}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(list.GetSize()).To(Equal(int32(1)))
		Expect(list.GetTotal()).To(Equal(int32(1)))
		Expect(list.GetItems()).To(HaveLen(1))
		Expect(proto.Equal(list.GetItems()[0], object)).To(BeTrue())
	})

	It("Preserves pagination including an explicitly zero limit", func() {
		seedManagedKey(ctx, "key-a")
		seedManagedKey(ctx, "key-b")
		publicResponse, err := publicServer.List(ctx, publicv1.ManagedKeysListRequest_builder{
			Offset: proto.Int32(1), Limit: proto.Int32(1),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(publicResponse.GetSize()).To(Equal(int32(1)))
		Expect(publicResponse.GetTotal()).To(Equal(int32(2)))
		publicResponse, err = publicServer.List(ctx, publicv1.ManagedKeysListRequest_builder{Limit: proto.Int32(0)}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(publicResponse.GetItems()).To(BeEmpty())
		Expect(publicResponse.GetTotal()).To(Equal(int32(2)))
	})

	It("Rejects private backend filters", func() {
		seedManagedKey(ctx, "key-a")
		filter := `this.backend_name == "test-backend"`
		_, err := publicServer.List(ctx, publicv1.ManagedKeysListRequest_builder{Filter: &filter}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
	})

	It("Returns NotFound for an unknown key", func() {
		_, err := publicServer.Get(ctx, publicv1.ManagedKeysGetRequest_builder{Id: "missing"}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound))
	})

	It("Applies tenant and project visibility to List and Get", func() {
		seedManagedKey(ctx, "visible-key")
		scopedTenancy := seedManagedKeyVisibilityFixtures(ctx)
		var err error
		publicServer, err = NewManagedKeysServer().
			SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(scopedTenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		response, err := publicServer.List(ctx, &publicv1.ManagedKeysListRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetItems()).To(HaveLen(1))
		Expect(response.GetItems()[0].GetId()).To(Equal("visible-key"))
		for _, id := range []string{"hidden-project-key", "hidden-tenant-key"} {
			_, err = publicServer.Get(ctx, publicv1.ManagedKeysGetRequest_builder{Id: id}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound))
		}
	})

	It("Leaves stored keys and change events untouched for all unimplemented operations", func() {
		stored := seedManagedKey(ctx, "key-a")
		original, err := publicServer.Get(ctx, publicv1.ManagedKeysGetRequest_builder{Id: stored.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		before := countManagedKeyChanges(ctx)
		// A context without a transaction proves the stubs never enter a DAO or generic mutation path.
		stubCtx := context.Background()
		_, err = publicServer.Create(stubCtx, &publicv1.ManagedKeysCreateRequest{})
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.Unimplemented))
		_, err = publicServer.Update(stubCtx, &publicv1.ManagedKeysUpdateRequest{})
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.Unimplemented))
		_, err = publicServer.Delete(stubCtx, publicv1.ManagedKeysDeleteRequest_builder{Id: stored.GetId()}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.Unimplemented))
		response, err := publicServer.Get(ctx, publicv1.ManagedKeysGetRequest_builder{Id: stored.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(proto.Equal(response.GetObject(), original.GetObject())).To(BeTrue())
		Expect(countManagedKeyChanges(ctx)).To(Equal(before))
	})
})
