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
	"fmt"
	"strings"
	"time"

	"github.com/IBM/sarama"
	"github.com/gobuffalo/flect"
	"github.com/jackc/pgx/v5/pgxpool"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/decorators"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/kafka"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Event publisher", Ordered, func() {
	type consumedEvent struct {
		topic   string
		key     string
		headers []*sarama.RecordHeader
		event   *privatev1.Event
	}

	newTestEventPublisher := func(pool *pgxpool.Pool, client sarama.Client, registry *prometheus.Registry) (*EventPublisher, error) {
		return NewEventPublisher().
			SetLogger(logger).
			SetDatabasePool(pool).
			SetKafkaClient(client).
			SetMetricsRegisterer(registry).
			Build()
	}

	metricValue := func(registry *prometheus.Registry, name string) float64 {
		GinkgoHelper()
		families, err := registry.Gather()
		Expect(err).ToNot(HaveOccurred())
		for _, family := range families {
			if family.GetName() != name {
				continue
			}
			Expect(family.GetMetric()).To(HaveLen(1))
			metric := family.GetMetric()[0]
			if metric.GetCounter() != nil {
				return metric.GetCounter().GetValue()
			}
			Expect(metric.GetGauge()).ToNot(BeNil())
			return metric.GetGauge().GetValue()
		}
		// Counter vectors are absent from the registry until their first use.
		return 0
	}

	kafkaHasTopic := func(client sarama.Client, topic string) bool {
		ExpectWithOffset(1, client.RefreshMetadata()).To(Succeed())
		topics, err := client.Topics()
		ExpectWithOffset(1, err).ToNot(HaveOccurred())
		for _, name := range topics {
			if name == topic {
				return true
			}
		}
		return false
	}

	collectKafkaEvents := func(client sarama.Client, topic string, n int) []consumedEvent {
		consumer, err := sarama.NewConsumerFromClient(client)
		ExpectWithOffset(1, err).ToNot(HaveOccurred())
		defer func() {
			_ = consumer.Close()
		}()

		var partitionConsumer sarama.PartitionConsumer
		EventuallyWithOffset(1, func() error {
			if partitionConsumer != nil {
				_ = partitionConsumer.Close()
				partitionConsumer = nil
			}
			_ = client.RefreshMetadata(topic)
			var consumeErr error
			partitionConsumer, consumeErr = consumer.ConsumePartition(topic, 0, sarama.OffsetOldest)
			return consumeErr
		}).WithTimeout(15 * time.Second).WithPolling(100 * time.Millisecond).Should(Succeed())
		defer func() {
			if partitionConsumer != nil {
				_ = partitionConsumer.Close()
			}
		}()

		events := make([]consumedEvent, 0, n)
		EventuallyWithOffset(1, func() int {
			for {
				select {
				case message := <-partitionConsumer.Messages():
					event := &privatev1.Event{}
					ExpectWithOffset(2, proto.Unmarshal(message.Value, event)).To(Succeed())
					events = append(events, consumedEvent{
						topic:   message.Topic,
						key:     string(message.Key),
						headers: message.Headers,
						event:   event,
					})
				default:
					return len(events)
				}
			}
		}).WithTimeout(30 * time.Second).WithPolling(50 * time.Millisecond).Should(BeNumerically(">=", n))
		return events[:n]
	}

	startPublisher := func(pub *EventPublisher) (cancel context.CancelFunc, done <-chan error) {
		runCtx, cancel := context.WithCancel(context.Background())
		ch := make(chan error, 1)
		go func() {
			ch <- pub.Run(runCtx)
		}()
		return cancel, ch
	}

	stopPublisher := func(cancel context.CancelFunc, done <-chan error) {
		cancel()
		Eventually(done).WithTimeout(5 * time.Second).Should(Receive(MatchError(context.Canceled)))
	}

	notifyChanges := func(ctx context.Context, pool *pgxpool.Pool) {
		_, err := pool.Exec(ctx, "select pg_notify($1, 'test')", "changes")
		ExpectWithOffset(1, err).ToNot(HaveOccurred())
	}

	var kafkaBroker *kafka.Container

	BeforeAll(func() {
		var err error
		startCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		kafkaBroker, err = kafka.NewContainer().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())
		err = kafkaBroker.Start(startCtx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Minute)
			defer stopCancel()
			err := kafkaBroker.Stop(stopCtx)
			Expect(err).ToNot(HaveOccurred())
		})
	})

	BeforeEach(func() {
		// EventPublisher specs use dedicated database instances. End the suite transaction so it
		// cannot pin pg_snapshot_xmin and hide committed change rows from the drain query.
		if suiteTx != nil {
			err := suiteTx.End(ctx)
			Expect(err).ToNot(HaveOccurred())
			suiteTx = nil
		}
	})

	Describe("Creation", func() {
		It("Can be created when all the required parameters are set", func() {
			client, err := kafkaBroker.Client()
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(client.Close)

			publisher, err := newTestEventPublisher(&pgxpool.Pool{}, client, prometheus.NewRegistry())
			Expect(err).ToNot(HaveOccurred())
			Expect(publisher).ToNot(BeNil())
			Expect(publisher.Close()).To(Succeed())
			Expect(publisher.Close()).To(Succeed())
		})

		It("Accepts a publish callback", func() {
			client, err := kafkaBroker.Client()
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(client.Close)

			publisher, err := NewEventPublisher().
				SetLogger(logger).
				SetDatabasePool(&pgxpool.Pool{}).
				SetKafkaClient(client).
				SetMetricsRegisterer(prometheus.NewRegistry()).
				SetPublishCallback(func(context.Context, *privatev1.Event) error {
					return nil
				}).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(publisher).ToNot(BeNil())
			Expect(publisher.Close()).To(Succeed())
		})

		It("Can't be created without a logger", func() {
			publisher, err := NewEventPublisher().
				SetDatabasePool(&pgxpool.Pool{}).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(publisher).To(BeNil())
		})

		It("Can't be created without a pool", func() {
			publisher, err := NewEventPublisher().
				SetLogger(logger).
				Build()
			Expect(err).To(MatchError("database pool is mandatory"))
			Expect(publisher).To(BeNil())
		})

		It("Can't be created without a Kafka client", func() {
			publisher, err := NewEventPublisher().
				SetLogger(logger).
				SetDatabasePool(&pgxpool.Pool{}).
				Build()
			Expect(err).To(MatchError("kafka client is mandatory"))
			Expect(publisher).To(BeNil())
		})

		It("Can't be created with non-positive timeouts or batch size", func() {
			publisher, err := NewEventPublisher().
				SetLogger(logger).
				SetDatabasePool(&pgxpool.Pool{}).
				SetListenWaitTimeout(0).
				Build()
			Expect(err).To(MatchError("listen wait timeout should be positive, but it is 0s"))
			Expect(publisher).To(BeNil())

			publisher, err = NewEventPublisher().
				SetLogger(logger).
				SetDatabasePool(&pgxpool.Pool{}).
				SetListenRetryInterval(0).
				Build()
			Expect(err).To(MatchError("listen retry interval should be positive, but it is 0s"))
			Expect(publisher).To(BeNil())

			publisher, err = NewEventPublisher().
				SetLogger(logger).
				SetDatabasePool(&pgxpool.Pool{}).
				SetBatchSize(0).
				Build()
			Expect(err).To(MatchError("batch size should be positive, but it is 0"))
			Expect(publisher).To(BeNil())

			publisher, err = NewEventPublisher().
				SetLogger(logger).
				SetDatabasePool(&pgxpool.Pool{}).
				SetMetricsInterval(0).
				Build()
			Expect(err).To(MatchError("metrics interval should be positive, but it is 0s"))
			Expect(publisher).To(BeNil())
		})

		It("Can't be created with an empty topic prefix", func() {
			publisher, err := NewEventPublisher().
				SetLogger(logger).
				SetDatabasePool(&pgxpool.Pool{}).
				SetKafkaTopicPrefix("").
				Build()
			Expect(err).To(MatchError("topic prefix is mandatory"))
			Expect(publisher).To(BeNil())
		})

		It("Can't be created with an empty table", func() {
			publisher, err := NewEventPublisher().
				SetLogger(logger).
				SetDatabasePool(&pgxpool.Pool{}).
				SetTable("").
				Build()
			Expect(err).To(MatchError("table is mandatory"))
			Expect(publisher).To(BeNil())
		})

		It("Can't be created with an empty channel", func() {
			publisher, err := NewEventPublisher().
				SetLogger(logger).
				SetDatabasePool(&pgxpool.Pool{}).
				SetChannel("").
				Build()
			Expect(err).To(MatchError("channel is mandatory"))
			Expect(publisher).To(BeNil())
		})

		It("Can't be created with an invalid table", func() {
			publisher, err := NewEventPublisher().
				SetLogger(logger).
				SetDatabasePool(&pgxpool.Pool{}).
				SetTable("foo-bar").
				Build()
			Expect(err).To(MatchError("table 'foo-bar' should contain only letters, digits and underscores"))
			Expect(publisher).To(BeNil())
		})

		It("Can't be created with an invalid channel", func() {
			publisher, err := NewEventPublisher().
				SetLogger(logger).
				SetDatabasePool(&pgxpool.Pool{}).
				SetChannel("foo;bar").
				Build()
			Expect(err).To(MatchError("channel 'foo;bar' should contain only letters, digits and underscores"))
			Expect(publisher).To(BeNil())
		})

		It("Can't be created with a table that starts with a digit", func() {
			publisher, err := NewEventPublisher().
				SetLogger(logger).
				SetDatabasePool(&pgxpool.Pool{}).
				SetTable("1changes").
				Build()
			Expect(err).To(MatchError("table '1changes' should not start with a digit"))
			Expect(publisher).To(BeNil())
		})

		It("Can't be created with a channel that starts with a digit", func() {
			publisher, err := NewEventPublisher().
				SetLogger(logger).
				SetDatabasePool(&pgxpool.Pool{}).
				SetChannel("1changes").
				Build()
			Expect(err).To(MatchError("channel '1changes' should not start with a digit"))
			Expect(publisher).To(BeNil())
		})
	})

	Describe("Encode change event", func() {
		var (
			encodeCtx context.Context
			pool      *pgxpool.Pool
			client    sarama.Client
			registry  *prometheus.Registry
			pub       *EventPublisher
		)

		BeforeEach(func() {
			var err error
			encodeCtx = context.Background()

			db, err := server.NewInstance().Build()
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(db.Close)

			pool, err = db.Pool(encodeCtx)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(pool.Close)

			client, err = kafkaBroker.Client()
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(client.Close)

			registry = prometheus.NewRegistry()
			pub, err = newTestEventPublisher(pool, client, registry)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(pub.Close)
		})

		insertChange := func(table, tenant, objectID, data string) string {
			if data == "" {
				data = fmt.Sprintf(
					`{"id": %q, "tenant": %q}`,
					objectID,
					tenant,
				)
			}
			var changeID string
			query := `
				insert into changes ("table", op, data)
				values ($1, 'INSERT', $2::jsonb)
				returning id
			`
			err := pool.QueryRow(encodeCtx, query, table, data).Scan(&changeID)
			Expect(err).ToNot(HaveOccurred())
			return changeID
		}

		It("Serializes a private Event from the change row", func() {
			tenant := "acme-" + uuid.New()
			topic := DefaultEventTopicPrefix + tenant
			changeID := insertChange("projects", tenant, "p-1", fmt.Sprintf(`{
				"id": "p-1",
				"name": "p-1",
				"tenant": %q,
				"data": {"spec":{"title":"Acme"}}
			}`, tenant))

			cancel, done := startPublisher(pub)
			defer stopPublisher(cancel, done)

			events := collectKafkaEvents(client, topic, 1)
			Expect(events[0].key).To(Equal("p-1"))
			Expect(events[0].event.GetId()).To(Equal(changeID))
			Expect(events[0].event.GetType()).To(Equal(privatev1.EventType_EVENT_TYPE_OBJECT_CREATED))
			Expect(events[0].event.GetProject()).ToNot(BeNil())
			Expect(events[0].event.GetProject().GetId()).To(Equal("p-1"))
			Expect(events[0].event.GetProject().GetMetadata().GetTenant()).To(Equal(tenant))
			Expect(events[0].event.GetProject().GetSpec().GetTitle()).To(Equal("Acme"))
		})

		It("Publishes a signal change from an object row", func() {
			tenant := "signal-" + uuid.New()
			topic := DefaultEventTopicPrefix + tenant
			var changeID string
			err := pool.QueryRow(encodeCtx, `
				insert into changes ("table", op, data)
				values ('projects', $1, $2::jsonb)
				returning id
			`, "SIGNAL", fmt.Sprintf(`{
				"id": "p-signal",
				"name": "p-signal",
				"tenant": %q,
				"version": 1,
				"data": {"spec":{"title":"Signaled"}}
			}`, tenant)).Scan(&changeID)
			Expect(err).ToNot(HaveOccurred())

			cancel, done := startPublisher(pub)
			defer stopPublisher(cancel, done)

			events := collectKafkaEvents(client, topic, 1)
			Expect(events[0].key).To(Equal("p-signal"))
			Expect(events[0].event.GetId()).To(Equal(changeID))
			Expect(events[0].event.GetType()).To(Equal(privatev1.EventType_EVENT_TYPE_OBJECT_SIGNALED))
			Expect(events[0].event.GetProject().GetId()).To(Equal("p-signal"))
			Expect(events[0].event.GetProject().GetMetadata().GetVersion()).To(Equal(int32(1)))
			Expect(events[0].event.GetProject().GetSpec().GetTitle()).To(Equal("Signaled"))
		})

		It("Redacts secret data before publishing", func() {
			tenant := "secret-" + uuid.New()
			topic := DefaultEventTopicPrefix + tenant
			payload, err := protojson.Marshal(privatev1.Secret_builder{
				Data: map[string][]byte{
					"password": []byte("super-secret"),
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			insertChange("secrets", tenant, "s-1", fmt.Sprintf(`{
				"id": "s-1",
				"name": "s-1",
				"tenant": %q,
				"data": %s
			}`, tenant, payload))

			cancel, done := startPublisher(pub)
			defer stopPublisher(cancel, done)

			events := collectKafkaEvents(client, topic, 1)
			Expect(events[0].event.GetSecret()).ToNot(BeNil())
			Expect(events[0].event.GetSecret().GetId()).To(Equal("s-1"))
			Expect(events[0].event.GetSecret().GetData()).To(BeEmpty())
		})

		It("Redacts hub kubeconfig before publishing", func() {
			tenant := "hub-" + uuid.New()
			topic := DefaultEventTopicPrefix + tenant
			payload, err := protojson.Marshal(privatev1.Hub_builder{
				Spec: privatev1.HubSpec_builder{
					Kubeconfig: []byte("my-kubeconfig"),
					Namespace:  "clusters",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			insertChange("hubs", tenant, "h-1", fmt.Sprintf(`{
				"id": "h-1",
				"name": "h-1",
				"tenant": %q,
				"data": %s
			}`, tenant, payload))

			cancel, done := startPublisher(pub)
			defer stopPublisher(cancel, done)

			events := collectKafkaEvents(client, topic, 1)
			Expect(events[0].event.GetHub()).ToNot(BeNil())
			Expect(events[0].event.GetHub().GetId()).To(Equal("h-1"))
			Expect(events[0].event.GetHub().GetSpec().GetKubeconfig()).To(BeEmpty())
			Expect(events[0].event.GetHub().GetSpec().GetNamespace()).To(Equal("clusters"))
		})

		It("Redacts storage backend password before publishing", func() {
			tenant := "storage-" + uuid.New()
			topic := DefaultEventTopicPrefix + tenant
			payload, err := protojson.Marshal(privatev1.StorageBackend_builder{
				Spec: privatev1.StorageBackendSpec_builder{
					Provider: "vast",
					Endpoint: "https://storage.example",
					Credentials: privatev1.StorageBackendCredentials_builder{
						Username: "admin",
						Password: "s3cret",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			insertChange("storage_backends", tenant, "sb-1", fmt.Sprintf(`{
				"id": "sb-1",
				"name": "sb-1",
				"tenant": %q,
				"data": %s
			}`, tenant, payload))

			cancel, done := startPublisher(pub)
			defer stopPublisher(cancel, done)

			events := collectKafkaEvents(client, topic, 1)
			Expect(events[0].event.GetStorageBackend()).ToNot(BeNil())
			Expect(events[0].event.GetStorageBackend().GetId()).To(Equal("sb-1"))
			Expect(events[0].event.GetStorageBackend().GetSpec().GetCredentials().GetUsername()).To(Equal("admin"))
			Expect(events[0].event.GetStorageBackend().GetSpec().GetCredentials().GetPassword()).To(BeEmpty())
		})

		It("Redacts user credentials before publishing", func() {
			tenant := "user-" + uuid.New()
			topic := DefaultEventTopicPrefix + tenant
			payload, err := protojson.Marshal(privatev1.User_builder{
				Spec: privatev1.UserSpec_builder{
					Credentials: privatev1.UserCredentials_builder{
						Password:          proto.String("super-secret"),
						TemporaryPassword: true,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			insertChange("users", tenant, "u-1", fmt.Sprintf(`{
				"id": "u-1",
				"name": "u-1",
				"tenant": %q,
				"data": %s
			}`, tenant, payload))

			cancel, done := startPublisher(pub)
			defer stopPublisher(cancel, done)

			events := collectKafkaEvents(client, topic, 1)
			Expect(events[0].event.GetUser()).ToNot(BeNil())
			Expect(events[0].event.GetUser().GetId()).To(Equal("u-1"))
			Expect(events[0].event.GetUser().GetSpec().GetCredentials()).To(BeNil())
		})

		It("Redacts tenant break-glass credentials before publishing", func() {
			tenant := "break-glass-" + uuid.New()
			topic := DefaultEventTopicPrefix + tenant
			payload, err := protojson.Marshal(privatev1.Tenant_builder{
				Status: privatev1.TenantStatus_builder{
					BreakGlassCredentials: privatev1.BreakGlassCredentials_builder{
						Username: "break-glass-user",
						Password: "super-secret",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			insertChange("tenants", tenant, tenant, fmt.Sprintf(`{
				"id": %q,
				"name": %q,
				"tenant": %q,
				"data": %s
			}`, tenant, tenant, tenant, payload))

			cancel, done := startPublisher(pub)
			defer stopPublisher(cancel, done)

			events := collectKafkaEvents(client, topic, 1)
			Expect(events[0].event.GetTenant()).ToNot(BeNil())
			Expect(events[0].event.GetTenant().GetId()).To(Equal(tenant))
			Expect(events[0].event.GetTenant().GetStatus().GetBreakGlassCredentials()).To(BeNil())
		})

		It("Maps each Event payload type to a unique table name", func() {
			tenant := "map-" + uuid.New()
			topic := DefaultEventTopicPrefix + tenant
			var event *privatev1.Event
			oneof := event.ProtoReflect().Descriptor().Oneofs().ByName("payload")
			Expect(oneof).ToNot(BeNil())

			tables := map[string]struct{}{}
			for i := range oneof.Fields().Len() {
				field := oneof.Fields().Get(i)
				Expect(field.Message()).ToNot(BeNil())
				table := flect.Pluralize(string(field.Name()))
				Expect(tables).ToNot(HaveKey(table))
				tables[table] = struct{}{}
				insertChange(table, tenant, string(field.Name()), "")
			}

			cancel, done := startPublisher(pub)
			defer stopPublisher(cancel, done)

			events := collectKafkaEvents(client, topic, oneof.Fields().Len())
			published := map[string]*privatev1.Event{}
			for _, item := range events {
				published[item.key] = item.event
			}
			for i := range oneof.Fields().Len() {
				field := oneof.Fields().Get(i)
				encoded := published[string(field.Name())]
				Expect(encoded).ToNot(BeNil())
				Expect(encoded.ProtoReflect().WhichOneof(oneof)).To(Equal(field))
			}
		})

		It("Maps table names using the DAO pluralization convention", func() {
			tenant := "dao-" + uuid.New()
			topic := DefaultEventTopicPrefix + tenant
			insertChange("network_classes", tenant, "nc-1", "")
			insertChange("external_ips", tenant, "ip-1", "")

			cancel, done := startPublisher(pub)
			defer stopPublisher(cancel, done)

			events := collectKafkaEvents(client, topic, 2)
			byID := map[string]*privatev1.Event{}
			for _, item := range events {
				byID[item.key] = item.event
			}
			Expect(byID["nc-1"].GetNetworkClass().GetId()).To(Equal("nc-1"))
			Expect(byID["ip-1"].GetExternalIp().GetId()).To(Equal("ip-1"))
		})

		It("Does not publish a row with invalid object data", func() {
			tenant := "baddata-" + uuid.New()
			topic := DefaultEventTopicPrefix + tenant
			insertChange("projects", tenant, "p-1", fmt.Sprintf(
				`{"id":"p-1","tenant":%q,"data":[]}`,
				tenant,
			))

			cancel, done := startPublisher(pub)
			defer stopPublisher(cancel, done)

			Consistently(func() bool {
				return kafkaHasTopic(client, topic)
			}).WithTimeout(300 * time.Millisecond).WithPolling(50 * time.Millisecond).Should(BeFalse())
			Eventually(func() int {
				var count int
				err := pool.QueryRow(encodeCtx, "select count(*) from changes").Scan(&count)
				Expect(err).ToNot(HaveOccurred())
				return count
			}).WithTimeout(5 * time.Second).Should(Equal(0))
			Expect(metricValue(registry, "event_publish_errors_total")).To(Equal(float64(1)))
		})

		It("Does not publish a row that has an empty tenant", func() {
			topic := DefaultEventTopicPrefix
			_, err := pool.Exec(encodeCtx, `
				insert into changes ("table", op, data)
				values ('projects', 'INSERT', '{"id": "p-1"}'::jsonb)
			`)
			Expect(err).ToNot(HaveOccurred())

			cancel, done := startPublisher(pub)
			defer stopPublisher(cancel, done)

			Consistently(func() bool {
				return kafkaHasTopic(client, topic)
			}).WithTimeout(300 * time.Millisecond).WithPolling(50 * time.Millisecond).Should(BeFalse())
			Eventually(func() int {
				var count int
				err := pool.QueryRow(encodeCtx, "select count(*) from changes").Scan(&count)
				Expect(err).ToNot(HaveOccurred())
				return count
			}).WithTimeout(5 * time.Second).Should(Equal(0))
			Expect(metricValue(registry, "event_publish_errors_total")).To(Equal(float64(1)))
		})
	})

	Describe("Wait for changes table", func() {
		var (
			waitCtx context.Context
			pool    *pgxpool.Pool
			client  sarama.Client
			pub     *EventPublisher
		)

		BeforeEach(func() {
			var err error
			waitCtx = context.Background()

			db, err := server.NewInstance().Build()
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(db.Close)

			pool, err = db.Pool(waitCtx)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(pool.Close)

			client, err = kafkaBroker.Client()
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(client.Close)

			pub, err = newTestEventPublisher(pool, client, prometheus.NewRegistry())
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(pub.Close)
		})

		insertProjectChange := func(tenant, objectID string) {
			_, err := pool.Exec(waitCtx, `
				insert into changes ("table", op, data)
				values (
					'projects',
					'INSERT',
					jsonb_build_object(
						'id', $2::text,
						'tenant', $1::text
					)
				)
			`, tenant, objectID)
			Expect(err).ToNot(HaveOccurred())
		}

		createChangesTable := func() {
			_, err := pool.Exec(waitCtx, `
				create table changes (
					id uuid primary key default uuidv7(),
					"table" text not null,
					op text not null,
					data jsonb not null,
					"timestamp" timestamptz not null default now()
				)
			`)
			Expect(err).ToNot(HaveOccurred())
		}

		It("Starts publishing immediately when the table exists", func() {
			tenant := "wait-" + uuid.New()
			topic := DefaultEventTopicPrefix + tenant
			insertProjectChange(tenant, "p-1")

			cancel, done := startPublisher(pub)
			defer stopPublisher(cancel, done)

			events := collectKafkaEvents(client, topic, 1)
			Expect(events[0].key).To(Equal("p-1"))
		})

		It("Waits until the table is created", func() {
			tenant := "waitcreate-" + uuid.New()
			topic := DefaultEventTopicPrefix + tenant
			_, err := pool.Exec(waitCtx, "drop table changes")
			Expect(err).ToNot(HaveOccurred())

			cancel, done := startPublisher(pub)
			defer stopPublisher(cancel, done)

			Consistently(done, "50ms").ShouldNot(Receive())

			createChangesTable()
			insertProjectChange(tenant, "p-1")
			notifyChanges(waitCtx, pool)

			events := collectKafkaEvents(client, topic, 1)
			Expect(events[0].key).To(Equal("p-1"))
		})

		It("Returns when the context is canceled", func() {
			_, err := pool.Exec(waitCtx, "drop table changes")
			Expect(err).ToNot(HaveOccurred())

			canceled, cancel := context.WithCancel(waitCtx)
			cancel()

			err = pub.Run(canceled)
			Expect(err).To(MatchError(context.Canceled))
		})
	})

	Describe("Drain", func() {
		var (
			drainCtx context.Context
			pool     *pgxpool.Pool
			client   sarama.Client
			registry *prometheus.Registry
			pub      *EventPublisher
		)

		BeforeEach(func() {
			var err error
			drainCtx = context.Background()

			db, err := server.NewInstance().Build()
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(db.Close)

			pool, err = db.Pool(drainCtx)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(pool.Close)

			client, err = kafkaBroker.Client()
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(client.Close)

			registry = prometheus.NewRegistry()
			pub, err = newTestEventPublisher(pool, client, registry)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(pub.Close)
		})

		insertChange := func(tenant, objectID, op string) string {
			var id string
			err := pool.QueryRow(drainCtx, `
				insert into changes ("table", op, data)
				values (
					'projects',
					$3::text,
					jsonb_build_object(
						'id', $2::text,
						'tenant', $1::text
					)
				)
				returning id
			`, tenant, objectID, op).Scan(&id)
			Expect(err).ToNot(HaveOccurred())
			return id
		}

		countChanges := func() int {
			var count int
			err := pool.QueryRow(drainCtx, "select count(*) from changes").Scan(&count)
			Expect(err).ToNot(HaveOccurred())
			return count
		}

		Describe("Topic creation", func() {
			It("Publishes to new topics and reuses them for later events", func() {
				tenant := "topics-" + uuid.New()
				otherTenant := "topics-" + uuid.New()
				topic := DefaultEventTopicPrefix + tenant
				otherTopic := DefaultEventTopicPrefix + otherTenant
				Expect(kafkaHasTopic(client, topic)).To(BeFalse())
				Expect(kafkaHasTopic(client, otherTopic)).To(BeFalse())
				insertChange(tenant, "obj-1", "INSERT")
				insertChange(otherTenant, "obj-2", "INSERT")
				cancel, done := startPublisher(pub)
				defer stopPublisher(cancel, done)
				Eventually(countChanges).WithTimeout(5 * time.Second).Should(BeZero())

				insertChange(tenant, "obj-3", "UPDATE")
				insertChange(otherTenant, "obj-4", "UPDATE")
				notifyChanges(drainCtx, pool)
				Eventually(countChanges).WithTimeout(10 * time.Second).Should(BeZero())
				events := collectKafkaEvents(client, topic, 2)
				Expect(events[0].key).To(Equal("obj-1"))
				Expect(events[1].key).To(Equal("obj-3"))
				otherEvents := collectKafkaEvents(client, otherTopic, 2)
				Expect(otherEvents[0].key).To(Equal("obj-2"))
				Expect(otherEvents[1].key).To(Equal("obj-4"))
			})

			It("Publishes to an existing topic after restart", func() {
				tenant := "existing-" + uuid.New()
				topic := DefaultEventTopicPrefix + tenant
				insertChange(tenant, "obj-1", "INSERT")
				func() {
					cancel, done := startPublisher(pub)
					defer stopPublisher(cancel, done)
					Eventually(countChanges).WithTimeout(5 * time.Second).Should(BeZero())
				}()
				Expect(pub.Close()).To(Succeed())

				var err error
				pub, err = newTestEventPublisher(pool, client, prometheus.NewRegistry())
				Expect(err).ToNot(HaveOccurred())
				DeferCleanup(pub.Close)
				insertChange(tenant, "obj-2", "INSERT")
				cancel, done := startPublisher(pub)
				defer stopPublisher(cancel, done)
				Eventually(countChanges).WithTimeout(5 * time.Second).Should(BeZero())
				events := collectKafkaEvents(client, topic, 2)
				Expect(events[0].key).To(Equal("obj-1"))
				Expect(events[1].key).To(Equal("obj-2"))
			})

			Describe("Kafka protocol responses", func() {
				var (
					broker  *sarama.MockBroker
					tenant  string
					topic   string
					ready   *sarama.MockMetadataResponse
					missing *sarama.MockMetadataResponse
				)

				// Configure failures at the Kafka boundary, using the same public client
				// injection as production. No publisher fields are replaced.
				configureBroker := func(metadata sarama.MockResponse, createErr, produceErr sarama.KError) {
					broker.SetHandlerByMap(map[string]sarama.MockResponse{
						"MetadataRequest": metadata,
						"CreateTopicsRequest": sarama.NewMockWrapper(&sarama.CreateTopicsResponse{
							Version:     3,
							TopicErrors: map[string]*sarama.TopicError{topic: {Err: createErr}},
						}),
						"ProduceRequest": sarama.NewMockProduceResponse(GinkgoT()).SetError(topic, 0, produceErr),
					})
				}

				topicChecks := func() int {
					count := 0
					for _, exchange := range broker.History() {
						request, ok := exchange.Request.(*sarama.MetadataRequest)
						if ok && !request.AllowAutoTopicCreation && len(request.Topics) == 1 && request.Topics[0] == topic {
							count++
						}
					}
					return count
				}

				createdTopics := func() []string {
					var topics []string
					for _, exchange := range broker.History() {
						if request, ok := exchange.Request.(*sarama.CreateTopicsRequest); ok {
							Expect(request.ValidateOnly).To(BeFalse())
							for name, detail := range request.TopicDetails {
								Expect(detail.NumPartitions).To(Equal(int32(1)))
								Expect(detail.ReplicationFactor).To(Equal(int16(1)))
								topics = append(topics, name)
							}
						}
					}
					return topics
				}

				BeforeEach(func() {
					Expect(pub.Close()).To(Succeed())
					broker = sarama.NewMockBroker(GinkgoT(), 1)
					DeferCleanup(broker.Close)
					tenant = "protocol-" + uuid.New()
					topic = DefaultEventTopicPrefix + tenant
					ready = sarama.NewMockMetadataResponse(GinkgoT()).
						SetBroker(broker.Addr(), broker.BrokerID()).
						SetController(broker.BrokerID()).
						SetLeader(topic, 0, broker.BrokerID())
					missing = sarama.NewMockMetadataResponse(GinkgoT()).
						SetBroker(broker.Addr(), broker.BrokerID()).
						SetController(broker.BrokerID())
					configureBroker(ready, sarama.ErrNoError, sarama.ErrNoError)
					config := sarama.NewConfig()
					config.Version = sarama.V2_1_0_0
					config.ApiVersionsRequest = false
					config.Producer.Return.Successes = true
					config.Producer.Retry.Max = 0
					config.Metadata.RefreshFrequency = 0
					config.Metadata.Retry.Max = 0
					config.Admin.Retry.Max = 0
					config.Net.ReadTimeout = time.Second
					mockClient, err := sarama.NewClient([]string{broker.Addr()}, config)
					Expect(err).ToNot(HaveOccurred())
					DeferCleanup(mockClient.Close)
					registry = prometheus.NewRegistry()
					pub, err = NewEventPublisher().
						SetLogger(logger).
						SetDatabasePool(pool).
						SetKafkaClient(mockClient).
						SetMetricsRegisterer(registry).
						SetListenWaitTimeout(50 * time.Millisecond).
						Build()
					Expect(err).ToNot(HaveOccurred())
					DeferCleanup(pub.Close)
				})

				It("Creates a missing topic once and skips checks after successful publication", func() {
					configureBroker(sarama.NewMockSequence(missing, ready), sarama.ErrNoError, sarama.ErrNoError)
					insertChange(tenant, "obj-1", "INSERT")
					cancel, done := startPublisher(pub)
					defer stopPublisher(cancel, done)
					Eventually(countChanges).WithTimeout(5 * time.Second).Should(BeZero())
					insertChange(tenant, "obj-2", "UPDATE")
					Eventually(countChanges).WithTimeout(5 * time.Second).Should(BeZero())
					Expect(createdTopics()).To(Equal([]string{topic}))
					Expect(topicChecks()).To(Equal(1))
					Expect(metricValue(registry, "event_publish_total")).To(Equal(float64(2)))
				})

				It("Does not try to create an existing topic", func() {
					insertChange(tenant, "obj-1", "INSERT")
					cancel, done := startPublisher(pub)
					defer stopPublisher(cancel, done)
					Eventually(countChanges).WithTimeout(5 * time.Second).Should(BeZero())
					Expect(createdTopics()).To(BeEmpty())
					Expect(topicChecks()).To(Equal(1))
				})

				It("Tolerates another publisher creating the topic after the check", func() {
					configureBroker(sarama.NewMockSequence(missing, ready), sarama.ErrTopicAlreadyExists, sarama.ErrNoError)
					insertChange(tenant, "obj-1", "INSERT")
					cancel, done := startPublisher(pub)
					defer stopPublisher(cancel, done)
					Eventually(countChanges).WithTimeout(5 * time.Second).Should(BeZero())
					Expect(createdTopics()).To(Equal([]string{topic}))
					Expect(metricValue(registry, "event_publish_total")).To(Equal(float64(1)))
				})

				It("Retains the event when the broker does not answer a metadata request", func() {
					configureBroker(nil, sarama.ErrNoError, sarama.ErrNoError)
					insertChange(tenant, "obj-1", "INSERT")
					cancel, done := startPublisher(pub)
					defer stopPublisher(cancel, done)
					Eventually(func() float64 {
						return metricValue(registry, "event_publish_errors_total")
					}).WithTimeout(5 * time.Second).Should(BeNumerically(">", 0))
					Expect(countChanges()).To(Equal(1))
					Expect(metricValue(registry, "event_publish_total")).To(BeZero())
				})

				DescribeTable("Retains the event and retries after a Kafka failure",
					func(phase string) {
						switch phase {
						case "metadata timeout":
							configureBroker(missing.SetError(topic, sarama.ErrRequestTimedOut), sarama.ErrNoError, sarama.ErrNoError)
						case "metadata authorization":
							configureBroker(missing.SetError(topic, sarama.ErrTopicAuthorizationFailed), sarama.ErrNoError, sarama.ErrNoError)
						case "topic creation":
							configureBroker(missing, sarama.ErrTopicAuthorizationFailed, sarama.ErrNoError)
						case "publication":
							configureBroker(sarama.NewMockSequence(missing, ready), sarama.ErrNoError, sarama.ErrTopicAuthorizationFailed)
						}
						insertChange(tenant, "obj-1", "INSERT")
						cancel, done := startPublisher(pub)
						defer stopPublisher(cancel, done)
						Eventually(func() float64 {
							return metricValue(registry, "event_publish_errors_total")
						}).WithTimeout(5 * time.Second).Should(BeNumerically(">", 0))
						Expect(countChanges()).To(Equal(1))
						Expect(metricValue(registry, "event_publish_total")).To(BeZero())
						if phase == "topic creation" {
							configureBroker(sarama.NewMockSequence(missing, ready), sarama.ErrNoError, sarama.ErrNoError)
						} else {
							configureBroker(ready, sarama.ErrNoError, sarama.ErrNoError)
						}
						Eventually(countChanges).WithTimeout(5 * time.Second).Should(BeZero())
						Expect(topicChecks()).To(BeNumerically(">=", 2))
						Expect(metricValue(registry, "event_publish_total")).To(Equal(float64(1)))
						switch phase {
						case "topic creation":
							Expect(createdTopics()).To(ContainElements(topic, topic))
						case "publication":
							Expect(createdTopics()).To(Equal([]string{topic}))
						}
					},
					Entry("broker-reported metadata timeout", "metadata timeout"),
					Entry("metadata authorization error", "metadata authorization"),
					Entry("topic creation error", "topic creation"),
					Entry("publish error", "publication"),
				)
			})
		})

		insertTenant := func(id string) {
			_, err := pool.Exec(drainCtx, `
				insert into tenants (id, name, tenant, creator, data)
				values ($1, $1, $1, 'system', '{}')
			`, id)
			Expect(err).ToNot(HaveOccurred())
		}

		insertProject := func(id, tenant, title string) {
			_, err := pool.Exec(drainCtx, `
				insert into projects (id, tenant, project, name, creator, data)
				values ($1, $2, '', $3, 'system', $4)
			`, id, tenant, id, fmt.Sprintf(`{"spec":{"title":%q}}`, title))
			Expect(err).ToNot(HaveOccurred())
		}

		It("Refreshes the unpublished event count while running", func() {
			tenant := "metrics-" + uuid.New()
			insertChange(tenant, "p-1", "INSERT")
			insertChange(tenant, "p-2", "INSERT")
			Expect(pub.Close()).To(Succeed())
			registry = prometheus.NewRegistry()
			release := make(chan struct{})
			var err error
			pub, err = NewEventPublisher().
				SetLogger(logger).
				SetDatabasePool(pool).
				SetKafkaClient(client).
				SetMetricsRegisterer(registry).
				SetMetricsInterval(20 * time.Millisecond).
				SetPublishCallback(func(ctx context.Context, _ *privatev1.Event) error {
					select {
					case <-release:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				}).
				Build()
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(pub.Close)

			Expect(metricValue(registry, "event_unpublished_count")).To(BeZero())
			cancel, done := startPublisher(pub)
			defer stopPublisher(cancel, done)
			Eventually(func() float64 {
				return metricValue(registry, "event_unpublished_count")
			}).WithTimeout(5 * time.Second).Should(Equal(float64(2)))
			close(release)
			Eventually(countChanges).WithTimeout(5 * time.Second).Should(BeZero())
			Eventually(func() float64 {
				return metricValue(registry, "event_unpublished_count")
			}).WithTimeout(5 * time.Second).Should(BeZero())
		})

		It("Does not claim a row from an open transaction ahead of a committed higher ID", func() {
			tenant := "xmin-" + uuid.New()
			topic := DefaultEventTopicPrefix + tenant
			tx, err := pool.Begin(drainCtx)
			Expect(err).ToNot(HaveOccurred())
			defer func() {
				_ = tx.Rollback(drainCtx)
			}()

			_, err = tx.Exec(drainCtx, `
				insert into changes ("table", op, data)
				values (
					'projects',
					'INSERT',
					jsonb_build_object(
						'id', 'obj-open',
						'tenant', $1::text
					)
				)
			`, tenant)
			Expect(err).ToNot(HaveOccurred())

			insertChange(tenant, "obj-committed", "INSERT")

			cancel, done := startPublisher(pub)
			defer stopPublisher(cancel, done)

			Consistently(func() bool {
				return kafkaHasTopic(client, topic)
			}).WithTimeout(time.Second).WithPolling(50 * time.Millisecond).Should(BeFalse())
			Expect(countChanges()).To(Equal(1))

			var hiddenCount int
			err = tx.QueryRow(drainCtx, "select count(*) from changes").Scan(&hiddenCount)
			Expect(err).ToNot(HaveOccurred())
			Expect(hiddenCount).To(Equal(2))

			err = tx.Commit(drainCtx)
			Expect(err).ToNot(HaveOccurred())
			notifyChanges(drainCtx, pool)

			events := collectKafkaEvents(client, topic, 2)
			Expect(events[0].key).To(Equal("obj-open"))
			Expect(events[1].key).To(Equal("obj-committed"))
			Expect(events[0].headers).To(BeEmpty())
			Expect(events[1].headers).To(BeEmpty())
			Expect(events[0].event.GetProject().GetMetadata().GetTenant()).To(Equal(tenant))
			Expect(events[1].event.GetProject().GetMetadata().GetTenant()).To(Equal(tenant))
			Expect(countChanges()).To(Equal(0))
		})

		It("Re-publishes the same row when the process crashes before delete", func() {
			tenant := "crash-" + uuid.New()
			topic := DefaultEventTopicPrefix + tenant
			changeID := insertChange(tenant, "obj-1", "INSERT")
			Expect(pub.Close()).To(Succeed())

			var err error
			pub, err = NewEventPublisher().
				SetLogger(logger).
				SetDatabasePool(pool).
				SetKafkaClient(client).
				SetMetricsRegisterer(prometheus.NewRegistry()).
				SetPublishCallback(func(context.Context, *privatev1.Event) error {
					return errors.New("simulated crash")
				}).
				Build()
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(pub.Close)

			cancel, done := startPublisher(pub)
			_ = collectKafkaEvents(client, topic, 1)
			Expect(countChanges()).To(Equal(1))
			stopPublisher(cancel, done)

			Expect(pub.Close()).To(Succeed())
			registry = prometheus.NewRegistry()
			pub, err = newTestEventPublisher(pool, client, registry)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(pub.Close)

			cancel, done = startPublisher(pub)
			defer stopPublisher(cancel, done)

			events := collectKafkaEvents(client, topic, 2)
			Expect(events[0].key).To(Equal("obj-1"))
			Expect(events[1].key).To(Equal("obj-1"))
			Expect(events[0].headers).To(BeEmpty())
			Expect(events[1].headers).To(BeEmpty())
			Expect(events[0].event.GetId()).To(Equal(changeID))
			Expect(events[1].event.GetId()).To(Equal(changeID))
			Expect(countChanges()).To(Equal(0))
		})

		It("Rolls back the transaction when processing is canceled", func() {
			tenant := "cancel-" + uuid.New()
			insertChange(tenant, "obj-1", "INSERT")
			Expect(pub.Close()).To(Succeed())
			runCtx, cancel := context.WithCancel(drainCtx)
			defer cancel()
			var err error
			pub, err = NewEventPublisher().
				SetLogger(logger).
				SetDatabasePool(pool).
				SetKafkaClient(client).
				SetMetricsRegisterer(prometheus.NewRegistry()).
				SetPublishCallback(func(ctx context.Context, _ *privatev1.Event) error {
					cancel()
					return ctx.Err()
				}).
				Build()
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(pub.Close)

			done := make(chan error, 1)
			go func() { done <- pub.Run(runCtx) }()
			Eventually(done).WithTimeout(5 * time.Second).Should(Receive(MatchError(context.Canceled)))
			Expect(countChanges()).To(Equal(1))
			// Another transaction must be able to lock the retained row immediately.
			_, err = pool.Exec(drainCtx, "select id from changes for update nowait")
			Expect(err).ToNot(HaveOccurred())
		})

		It("Skips a row that cannot be encoded and continues draining later rows", func() {
			badTenant := "badop-" + uuid.New()
			badTopic := DefaultEventTopicPrefix + badTenant
			tenant := "keep-" + uuid.New()
			topic := DefaultEventTopicPrefix + tenant
			insertChange(badTenant, "obj-1", "TRUNCATE")
			insertChange(tenant, "obj-1", "INSERT")

			cancel, done := startPublisher(pub)
			defer stopPublisher(cancel, done)

			Consistently(func() bool {
				return kafkaHasTopic(client, badTopic)
			}).WithTimeout(300 * time.Millisecond).WithPolling(50 * time.Millisecond).Should(BeFalse())
			Eventually(countChanges).WithTimeout(5 * time.Second).Should(Equal(0))
			Expect(metricValue(registry, "event_publish_errors_total")).To(Equal(float64(1)))

			events := collectKafkaEvents(client, topic, 1)
			Expect(events[0].key).To(Equal("obj-1"))
			Expect(events[0].event.GetProject().GetId()).To(Equal("obj-1"))
			Expect(events[0].event.GetProject().GetMetadata().GetTenant()).To(Equal(tenant))
		})

		It("Skips a row that cannot be routed and continues draining later rows", func() {
			badTenant := "emptyid-" + uuid.New()
			badTopic := DefaultEventTopicPrefix + badTenant
			tenant := "keep-" + uuid.New()
			topic := DefaultEventTopicPrefix + tenant
			insertChange(badTenant, "", "INSERT")
			insertChange(tenant, "obj-1", "INSERT")

			cancel, done := startPublisher(pub)
			defer stopPublisher(cancel, done)

			Consistently(func() bool {
				return kafkaHasTopic(client, badTopic)
			}).WithTimeout(300 * time.Millisecond).WithPolling(50 * time.Millisecond).Should(BeFalse())
			Eventually(countChanges).WithTimeout(5 * time.Second).Should(Equal(0))
			Expect(metricValue(registry, "event_publish_errors_total")).To(Equal(float64(1)))

			events := collectKafkaEvents(client, topic, 1)
			Expect(events[0].key).To(Equal("obj-1"))
			Expect(events[0].event.GetProject().GetId()).To(Equal("obj-1"))
			Expect(events[0].event.GetProject().GetMetadata().GetTenant()).To(Equal(tenant))
		})

		It("Skips non-Event payloads and continues draining later rows", func() {
			skipTenant := "skip-" + uuid.New()
			skipTopic := DefaultEventTopicPrefix + skipTenant
			tenant := "keep-" + uuid.New()
			topic := DefaultEventTopicPrefix + tenant
			_, err := pool.Exec(drainCtx, `
				insert into changes ("table", op, data)
				values (
					'objects',
					'INSERT',
					jsonb_build_object(
						'id', 'obj-1',
						'tenant', $1::text
					)
				)
			`, skipTenant)
			Expect(err).ToNot(HaveOccurred())
			insertChange(tenant, "obj-1", "INSERT")
			Expect(countChanges()).To(Equal(2))

			cancel, done := startPublisher(pub)
			defer stopPublisher(cancel, done)

			Consistently(func() bool {
				return kafkaHasTopic(client, skipTopic)
			}).WithTimeout(300 * time.Millisecond).WithPolling(50 * time.Millisecond).Should(BeFalse())
			Eventually(countChanges).WithTimeout(5 * time.Second).Should(Equal(0))

			events := collectKafkaEvents(client, topic, 1)
			Expect(events[0].key).To(Equal("obj-1"))
			Expect(events[0].headers).To(BeEmpty())
			Expect(events[0].event.GetProject().GetId()).To(Equal("obj-1"))
			Expect(events[0].event.GetProject().GetMetadata().GetTenant()).To(Equal(tenant))
		})

		It("Drains all batches without waiting for another notification", func() {
			tenant := "batch-" + uuid.New()
			topic := DefaultEventTopicPrefix + tenant
			Expect(pub.Close()).To(Succeed())

			var err error
			pub, err = NewEventPublisher().
				SetLogger(logger).
				SetDatabasePool(pool).
				SetKafkaClient(client).
				SetMetricsRegisterer(prometheus.NewRegistry()).
				SetBatchSize(2).
				Build()
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(pub.Close)

			insertChange(tenant, "obj-1", "INSERT")
			insertChange(tenant, "obj-2", "INSERT")
			insertChange(tenant, "obj-3", "INSERT")

			cancel, done := startPublisher(pub)
			defer stopPublisher(cancel, done)

			Eventually(countChanges).WithTimeout(5 * time.Second).Should(Equal(0))

			events := collectKafkaEvents(client, topic, 3)
			Expect(events[0].key).To(Equal("obj-1"))
			Expect(events[1].key).To(Equal("obj-2"))
			Expect(events[2].key).To(Equal("obj-3"))
		})

		It("Publishes to the configured topic prefix", func() {
			tenant := "prefix-" + uuid.New()
			prefix := "custom.events."
			topic := prefix + tenant
			Expect(pub.Close()).To(Succeed())

			var err error
			pub, err = NewEventPublisher().
				SetLogger(logger).
				SetDatabasePool(pool).
				SetKafkaClient(client).
				SetMetricsRegisterer(prometheus.NewRegistry()).
				SetKafkaTopicPrefix(prefix).
				Build()
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(pub.Close)

			insertChange(tenant, "obj-1", "INSERT")
			cancel, done := startPublisher(pub)
			defer stopPublisher(cancel, done)
			Eventually(countChanges).WithTimeout(5 * time.Second).Should(Equal(0))

			events := collectKafkaEvents(client, topic, 1)
			Expect(events[0].topic).To(Equal(topic))
			Expect(events[0].key).To(Equal("obj-1"))
		})

		It("Publishes trigger-written insert, signal, update, and delete events", func() {
			tenant := "acme-" + uuid.New()
			objectID := "p-1"
			topic := DefaultEventTopicPrefix + tenant

			insertTenant(tenant)
			insertProject(objectID, tenant, "Acme")

			tx, err := pool.Begin(drainCtx)
			Expect(err).ToNot(HaveOccurred())
			_, err = tx.Exec(drainCtx, `set local osac.signal = 'on'`)
			Expect(err).ToNot(HaveOccurred())
			_, err = tx.Exec(drainCtx, `
				update projects
				set version = version
				where id = $1
			`, objectID)
			Expect(err).ToNot(HaveOccurred())
			Expect(tx.Commit(drainCtx)).To(Succeed())

			_, err = pool.Exec(drainCtx, `
				update projects
				set data = '{"spec":{"title":"Updated"}}'
				where id = $1
			`, objectID)
			Expect(err).ToNot(HaveOccurred())

			_, err = pool.Exec(drainCtx, `
				delete from projects
				where id = $1
			`, objectID)
			Expect(err).ToNot(HaveOccurred())

			cancel, done := startPublisher(pub)
			defer stopPublisher(cancel, done)
			Eventually(countChanges).WithTimeout(5 * time.Second).Should(Equal(0))

			// Inserting a tenant also creates the empty default project, and that trigger runs
			// before enqueue_change on tenants, so the default project is published first.
			events := collectKafkaEvents(client, topic, 6)
			Expect(events[0].topic).To(Equal(topic))
			Expect(events[0].headers).To(BeEmpty())
			Expect(events[0].event.GetId()).ToNot(BeEmpty())
			Expect(events[0].event.GetType()).To(Equal(privatev1.EventType_EVENT_TYPE_OBJECT_CREATED))
			Expect(events[0].event.GetProject()).ToNot(BeNil())
			Expect(events[0].event.GetProject().GetId()).ToNot(Equal(objectID))
			Expect(events[0].event.GetProject().GetMetadata().GetTenant()).To(Equal(tenant))

			Expect(events[1].key).To(Equal(tenant))
			Expect(events[1].headers).To(BeEmpty())
			Expect(events[1].event.GetType()).To(Equal(privatev1.EventType_EVENT_TYPE_OBJECT_CREATED))
			Expect(events[1].event.GetTenant().GetId()).To(Equal(tenant))

			Expect(events[2].key).To(Equal(objectID))
			Expect(events[2].event.GetType()).To(Equal(privatev1.EventType_EVENT_TYPE_OBJECT_CREATED))
			Expect(events[2].event.GetProject().GetId()).To(Equal(objectID))
			Expect(events[2].event.GetProject().GetMetadata().GetTenant()).To(Equal(tenant))
			Expect(events[2].event.GetProject().GetSpec().GetTitle()).To(Equal("Acme"))

			Expect(events[3].key).To(Equal(objectID))
			Expect(events[3].event.GetType()).To(Equal(privatev1.EventType_EVENT_TYPE_OBJECT_SIGNALED))
			Expect(events[3].event.GetProject().GetMetadata().GetVersion()).To(Equal(int32(0)))
			Expect(events[3].event.GetProject().GetSpec().GetTitle()).To(Equal("Acme"))

			Expect(events[4].key).To(Equal(objectID))
			Expect(events[4].event.GetType()).To(Equal(privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED))
			Expect(events[4].event.GetProject().GetSpec().GetTitle()).To(Equal("Updated"))

			Expect(events[5].key).To(Equal(objectID))
			Expect(events[5].event.GetType()).To(Equal(privatev1.EventType_EVENT_TYPE_OBJECT_DELETED))
			Expect(events[5].event.GetProject().GetSpec().GetTitle()).To(Equal("Updated"))
		})

		It("Publishes object changes through the listener loop", func() {
			tenant := "acme-" + uuid.New()
			objectID := "p-2"
			topic := DefaultEventTopicPrefix + tenant
			runCtx, cancel := context.WithCancel(drainCtx)
			defer cancel()

			done := make(chan error, 1)
			go func() {
				done <- pub.Run(runCtx)
			}()

			insertTenant(tenant)
			insertProject(objectID, tenant, "Listened")

			events := collectKafkaEvents(client, topic, 3)
			Expect(events[1].key).To(Equal(tenant))
			Expect(events[1].event.GetType()).To(Equal(privatev1.EventType_EVENT_TYPE_OBJECT_CREATED))
			Expect(events[1].event.GetTenant().GetId()).To(Equal(tenant))
			Expect(events[2].key).To(Equal(objectID))
			Expect(events[2].event.GetType()).To(Equal(privatev1.EventType_EVENT_TYPE_OBJECT_CREATED))
			Expect(events[2].event.GetProject().GetId()).To(Equal(objectID))
			Expect(events[2].event.GetProject().GetMetadata().GetTenant()).To(Equal(tenant))
			Expect(events[2].event.GetProject().GetSpec().GetTitle()).To(Equal("Listened"))

			cancel()
			Eventually(done).Should(Receive(MatchError(context.Canceled)))
		})

		It("Normalizes custom table and channel names and publishes changes", func() {
			table := "custom_changes"
			channel := "custom_changes_channel"
			Expect(pub.Close()).To(Succeed())

			var err error
			pub, err = NewEventPublisher().
				SetLogger(logger).
				SetDatabasePool(pool).
				SetKafkaClient(client).
				SetMetricsRegisterer(prometheus.NewRegistry()).
				SetTable(strings.ToUpper(table)).
				SetChannel(strings.ToUpper(channel)).
				Build()
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(pub.Close)

			tenant := "custom-" + uuid.New()
			topic := DefaultEventTopicPrefix + tenant
			insertChange(tenant, "from-default", "INSERT")

			cancel, done := startPublisher(pub)
			defer stopPublisher(cancel, done)
			Consistently(done, "50ms").ShouldNot(Receive())

			_, err = pool.Exec(drainCtx, `
				create table custom_changes (
					id uuid primary key default uuidv7(),
					"table" text not null,
					op text not null,
					data jsonb not null,
					"timestamp" timestamptz not null default now()
				)
			`)
			Expect(err).ToNot(HaveOccurred())
			_, err = pool.Exec(drainCtx, `
				insert into custom_changes ("table", op, data)
				values (
					'projects',
					'INSERT',
					jsonb_build_object(
						'id', $2::text,
						'tenant', $1::text
					)
				)
			`, tenant, "from-custom")
			Expect(err).ToNot(HaveOccurred())
			_, err = pool.Exec(drainCtx, "select pg_notify($1, 'test')", channel)
			Expect(err).ToNot(HaveOccurred())

			events := collectKafkaEvents(client, topic, 1)
			Expect(events[0].key).To(Equal("from-custom"))

			var customCount int
			err = pool.QueryRow(drainCtx, "select count(*) from custom_changes").Scan(&customCount)
			Expect(err).ToNot(HaveOccurred())
			Expect(customCount).To(Equal(0))
			Expect(countChanges()).To(Equal(1))
		})
	})
})
