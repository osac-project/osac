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
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func seedManagedKey(ctx context.Context, id string) *privatev1.ManagedKey {
	keysDAO, err := dao.NewGenericDAO[*privatev1.ManagedKey]().
		SetLogger(logger).SetTenancyLogic(tenancy).Build()
	Expect(err).ToNot(HaveOccurred())
	response, err := keysDAO.Create().SetObject(privatev1.ManagedKey_builder{
		Id:          id,
		Metadata:    privatev1.Metadata_builder{Name: id, Tenant: testTenant}.Build(),
		Usage:       privatev1.ManagedKeyUsage_MANAGED_KEY_USAGE_ENCRYPT_DECRYPT,
		State:       privatev1.ManagedKeyState_MANAGED_KEY_STATE_ACTIVE,
		Backend:     privatev1.KeyBackend_KEY_BACKEND_VAULT_TRANSIT,
		BackendName: "test-backend",
		Versions: []*privatev1.ManagedKeyVersion{
			privatev1.ManagedKeyVersion_builder{
				Generation: 1, CreationTimestamp: timestamppb.New(time.Unix(100, 0)),
				BackendObjectId: "private-object", BackendVersionId: proto.String("private-version"),
			}.Build(),
		},
	}.Build()).Do(ctx)
	Expect(err).ToNot(HaveOccurred())
	return response.GetObject()
}

func seedManagedKeyVisibilityFixtures(ctx context.Context) *auth.MockTenancyLogic {
	_, err := suiteTx.Exec(ctx, `
            insert into projects (id, name, tenant, project, creator, data)
            values ('private-project', 'private', $1, 'private', 'system', '{}');
        `, testTenant)
	Expect(err).ToNot(HaveOccurred())
	_, err = suiteTx.Exec(ctx, `
            insert into managed_keys (id, name, tenant, project, data)
            values ('hidden-project-key', 'hidden-project-key', $1, 'private', '{}'),
                   ('hidden-tenant-key', 'hidden-tenant-key', 'system', '', '{}')
        `, testTenant)
	Expect(err).ToNot(HaveOccurred())

	scopedTenancy := auth.NewMockTenancyLogic(ctrl)
	visibility, err := auth.NewVisibility().AddVisibleTenant(testTenant).Build()
	Expect(err).ToNot(HaveOccurred())
	scopedTenancy.EXPECT().DetermineVisibility(gomock.Any()).Return(visibility, nil).AnyTimes()
	return scopedTenancy
}

func countManagedKeyChanges(ctx context.Context) int {
	var count int
	err := suiteTx.QueryRow(ctx, `select count(*) from changes where "table" = 'managed_keys'`).Scan(&count)
	Expect(err).ToNot(HaveOccurred())
	return count
}

var _ = Describe("Private managed keys server", func() {
	var privateServer *PrivateManagedKeysServer

	BeforeEach(func() {
		var err error
		privateServer, err = NewPrivateManagedKeysServer().
			SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
	})

	It("Lists an empty collection", func() {
		privateResponse, err := privateServer.List(ctx, &privatev1.ManagedKeysListRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(privateResponse.GetItems()).To(BeEmpty())
		Expect(privateResponse.GetTotal()).To(BeZero())
	})

	It("Reads stored metadata", func() {
		stored := seedManagedKey(ctx, "key-a")
		privateResponse, err := privateServer.Get(ctx, privatev1.ManagedKeysGetRequest_builder{Id: stored.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(proto.Equal(privateResponse.GetObject(), stored)).To(BeTrue())
	})

	It("Preserves pagination including an explicitly zero limit", func() {
		seedManagedKey(ctx, "key-a")
		seedManagedKey(ctx, "key-b")
		privateResponse, err := privateServer.List(ctx, privatev1.ManagedKeysListRequest_builder{
			Offset: proto.Int32(1), Limit: proto.Int32(1),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(privateResponse.GetSize()).To(Equal(int32(1)))
		Expect(privateResponse.GetTotal()).To(Equal(int32(2)))
		privateResponse, err = privateServer.List(ctx, privatev1.ManagedKeysListRequest_builder{Limit: proto.Int32(0)}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(privateResponse.GetItems()).To(BeEmpty())
		Expect(privateResponse.GetTotal()).To(Equal(int32(2)))
	})

	It("Accepts private backend filters", func() {
		seedManagedKey(ctx, "key-a")
		filter := `this.backend_name == "test-backend"`
		privateResponse, err := privateServer.List(ctx, privatev1.ManagedKeysListRequest_builder{Filter: &filter}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(privateResponse.GetItems()).To(HaveLen(1))
	})

	It("Returns NotFound for an unknown key", func() {
		_, err := privateServer.Get(ctx, privatev1.ManagedKeysGetRequest_builder{Id: "missing"}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound))
	})

	It("Applies tenant and project visibility to List and Get", func() {
		seedManagedKey(ctx, "visible-key")
		scopedTenancy := seedManagedKeyVisibilityFixtures(ctx)
		var err error
		privateServer, err = NewPrivateManagedKeysServer().
			SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(scopedTenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		response, err := privateServer.List(ctx, &privatev1.ManagedKeysListRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetItems()).To(HaveLen(1))
		Expect(response.GetItems()[0].GetId()).To(Equal("visible-key"))
		for _, id := range []string{"hidden-project-key", "hidden-tenant-key"} {
			_, err = privateServer.Get(ctx, privatev1.ManagedKeysGetRequest_builder{Id: id}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound))
		}
	})

	It("Leaves stored keys and change events untouched for all unimplemented operations", func() {
		stored := seedManagedKey(ctx, "key-a")
		before := countManagedKeyChanges(ctx)
		// A context without a transaction also proves the stubs never enter a DAO or generic mutation path.
		stubCtx := context.Background()
		_, err := privateServer.Create(stubCtx, privatev1.ManagedKeysCreateRequest_builder{Object: stored}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.Unimplemented))
		_, err = privateServer.Update(stubCtx, privatev1.ManagedKeysUpdateRequest_builder{Object: stored}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.Unimplemented))
		_, err = privateServer.Delete(stubCtx, privatev1.ManagedKeysDeleteRequest_builder{Id: stored.GetId()}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.Unimplemented))
		_, err = privateServer.Signal(stubCtx, privatev1.ManagedKeysSignalRequest_builder{Id: stored.GetId()}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.Unimplemented))
		response, err := privateServer.Get(ctx, privatev1.ManagedKeysGetRequest_builder{Id: stored.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(proto.Equal(response.GetObject(), stored)).To(BeTrue())
		Expect(countManagedKeyChanges(ctx)).To(Equal(before))
	})
})
