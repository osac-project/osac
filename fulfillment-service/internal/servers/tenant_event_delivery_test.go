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
	"errors"

	"github.com/IBM/sarama"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/collections"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type tenantAuthorizationStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *tenantAuthorizationStream) Context() context.Context { return s.ctx }

type tenantEventStream struct {
	grpc.ServerStream
	events []*publicv1.Event
}

func (s *tenantEventStream) Send(response *publicv1.EventsWatchResponse) error {
	s.events = append(s.events, response.GetEvent())
	return nil
}

var _ = Describe("Tenant event delivery authorization", func() {
	var subscription *eventsServerSubscription
	var stream *tenantEventStream
	var message *sarama.ConsumerMessage
	BeforeEach(func() {
		builder := NewEventsServer().SetLogger(logger)
		oneof, err := builder.findPayloadOneof()
		Expect(err).ToNot(HaveOccurred())
		mapper, err := NewGenericMapper[*privatev1.Event, *publicv1.Event]().SetLogger(logger).Build()
		Expect(err).ToNot(HaveOccurred())
		stream = &tenantEventStream{}
		subscription = &eventsServerSubscription{
			ctx: ctx, logger: logger, stream: stream,
			allowedTopics: map[string]struct{}{DefaultEventTopicPrefix + testTenant: {}},
			server:        &EventsServer{logger: logger, payloadOneof: oneof, mapper: mapper},
		}
		data, err := proto.Marshal(privatev1.Event_builder{
			Id: "queued-event", Type: privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			Cluster: privatev1.Cluster_builder{Id: "cluster", Metadata: privatev1.Metadata_builder{Tenant: testTenant}.Build()}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		message = &sarama.ConsumerMessage{Topic: DefaultEventTopicPrefix + testTenant, Value: data}
	})

	It("checks queued event delivery against committed deletion instead of the old Watch subject", func() {
		logic, err := auth.NewDefaultTenancyLogic().SetLogger(logger).
			SetTenantResolver(database.ResolveLiveTenantNames).Build()
		Expect(err).ToNot(HaveOccurred())
		subscription.server.tenancyLogic = logic
		Expect(suiteTx.End(ctx)).To(Succeed())
		suiteTx = nil
		managerCtx := database.TxManagerIntoContext(context.Background(), tm)
		subscription.ctx = auth.ContextWithSubject(managerCtx, &auth.Subject{
			User: "alice", Tenants: collections.NewSet(testTenant),
		})
		Expect(subscription.processMessage(message)).To(Succeed())
		Expect(stream.events).To(HaveLen(1))
		_, err = database.WithNewTx(managerCtx, func(c context.Context) (struct{}, error) {
			tx, err := database.TxFromContext(c)
			if err == nil {
				_, err = tx.Exec(c, `update tenants set deletion_timestamp=now() where name=$1`, testTenant)
			}
			return struct{}{}, err
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(subscription.processMessage(message)).To(Succeed())
		Expect(stream.events).To(HaveLen(1))
	})

	It("terminates delivery without sending when authoritative visibility fails", func() {
		logic := auth.NewMockTenancyLogic(ctrl)
		logic.EXPECT().DetermineVisibility(gomock.Any()).Return(nil, errors.New("database unavailable"))
		subscription.server.tenancyLogic = logic
		Expect(status.Code(subscription.processMessage(message))).To(Equal(codes.Internal))
		Expect(stream.events).To(BeEmpty())
	})
})
