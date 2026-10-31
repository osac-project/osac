//
// Copyright (c) 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

package servers

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/collections"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Service account management", func() {
	var public *ServiceAccountsServer
	var private *PrivateServiceAccountsServer
	var stored *privatev1.ServiceAccount
	var tenantCtx context.Context
	BeforeEach(func() {
		tenantCtx = auth.ContextWithSubject(ctx, &auth.Subject{User: "admin", Tenants: collections.NewSet(testTenant)})
		tenancyLogic, err := auth.NewDefaultTenancyLogic().SetLogger(logger).Build()
		Expect(err).ToNot(HaveOccurred())
		public, err = NewServiceAccountsServer().SetLogger(logger).SetAttributionLogic(attribution).
			SetTenancyLogic(tenancyLogic).Build()
		Expect(err).ToNot(HaveOccurred())
		private, err = NewPrivateServiceAccountsServer().SetLogger(logger).SetAttributionLogic(attribution).
			SetTenancyLogic(tenancyLogic).Build()
		Expect(err).ToNot(HaveOccurred())
		accountDAO, err := dao.NewGenericDAO[*privatev1.ServiceAccount]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		created, err := accountDAO.Create().SetObject(privatev1.ServiceAccount_builder{
			Metadata: privatev1.Metadata_builder{Name: "robot", Tenant: testTenant, Creator: "admin",
				Finalizers:  []string{"fulfillment-controller", "other"},
				Annotations: map[string]string{"osac.openshift.io/tenant": testTenant},
				Labels:      map[string]string{"old": "value"},
			}.Build(),
		}.Build()).Do(tenantCtx)
		Expect(err).ToNot(HaveOccurred())
		stored = created.GetObject()
		stored.SetStatus(privatev1.ServiceAccountStatus_builder{
			KeycloakClientUuid: "00000000-0000-0000-0000-000000000000",
		}.Build())
		updated, err := accountDAO.Update().SetObject(stored).Do(tenantCtx)
		Expect(err).ToNot(HaveOccurred())
		stored = updated.GetObject()
	})
	It("returns public status and a tenant-filtered list", func() {
		got, err := public.Get(tenantCtx, publicv1.ServiceAccountsGetRequest_builder{Id: stored.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(got.GetObject().GetMetadata().GetLabels()).To(HaveKeyWithValue("old", "value"))
		Expect(got.GetObject().GetStatus().ProtoReflect().Descriptor().Fields().ByName("client_id")).To(BeNil())
		list, err := public.List(tenantCtx, publicv1.ServiceAccountsListRequest_builder{Limit: proto.Int32(1)}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(list.GetItems()).To(HaveLen(1))
		Expect(list.GetTotal()).To(Equal(int32(1)))
		foreign := auth.ContextWithSubject(ctx, &auth.Subject{User: "other", Tenants: collections.NewSet("other-tenant")})
		list, err = public.List(foreign, &publicv1.ServiceAccountsListRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(list.GetTotal()).To(BeZero())
		Expect(list.GetItems()).To(BeEmpty())
	})
	DescribeTable("conceals foreign IDs", func(operation string) {
		foreign := auth.ContextWithSubject(ctx, &auth.Subject{User: "other", Tenants: collections.NewSet("other-tenant")})
		var err error
		switch operation {
		case "Get":
			_, err = public.Get(foreign, publicv1.ServiceAccountsGetRequest_builder{Id: stored.GetId()}.Build())
		case "Update":
			_, err = public.Update(foreign, publicv1.ServiceAccountsUpdateRequest_builder{
				Object: publicv1.ServiceAccount_builder{Id: stored.GetId()}.Build(), UpdateMask: &fieldmaskpb.FieldMask{},
			}.Build())
		case "Delete":
			_, err = public.Delete(foreign, publicv1.ServiceAccountsDeleteRequest_builder{Id: stored.GetId()}.Build())
		}
		Expect(status.Code(err)).To(Equal(codes.NotFound))
	}, Entry("Get", "Get"), Entry("Update", "Update"), Entry("Delete", "Delete"))
	It("merges only masked fields and preserves controller-owned state", func() {
		response, err := public.Update(tenantCtx, publicv1.ServiceAccountsUpdateRequest_builder{
			Object: publicv1.ServiceAccount_builder{Id: stored.GetId(),
				Metadata: publicv1.Metadata_builder{Labels: map[string]string{"updated": "value"}}.Build(),
				Status: publicv1.ServiceAccountStatus_builder{
					State: publicv1.ServiceAccountState_SERVICE_ACCOUNT_STATE_FAILED, Message: proto.String("forged"),
				}.Build(),
			}.Build(), UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.labels"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetObject().GetMetadata().GetLabels()).To(HaveKeyWithValue("updated", "value"))
		got, err := private.Get(tenantCtx, privatev1.ServiceAccountsGetRequest_builder{Id: stored.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(proto.Equal(got.GetObject().GetStatus(), stored.GetStatus())).To(BeTrue())
		Expect(got.GetObject().GetMetadata().GetFinalizers()).To(Equal(stored.GetMetadata().GetFinalizers()))
	})
	DescribeTable("rejects server-owned update masks", func(path string) {
		_, err := public.Update(tenantCtx, publicv1.ServiceAccountsUpdateRequest_builder{
			Object:     publicv1.ServiceAccount_builder{Id: stored.GetId()}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{path}},
		}.Build())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
	}, Entry("status", "status"), Entry("client secret", "status.client_secret"),
		Entry("state", "status.state"), Entry("message", "status.message"), Entry("finalizers", "metadata.finalizers"),
		Entry("display name", "metadata.display_name"), Entry("description", "metadata.description"),
		Entry("tenant", "metadata.tenant"), Entry("project", "metadata.project"), Entry("annotations", "metadata.annotations"))
	It("allows private status updates from controllers", func() {
		response, err := private.Update(tenantCtx, privatev1.ServiceAccountsUpdateRequest_builder{
			Object: privatev1.ServiceAccount_builder{
				Id: stored.GetId(),
				Status: privatev1.ServiceAccountStatus_builder{
					State:   privatev1.ServiceAccountState_SERVICE_ACCOUNT_STATE_READY,
					Message: proto.String("provisioned"),
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state", "status.message"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetObject().GetStatus().GetState()).To(Equal(privatev1.ServiceAccountState_SERVICE_ACCOUNT_STATE_READY))
		Expect(response.GetObject().GetStatus().GetMessage()).To(Equal("provisioned"))
	})
	It("replaces mutable values with a nil mask but preserves identity and status", func() {
		response, err := public.Update(tenantCtx, publicv1.ServiceAccountsUpdateRequest_builder{
			Object: publicv1.ServiceAccount_builder{Id: stored.GetId(),
				Status: publicv1.ServiceAccountStatus_builder{State: publicv1.ServiceAccountState_SERVICE_ACCOUNT_STATE_FAILED}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		object := response.GetObject()
		Expect(object.GetMetadata().GetName()).To(Equal("robot"))
		Expect(object.GetMetadata().GetTenant()).To(Equal(testTenant))
		Expect(object.GetMetadata().GetLabels()).To(BeEmpty())
		Expect(object.GetStatus().GetState()).To(Equal(publicv1.ServiceAccountState_SERVICE_ACCOUNT_STATE_UNSPECIFIED))
	})
	It("treats an explicit empty mask as a no-op", func() {
		response, err := public.Update(tenantCtx, publicv1.ServiceAccountsUpdateRequest_builder{
			Object: publicv1.ServiceAccount_builder{Id: stored.GetId()}.Build(), UpdateMask: &fieldmaskpb.FieldMask{},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetObject().GetMetadata().GetVersion()).To(Equal(stored.GetMetadata().GetVersion()))
	})
	It("rejects a stale version before changing stored labels", func() {
		_, err := public.Update(tenantCtx, publicv1.ServiceAccountsUpdateRequest_builder{
			Object:     publicv1.ServiceAccount_builder{Id: stored.GetId(), Metadata: publicv1.Metadata_builder{Version: -1}.Build()}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.labels"}}, Lock: true,
		}.Build())
		Expect(status.Code(err)).To(Equal(codes.Aborted))
	})
	It("rejects a name change while allowing the same name", func() {
		for _, name := range []string{"robot", "changed"} {
			_, err := public.Update(tenantCtx, publicv1.ServiceAccountsUpdateRequest_builder{
				Object:     publicv1.ServiceAccount_builder{Id: stored.GetId(), Metadata: publicv1.Metadata_builder{Name: name}.Build()}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.name"}},
			}.Build())
			if name == "robot" {
				Expect(err).ToNot(HaveOccurred())
			} else {
				Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			}
		}
	})
	It("allows trusted finalizer cleanup", func() {
		_, err := private.Delete(tenantCtx, privatev1.ServiceAccountsDeleteRequest_builder{Id: stored.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		got, err := private.Get(tenantCtx, privatev1.ServiceAccountsGetRequest_builder{Id: stored.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		got.GetObject().GetMetadata().SetFinalizers(nil)
		_, err = private.Update(tenantCtx, privatev1.ServiceAccountsUpdateRequest_builder{
			Object: got.GetObject(), Lock: true, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		var live, archived int
		Expect(suiteTx.QueryRow(ctx, `select count(*) from service_accounts where id=$1`, stored.GetId()).Scan(&live)).To(Succeed())
		Expect(suiteTx.QueryRow(ctx, `select count(*) from archived_service_accounts where id=$1`, stored.GetId()).Scan(&archived)).To(Succeed())
		Expect(live).To(BeZero())
		Expect(archived).To(Equal(1))
	})

	It("marks deletion without synchronous Keycloak calls and retains cleanup finalizers", func() {
		_, err := public.Delete(tenantCtx, publicv1.ServiceAccountsDeleteRequest_builder{Id: stored.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		response, err := private.Get(tenantCtx, privatev1.ServiceAccountsGetRequest_builder{Id: stored.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetObject().GetMetadata().GetDeletionTimestamp()).ToNot(BeNil())
		Expect(response.GetObject().GetMetadata().GetFinalizers()).To(ConsistOf("fulfillment-controller", "other"))
	})
})
