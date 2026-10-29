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
	"sync"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/collections"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Service account creation", func() {
	var public *ServiceAccountsServer
	var request *publicv1.ServiceAccountsCreateRequest
	var create func(context.Context, *publicv1.ServiceAccountsCreateRequest) (*publicv1.ServiceAccountsCreateResponse, error)
	var caller context.Context
	BeforeEach(func() {
		Expect(suiteTx.End(ctx)).To(Succeed())
		suiteTx = nil
		caller = auth.ContextWithSubject(context.Background(), &auth.Subject{User: "admin", Tenants: collections.NewSet(testTenant)})
		tenancyLogic, err := auth.NewDefaultTenancyLogic().SetLogger(logger).Build()
		Expect(err).ToNot(HaveOccurred())
		public, err = NewServiceAccountsServer().SetLogger(logger).SetAttributionLogic(attribution).
			SetTenancyLogic(tenancyLogic).Build()
		Expect(err).ToNot(HaveOccurred())
		txInterceptor, err := database.NewTxInterceptor().SetLogger(logger).SetManager(tm).Build()
		Expect(err).ToNot(HaveOccurred())
		create = func(ctx context.Context, req *publicv1.ServiceAccountsCreateRequest) (*publicv1.ServiceAccountsCreateResponse, error) {
			response, err := txInterceptor.UnaryServer(ctx, req, &grpc.UnaryServerInfo{FullMethod: publicv1.ServiceAccounts_Create_FullMethodName},
				func(ctx context.Context, req any) (any, error) {
					return public.Create(ctx, req.(*publicv1.ServiceAccountsCreateRequest))
				})
			if response == nil {
				return nil, err
			}
			return response.(*publicv1.ServiceAccountsCreateResponse), err
		}
		request = publicv1.ServiceAccountsCreateRequest_builder{Object: publicv1.ServiceAccount_builder{
			Metadata: publicv1.Metadata_builder{Name: "robot"}.Build(),
		}.Build()}.Build()
	})
	It("persists a pending account and returns before Keycloak provisioning", func() {
		request.GetObject().SetStatus(publicv1.ServiceAccountStatus_builder{
			State:   publicv1.ServiceAccountState_SERVICE_ACCOUNT_STATE_READY,
			Message: proto.String("forged"),
		}.Build())
		request.GetObject().GetMetadata().SetAnnotations(map[string]string{"osac.openshift.io/tenant": "forged"})
		response, err := create(caller, request)
		Expect(err).ToNot(HaveOccurred())
		_, err = uuid.Parse(response.GetObject().GetId())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetObject().GetStatus().GetState()).To(Equal(publicv1.ServiceAccountState_SERVICE_ACCOUNT_STATE_PENDING))
		Expect(response.GetObject().GetStatus().GetClientSecret()).To(BeNil())
		Expect(response.GetObject().GetStatus().GetMessage()).To(BeEmpty())
		Expect(response.GetObject().GetMetadata().GetDisplayName()).To(BeEmpty())
		Expect(response.GetObject().GetMetadata().GetTenant()).To(Equal(testTenant))
		Expect(response.GetObject().GetMetadata().GetAnnotations()).To(HaveKeyWithValue("osac.openshift.io/tenant", testTenant))
		Expect(request.GetObject().GetStatus().GetState()).To(Equal(publicv1.ServiceAccountState_SERVICE_ACCOUNT_STATE_READY), "input must remain unmodified")
		Expect(request.GetObject().GetStatus().GetMessage()).To(Equal("forged"), "input must remain unmodified")
		var count int
		Expect(testPool.QueryRow(ctx, `select count(*) from service_accounts where id=$1 and tenant=$2`, response.GetObject().GetId(), testTenant).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(1))
		Expect(testPool.QueryRow(ctx, `select count(*) from changes where "table"='service_accounts' and data->>'id'=$1`, response.GetObject().GetId()).Scan(&count)).To(Succeed())
		Expect(count).To(BeNumerically(">", 0))
	})
	It("validates a dry run without creating a row", func() {
		dry := metadata.NewIncomingContext(caller, metadata.Pairs(DryRunMetadataKey, "true"))
		response, err := create(dry, request)
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetObject().GetId()).To(BeEmpty())
		Expect(response.GetObject().GetStatus().GetState()).To(Equal(publicv1.ServiceAccountState_SERVICE_ACCOUNT_STATE_PENDING))
		Expect(response.GetObject().GetStatus().GetClientSecret()).To(BeNil())
		var count int
		Expect(testPool.QueryRow(ctx, `select count(*) from service_accounts`).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
	})
	DescribeTable("rejects invalid input before persistence", func(change func(*publicv1.ServiceAccount)) {
		change(request.GetObject())
		_, err := create(caller, request)
		Expect(status.Code(err)).To(Or(Equal(codes.InvalidArgument), Equal(codes.PermissionDenied)))
		var count int
		Expect(testPool.QueryRow(ctx, `select count(*) from service_accounts`).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
	}, Entry("caller ID", func(o *publicv1.ServiceAccount) { o.SetId("chosen") }),
		Entry("tenant mismatch", func(o *publicv1.ServiceAccount) { o.GetMetadata().SetTenant("foreign") }),
		Entry("project", func(o *publicv1.ServiceAccount) { o.GetMetadata().SetProject("team") }),
		Entry("display name", func(o *publicv1.ServiceAccount) { o.GetMetadata().SetDisplayName("Build robot") }),
		Entry("description", func(o *publicv1.ServiceAccount) { o.GetMetadata().SetDescription("Automation identity") }),
		Entry("bad name", func(o *publicv1.ServiceAccount) { o.GetMetadata().SetName("Invalid_name") }))
	It("rejects a duplicate name", func() {
		_, err := create(caller, request)
		Expect(err).ToNot(HaveOccurred())
		response, err := create(caller, request)
		Expect(status.Code(err)).To(Equal(codes.AlreadyExists))
		Expect(response).To(BeNil())
		var count int
		Expect(testPool.QueryRow(ctx, `select count(*) from service_accounts`).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(1))
	})
	It("allows only one concurrent create for a tenant and name", func() {
		type result struct {
			response *publicv1.ServiceAccountsCreateResponse
			code     codes.Code
		}
		start := make(chan struct{})
		results := make(chan result, 2)
		var workers sync.WaitGroup
		for range 2 {
			workers.Go(func() {
				<-start
				response, err := create(caller, proto.Clone(request).(*publicv1.ServiceAccountsCreateRequest))
				results <- result{response: response, code: status.Code(err)}
			})
		}
		close(start)
		workers.Wait()
		first, second := <-results, <-results
		Expect([]codes.Code{first.code, second.code}).To(ConsistOf(codes.OK, codes.AlreadyExists))
	})
	It("maps persistence failures without creating an external client", func() {
		_, err := testPool.Exec(ctx, `create function reject_service_account_insert() returns trigger language plpgsql as $$
		begin raise exception 'test insert failure'; end $$;
		create trigger reject_service_account_insert before insert on service_accounts
		for each row execute function reject_service_account_insert()`)
		Expect(err).ToNot(HaveOccurred())
		response, err := create(caller, request)
		Expect(status.Code(err)).To(Equal(codes.Internal))
		Expect(response).To(BeNil())
		var count int
		Expect(testPool.QueryRow(ctx, `select count(*) from service_accounts`).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
	})
	It("does not persist credentials or caller-supplied status", func() {
		request.GetObject().SetStatus(publicv1.ServiceAccountStatus_builder{
			State:   publicv1.ServiceAccountState_SERVICE_ACCOUNT_STATE_FAILED,
			Message: proto.String("forged"),
		}.Build())
		response, err := create(caller, request)
		Expect(err).ToNot(HaveOccurred())
		data := ""
		Expect(testPool.QueryRow(ctx, `select data::text from service_accounts where id=$1`, response.GetObject().GetId()).Scan(&data)).To(Succeed())
		Expect(data).NotTo(ContainSubstring("forged"))
		Expect(data).NotTo(ContainSubstring("client_secret"))
	})
})
