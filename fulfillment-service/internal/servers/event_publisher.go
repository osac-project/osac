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
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/IBM/sarama"
	"github.com/cenkalti/backoff/v4"
	"github.com/gobuffalo/flect"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/work"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// EventPublisherBuilder contains the data and logic needed to create an EventPublisher that drains the changes table
// to Kafka.
//
// It generates the following Prometheus metrics:
//
//	event_unpublished_count - Number of events waiting to be published.
//	event_publish_latency_seconds - Time from change row creation to successful Kafka publish, in seconds.
//	event_publish_total - Number of events published to Kafka.
//	event_publish_errors_total - Number of errors publishing events, including rows that cannot be encoded or routed.
//
// The unpublished event count is refreshed periodically. Use the SetMetricsInterval method to change that interval. It
// defaults to 15 seconds.
//
// To set the Prometheus registerer use the SetMetricsRegisterer method. This is optional and defaults to the Prometheus
// default registerer.
//
// The latency histogram buckets contain an `le` label that indicates the upper bound. For example if the `le` label is
// `1` then the value will be the number of events that were published in less than one second after the change row was
// created.
//
// To calculate the average publish latency during the last 10 minutes, for example, use a Prometheus expression like
// this:
//
//	rate(event_publish_latency_seconds_sum[10m]) / rate(event_publish_latency_seconds_count[10m])
//
// Don't create instances of this type directly, use the NewEventPublisher function instead.
type EventPublisherBuilder struct {
	logger              *slog.Logger
	dbPool              *pgxpool.Pool
	dbTable             string
	dbChannel           string
	kafkaClient         sarama.Client
	kafkaTopicPrefix    string
	listenWaitTimeout   time.Duration
	listenRetryInterval time.Duration
	batchSize           int
	publishCallback     func(context.Context, *privatev1.Event) error
	metricsRegisterer   prometheus.Registerer
	metricsInterval     time.Duration
}

// EventPublisher drains the changes table to Kafka. Don't create instances of this type directly, use the
// NewEventPublisher function instead.
type EventPublisher struct {
	logger           *slog.Logger
	dbPool           *pgxpool.Pool
	dbTable          string
	dbChannel        string
	fetchSQL         string
	deleteSQL        string
	countSQL         string
	existsSQL        string
	listenSQL        string
	listenConn       *pgxpool.Conn
	drainLoop        *work.Loop
	listenLoop       *work.Loop
	metricsLoop      *work.Loop
	kafkaProducer    sarama.SyncProducer
	kafkaTopicPrefix string
	batchSize        int
	publishCallback  func(context.Context, *privatev1.Event) error
	payloadOneof     protoreflect.OneofDescriptor
	payloadFields    map[string]protoreflect.FieldDescriptor
	metrics          struct {
		unpublishedCount prometheus.Gauge
		publishLatency   prometheus.Histogram
		publishTotal     *prometheus.CounterVec
		publishErrors    *prometheus.CounterVec
	}
}

// eventPublisherChange represents a row of the changes table.
type eventPublisherChange struct {
	id        string
	table     string
	op        string
	data      []byte
	timestamp time.Time
}

// eventPublisherObjectJson is the JSON shape of the 'data' column of the changes table.
type eventPublisherObjectJson struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	Tenant            string            `json:"tenant"`
	Project           string            `json:"project"`
	Creator           string            `json:"creator"`
	CreationTimestamp time.Time         `json:"creation_timestamp"`
	DeletionTimestamp time.Time         `json:"deletion_timestamp"`
	Finalizers        []string          `json:"finalizers"`
	Labels            map[string]string `json:"labels"`
	Annotations       map[string]string `json:"annotations"`
	Version           int32             `json:"version"`
	Data              json.RawMessage   `json:"data"`
}

// NewEventPublisher creates a builder that can then be used to configure and create an EventPublisher.
func NewEventPublisher() *EventPublisherBuilder {
	return &EventPublisherBuilder{
		kafkaTopicPrefix:    DefaultEventTopicPrefix,
		dbTable:             defaultEventPublisherTable,
		dbChannel:           defaultEventPublisherChannel,
		listenWaitTimeout:   defaultListenWaitTimeout,
		listenRetryInterval: defaultListenRetryInterval,
		batchSize:           defaultBatchSize,
		metricsInterval:     defaultMetricsInterval,
	}
}

// SetLogger sets the logger. This is mandatory.
func (b *EventPublisherBuilder) SetLogger(value *slog.Logger) *EventPublisherBuilder {
	b.logger = value
	return b
}

// SetDatabasePool sets the database connection pool used to fetch and delete changes. This is mandatory.
func (b *EventPublisherBuilder) SetDatabasePool(value *pgxpool.Pool) *EventPublisherBuilder {
	b.dbPool = value
	return b
}

// SetTable sets the name of the PostgreSQL table that stores change rows. This is optional and defaults to 'changes'.
func (b *EventPublisherBuilder) SetTable(value string) *EventPublisherBuilder {
	b.dbTable = value
	return b
}

// SetChannel sets the PostgreSQL LISTEN/NOTIFY channel used to signal that change rows are available. This is optional
// and defaults to 'changes'.
func (b *EventPublisherBuilder) SetChannel(value string) *EventPublisherBuilder {
	b.dbChannel = value
	return b
}

// SetKafkaClient sets the client used to connect to Kafka to publish events. This is mandatory.
func (b *EventPublisherBuilder) SetKafkaClient(value sarama.Client) *EventPublisherBuilder {
	b.kafkaClient = value
	return b
}

// SetKafkaTopicPrefix sets the prefix prepended to the tenant identifier to form Kafka topic names. This is optional
// and defaults to DefaultEventTopicPrefix.
func (b *EventPublisherBuilder) SetKafkaTopicPrefix(value string) *EventPublisherBuilder {
	b.kafkaTopicPrefix = value
	return b
}

// SetListenWaitTimeout sets how long the drain loop sleeps after an empty drain before it looks for unpublished rows
// again. A PostgreSQL notification received by the listen loop interrupts this wait. This is optional, defaults to five
// seconds, and there is usually no need to change it.
func (b *EventPublisherBuilder) SetListenWaitTimeout(value time.Duration) *EventPublisherBuilder {
	b.listenWaitTimeout = value
	return b
}

// SetListenRetryInterval sets how long to wait after a PostgreSQL LISTEN failure before reconnecting. This is optional,
// defaults to five seconds, and there is usually no need to change it.
func (b *EventPublisherBuilder) SetListenRetryInterval(value time.Duration) *EventPublisherBuilder {
	b.listenRetryInterval = value
	return b
}

// SetBatchSize sets the maximum number of changes fetched per drain. This is optional and defaults to 100.
func (b *EventPublisherBuilder) SetBatchSize(value int) *EventPublisherBuilder {
	b.batchSize = value
	return b
}

// SetPublishCallback sets a function that is called after an event has been written to Kafka and before the
// corresponding row is deleted from the changes table. If the function returns an error the drain stops and the row is
// left in the table so it can be published again. This is optional and intended mostly for unit tests.
func (b *EventPublisherBuilder) SetPublishCallback(
	value func(context.Context, *privatev1.Event) error) *EventPublisherBuilder {
	b.publishCallback = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer for publisher metrics. This is optional and defaults to the
// Prometheus default registerer.
func (b *EventPublisherBuilder) SetMetricsRegisterer(value prometheus.Registerer) *EventPublisherBuilder {
	b.metricsRegisterer = value
	return b
}

// SetMetricsInterval sets how often metrics whose values are calculated periodically are refreshed, such as the
// unpublished event count. This is optional and defaults to 15 seconds. seconds.
func (b *EventPublisherBuilder) SetMetricsInterval(value time.Duration) *EventPublisherBuilder {
	b.metricsInterval = value
	return b
}

// Build uses the data stored in the builder to create a new EventPublisher.
func (b *EventPublisherBuilder) Build() (result *EventPublisher, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.dbPool == nil {
		err = errors.New("database pool is mandatory")
		return
	}
	if b.listenWaitTimeout <= 0 {
		err = fmt.Errorf("listen wait timeout should be positive, but it is %s", b.listenWaitTimeout)
		return
	}
	if b.listenRetryInterval <= 0 {
		err = fmt.Errorf("listen retry interval should be positive, but it is %s", b.listenRetryInterval)
		return
	}
	if b.batchSize <= 0 {
		err = fmt.Errorf("batch size should be positive, but it is %d", b.batchSize)
		return
	}
	if b.metricsInterval <= 0 {
		err = fmt.Errorf("metrics interval should be positive, but it is %s", b.metricsInterval)
		return
	}
	if b.kafkaTopicPrefix == "" {
		err = errors.New("topic prefix is mandatory")
		return
	}
	if b.dbTable == "" {
		err = errors.New("table is mandatory")
		return
	}
	dbTable := strings.ToLower(b.dbTable)
	err = b.checkName(dbTable)
	if err != nil {
		err = fmt.Errorf("table %w", err)
		return
	}
	if b.dbChannel == "" {
		err = errors.New("channel is mandatory")
		return
	}
	dbChannel := strings.ToLower(b.dbChannel)
	err = b.checkName(dbChannel)
	if err != nil {
		err = fmt.Errorf("channel %w", err)
		return
	}
	if b.kafkaClient == nil {
		err = errors.New("kafka client is mandatory")
		return
	}

	// Calculate the SQL statements:
	fetchSQL := b.calculateFetchSQL(dbTable)
	deleteSQL := b.calculateDeleteSQL(dbTable)
	countSQL := b.calculateCountSQL(dbTable)
	existsSQL := b.calculateExistsSQL(dbTable)
	listenSQL := b.calculateListenSQL(dbChannel)

	// Create the Kafka kafka message producer:
	kafkaProducer, err := sarama.NewSyncProducerFromClient(b.kafkaClient)
	if err != nil {
		err = fmt.Errorf("failed to create Kafka producer: %w", err)
		return
	}

	// Find the descriptor of the event type:
	eventDesc := (*privatev1.Event)(nil).ProtoReflect().Descriptor()

	// Find the descriptor of the payload oneof of the events:
	payloadOneof, err := b.calculatePayloadOneof(eventDesc)
	if err != nil {
		err = fmt.Errorf("failed to calculate payload oneof: %w", err)
		return
	}

	// Find the descriptors of the payload fields of the events, so that we don't need to look them up every time we
	// process a change:
	payloadFields, err := b.calculatePayloadFields(payloadOneof)
	if err != nil {
		err = fmt.Errorf("failed to calculate payload fields: %w", err)
		return
	}

	// Create and populate the object:
	result = &EventPublisher{
		logger:           b.logger,
		dbPool:           b.dbPool,
		dbTable:          dbTable,
		dbChannel:        dbChannel,
		kafkaProducer:    kafkaProducer,
		kafkaTopicPrefix: b.kafkaTopicPrefix,
		fetchSQL:         fetchSQL,
		deleteSQL:        deleteSQL,
		countSQL:         countSQL,
		existsSQL:        existsSQL,
		listenSQL:        listenSQL,
		batchSize:        b.batchSize,
		publishCallback:  b.publishCallback,
		payloadOneof:     payloadOneof,
		payloadFields:    payloadFields,
	}

	// Set up metrics:
	metricsRegisterer := b.metricsRegisterer
	if metricsRegisterer == nil {
		metricsRegisterer = prometheus.DefaultRegisterer
	}
	err = result.registerUnpublishedCountMetric(metricsRegisterer)
	if err != nil {
		result = nil
		return
	}
	err = result.registerPublishLatencyMetric(metricsRegisterer)
	if err != nil {
		result = nil
		return
	}
	err = result.registerPublishTotalMetric(metricsRegisterer)
	if err != nil {
		result = nil
		return
	}
	err = result.registerPublishErrorsMetric(metricsRegisterer)
	if err != nil {
		result = nil
		return
	}

	// Create the drain, listen, and metrics loops. The drain loop keeps fetching batches until the table is empty,
	// then sleeps until a notification kicks it or the wait timeout expires. The listen loop waits for PostgreSQL
	// notifications and kicks the drain loop. The metrics loop refreshes gauges that are calculated periodically,
	// such as the unpublished event count.
	result.drainLoop, err = work.NewLoop().
		SetLogger(b.logger).
		SetName("drain").
		SetInterval(b.listenWaitTimeout).
		SetWorkFunc(result.drainWorkFunc).
		Build()
	if err != nil {
		closeErr := result.Close()
		result = nil
		err = fmt.Errorf("failed to create drain loop: %w", err)
		if closeErr != nil {
			err = fmt.Errorf("%w; failed to close publisher: %w", err, closeErr)
		}
		return
	}
	result.listenLoop, err = work.NewLoop().
		SetLogger(b.logger).
		SetName("listen").
		SetInterval(b.listenRetryInterval).
		SetWorkFunc(result.listenWorkFunc).
		Build()
	if err != nil {
		closeErr := result.Close()
		result = nil
		err = fmt.Errorf("failed to create listen loop: %w", err)
		if closeErr != nil {
			err = fmt.Errorf("%w; failed to close publisher: %w", err, closeErr)
		}
		return
	}
	result.metricsLoop, err = work.NewLoop().
		SetLogger(b.logger).
		SetName("metrics").
		SetInterval(b.metricsInterval).
		SetWorkFunc(result.metricsWorkFunc).
		Build()
	if err != nil {
		closeErr := result.Close()
		result = nil
		err = fmt.Errorf("failed to create metrics loop: %w", err)
		if closeErr != nil {
			err = fmt.Errorf("%w; failed to close publisher: %w", err, closeErr)
		}
		return
	}

	return
}

// calculatePayloadOneof calculates the descriptor of the payload oneof of the events.
func (b *EventPublisherBuilder) calculatePayloadOneof(
	eventDesc protoreflect.MessageDescriptor) (result protoreflect.OneofDescriptor, err error) {
	oneofDesc := eventDesc.Oneofs().ByName("payload")
	if oneofDesc == nil {
		err = errors.New("event payload oneof not found")
		return
	}
	result = oneofDesc
	return
}

// calculatePayloadFields calculates the protobuf message type for each table and returns a map of table names to
// payload field descriptors.
func (b *EventPublisherBuilder) calculatePayloadFields(
	oneofDesc protoreflect.OneofDescriptor) (result map[string]protoreflect.FieldDescriptor, err error) {
	fieldDescs := map[string]protoreflect.FieldDescriptor{}
	for i := range oneofDesc.Fields().Len() {
		fieldDesc := oneofDesc.Fields().Get(i)
		fieldName := string(fieldDesc.Name())
		messageDesc := fieldDesc.Message()
		if messageDesc == nil {
			err = fmt.Errorf("event payload field '%s' is not a message", fieldName)
			continue
		}
		messageType := string(messageDesc.Name())
		tableName := flect.Pluralize(flect.Underscore(messageType))
		fieldDescs[tableName] = fieldDesc
	}
	result = fieldDescs
	return
}

// calculateFetchSQL returns the query used to fetch changes from the configured table.
//
// The 'xmin' where clause skips rows that are not yet safe to publish. PostgreSQL stores the identifier of the
// inserting transaction in 'xmin'. 'pg_snapshot_xmin' is the oldest transaction still in progress, so a row with 'xmin'
// greater than or equal to that value belongs to a transaction that has not finished from the point of view of all
// snapshots. Without this filter the publisher could fetch a committed later UUID while an earlier insert is still
// open, and publishing that later row first would reorder events. The 'xid' values are compared as integers through a
// text cast because PostgreSQL does not allow a direct comparison of the 'xid' type in this expression.
func (b *EventPublisherBuilder) calculateFetchSQL(dbTable string) string {
	return fmt.Sprintf(`
		select
			"id",
			"table",
			"op",
			"data",
			"timestamp"
		from
			%s
		where
			xmin::text::bigint < pg_snapshot_xmin(pg_current_snapshot())::text::bigint
		order by
			id
		for update
		skip locked
		limit $1
		`,
		dbTable,
	)
}

// calculateDeleteSQL returns the query used to delete a change from the configured table.
func (b *EventPublisherBuilder) calculateDeleteSQL(dbTable string) string {
	return fmt.Sprintf("delete from %s where id = $1", dbTable)
}

// calculateCountSQL returns the query used to count the number of changes in the configured table.
func (b *EventPublisherBuilder) calculateCountSQL(dbTable string) string {
	return fmt.Sprintf("select count(*) from %s", dbTable)
}

// calculateExistsSQL returns the query used to check if the configured table exists.
func (b *EventPublisherBuilder) calculateExistsSQL(dbTable string) string {
	return fmt.Sprintf(`
		select exists (
			select
				1
			from
				pg_catalog.pg_class c
			join
				pg_catalog.pg_namespace n on n.oid = c.relnamespace
			where
				n.nspname = 'public' and
				c.relkind = 'r' and
				c.relname = '%s'
		)
		`,
		dbTable,
	)
}

// calculateListenSQL returns the query used to listen for changes on the configured channel.
func (b *EventPublisherBuilder) calculateListenSQL(dbChannel string) string {
	return fmt.Sprintf("listen %s", dbChannel)
}

// checkName returns an error if the given name is not a valid unquoted PostgreSQL identifier. It must not start with a
// digit, and it may contain only letters, digits and underscores.
func (b *EventPublisherBuilder) checkName(name string) error {
	for i, r := range name {
		if i == 0 && unicode.IsDigit(r) {
			return fmt.Errorf("'%s' should not start with a digit", name)
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return fmt.Errorf("'%s' should contain only letters, digits and underscores", name)
		}
	}
	return nil
}

// Close releases all the resources used by the publisher.
func (p *EventPublisher) Close() error {
	if p.kafkaProducer == nil {
		return nil
	}
	err := p.kafkaProducer.Close()
	p.kafkaProducer = nil
	return err
}

// Run waits until the changes table exists, then runs the drain, listen, and metrics loops until the context is
// canceled.
func (p *EventPublisher) Run(ctx context.Context) error {
	// Wait until the changes table exists:
	err := p.waitForTable(ctx)
	if err != nil {
		return err
	}

	// Run the drain, listen, and metrics loops in parallel:
	var wg sync.WaitGroup
	wg.Go(func() {
		defer p.releaseListenConn()
		err := p.listenLoop.Run(ctx)
		if err != nil {
			p.logger.ErrorContext(
				ctx,
				"Listen loop failed",
				slog.Any("error", err),
			)
		}
	})
	wg.Go(func() {
		err := p.metricsLoop.Run(ctx)
		if err != nil {
			p.logger.ErrorContext(
				ctx,
				"Metrics loop failed",
				slog.Any("error", err),
			)
		}
	})
	wg.Go(func() {
		err := p.drainLoop.Run(ctx)
		if err != nil {
			p.logger.ErrorContext(
				ctx,
				"Drain loop failed",
				slog.Any("error", err),
			)
		}
	})
	wg.Wait()

	// Return the error from the context if it is done:
	return ctx.Err()
}

// listenWorkFunc connects to PostgreSQL, listens for change notifications, and kicks the drain loop each time a
// notification arrives. It also kicks once after a successful listen so that rows committed while the connection was
// down are not left waiting for the next periodic drain. The work loop retries this function after listen failures.
func (p *EventPublisher) listenWorkFunc(ctx context.Context) error {
	err := p.listen(ctx)
	if err != nil {
		return err
	}
	p.logger.DebugContext(ctx, "Listen succeeded")
	p.drainLoop.Kick()
	for {
		_, err := p.listenConn.Conn().WaitForNotification(ctx)
		if err != nil {
			return err
		}
		p.drainLoop.Kick()
	}
}

// listen acquires a connection from the pool and runs LISTEN. PostgreSQL LISTEN is session-scoped, so the connection is
// held until the listen loop stops and then returned to the pool.
func (p *EventPublisher) listen(ctx context.Context) error {
	p.releaseListenConn()
	conn, err := p.dbPool.Acquire(ctx)
	if err != nil {
		return err
	}
	_, err = conn.Exec(ctx, p.listenSQL)
	if err != nil {
		conn.Release()
		return err
	}
	p.listenConn = conn
	return nil
}

// releaseListenConn releases the listen connection and sets it to nil.
func (p *EventPublisher) releaseListenConn() {
	if p.listenConn == nil {
		return
	}
	p.listenConn.Release()
	p.listenConn = nil
}

// drainWorkFunc fetches and processes batches while there may be more changes. A full batch causes the next batch to be
// fetched immediately so a backlog larger than the batch size does not wait for the next notification or wait timeout.
func (p *EventPublisher) drainWorkFunc(ctx context.Context) error {
	for {
		more, err := p.drain(ctx)
		if err != nil {
			return err
		}
		if !more {
			return nil
		}
	}
}

// drain reads changes from the database and processes them. It returns true if the batch was full and there may be more
// changes to process, and an error if the transaction fails.
func (p *EventPublisher) drain(ctx context.Context) (more bool, err error) {
	// Begin a transaction and remember to commit on success, or rollback on error:
	tx, err := p.dbPool.Begin(ctx)
	if err != nil {
		err = fmt.Errorf("failed to begin drain transaction: %w", err)
		return
	}
	defer func() {
		panicValue := recover()
		if panicValue != nil {
			rollbackErr := tx.Rollback(ctx)
			if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
				p.logger.ErrorContext(
					ctx,
					"Failed to rollback drain transaction after panic",
					slog.Any("error", rollbackErr),
				)
			}
			panic(panicValue)
		}
		if err == nil {
			err = tx.Commit(ctx)
			if err != nil {
				more = false
				err = fmt.Errorf("failed to commit drain transaction: %w", err)
				return
			}
			p.updateUnpublishedMetric(ctx)
			return
		}
		rollbackErr := tx.Rollback(ctx)
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			p.logger.ErrorContext(
				ctx,
				"Failed to rollback drain transaction",
				slog.Any("error", rollbackErr),
			)
		}
	}()

	// Fetch the next batch of changes:
	rows, err := tx.Query(ctx, p.fetchSQL, p.batchSize)
	if err != nil {
		return false, fmt.Errorf("failed to fetch changes: %w", err)
	}
	changes, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (change eventPublisherChange, err error) {
		err = row.Scan(
			&change.id,
			&change.table,
			&change.op,
			&change.data,
			&change.timestamp,
		)
		return
	})
	if err != nil {
		return false, fmt.Errorf("failed to scan change rows: %w", err)
	}

	// Process the changes:
	for _, change := range changes {
		if err = ctx.Err(); err != nil {
			err = fmt.Errorf("publish aborted: %w", err)
			return
		}
		err = p.processChange(ctx, tx, &change)
		if err != nil {
			return
		}
	}

	more = len(changes) == p.batchSize
	return
}

// processChange converts a change into an event and writes it to Kafka.
//
// Changes that cannot be converted are reated as permanently unpublishable. Returning an error here would abort the
// drain and leave that row at the head of the table, so later changes would never be published. Instead the failure is
// written to the log, the 'event_publish_errors_total' metric is incremented, and the row is deleted so the drain can
// continue. Kafka send failures are not skipped: they abort the drain and leave the row in the table so it can be
// retried.
//
// The 'event_publish_errors_total' metric is intended for a future alert when unpublishable rows appear, for example:
//
//	increase(event_publish_errors_total[5m]) > 0
func (p *EventPublisher) processChange(ctx context.Context, tx pgx.Tx, change *eventPublisherChange) error {
	// Convert the change to an event:
	event, err := p.convertChange(change)
	if err != nil {
		p.logger.ErrorContext(
			ctx,
			"Failed to convert change",
			slog.String("id", change.id),
			slog.String("table", change.table),
			slog.String("op", change.op),
			slog.String("error", err.Error()),
		)
		p.metrics.publishErrors.With(nil).Inc()
		return p.deleteChange(ctx, tx, change.id)
	}

	// Find the tenant and the identifier object, as we need them to calculate the Kafka topic and message key:
	tenant, id, err := p.payloadIdentity(event)
	if err != nil {
		p.logger.ErrorContext(
			ctx,
			"Failed to obtain tenant and identifier from change",
			slog.String("id", change.id),
			slog.String("table", change.table),
			slog.String("op", change.op),
			slog.String("error", err.Error()),
		)
		p.metrics.publishErrors.With(nil).Inc()
		return p.deleteChange(ctx, tx, change.id)
	}
	value, err := proto.Marshal(event)
	if err != nil {
		p.logger.ErrorContext(
			ctx,
			"Failed to marshal event",
			slog.String("id", change.id),
			slog.String("table", change.table),
			slog.String("op", change.op),
			slog.String("error", err.Error()),
		)
		p.metrics.publishErrors.With(nil).Inc()
		return p.deleteChange(ctx, tx, change.id)
	}

	// Calculate the Kafka topic and the message key:
	topic := p.kafkaTopicPrefix + tenant
	_, _, err = p.kafkaProducer.SendMessage(&sarama.ProducerMessage{
		Topic: topic,
		Key:   sarama.StringEncoder(id),
		Value: sarama.ByteEncoder(value),
	})
	if err != nil {
		p.metrics.publishErrors.With(nil).Inc()
		return fmt.Errorf("failed to publish change %s: %w", change.id, err)
	}
	if p.logger.Enabled(ctx, slog.LevelDebug) {
		p.logger.DebugContext(
			ctx,
			"Published event",
			slog.String("id", change.id),
			slog.String("topic", topic),
			slog.Any("!event", event),
		)
	}
	if p.publishCallback != nil {
		err = p.publishCallback(ctx, event)
		if err != nil {
			return err
		}
	}
	err = p.deleteChange(ctx, tx, change.id)
	if err != nil {
		p.metrics.publishErrors.With(nil).Inc()
		return err
	}
	p.metrics.publishTotal.With(nil).Inc()
	p.metrics.publishLatency.Observe(time.Since(change.timestamp).Seconds())
	return nil
}

func (p *EventPublisher) deleteChange(ctx context.Context, tx pgx.Tx, id string) error {
	_, err := tx.Exec(ctx, p.deleteSQL, id)
	if err != nil {
		return fmt.Errorf("failed to delete change %s: %w", id, err)
	}
	return nil
}

// metricsWorkFunc refreshes gauges whose values are calculated periodically, such as the unpublished event count.
func (p *EventPublisher) metricsWorkFunc(ctx context.Context) error {
	p.updateUnpublishedMetric(ctx)
	return nil
}

func (p *EventPublisher) updateUnpublishedMetric(ctx context.Context) {
	var count int64
	row := p.dbPool.QueryRow(ctx, p.countSQL)
	err := row.Scan(&count)
	if err != nil {
		p.logger.ErrorContext(
			ctx,
			"Failed to count unpublished change rows",
			slog.Any("error", err),
		)
		return
	}
	p.metrics.unpublishedCount.Set(float64(count))
}

// waitForTable blocks until the changes table exists. The table is created by database migrations that run only in the
// gRPC server process. The publisher is a separate process, so it can start before those migrations have finished.
func (p *EventPublisher) waitForTable(ctx context.Context) error {
	p.logger.InfoContext(
		ctx,
		"Waiting for changes table",
		slog.String("table", p.dbTable),
	)
	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = 100 * time.Millisecond
	bo.MaxInterval = 5 * time.Second
	bo.MaxElapsedTime = 0
	err := backoff.RetryNotify(
		func() error {
			exists, err := p.tableExists(ctx)
			if err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("changes table '%s' does not exist", p.dbTable)
			}
			return nil
		},
		backoff.WithContext(bo, ctx),
		func(err error, delay time.Duration) {
			p.logger.InfoContext(
				ctx,
				"Changes table is not ready yet",
				slog.String("table", p.dbTable),
				slog.Any("error", err),
				slog.Duration("retry_after", delay),
			)
		},
	)
	if err != nil {
		return err
	}
	p.logger.InfoContext(
		ctx,
		"Changes table is ready",
		slog.String("table", p.dbTable),
	)
	return nil
}

// tableExists checks if the configured changes table exists.
func (p *EventPublisher) tableExists(ctx context.Context) (exists bool, err error) {
	err = p.dbPool.QueryRow(ctx, p.existsSQL).Scan(&exists)
	return
}

// convertChange converts a change row into a protobuf Event.
//
// The Event id is copied from the durable change row so retries of that row publish the same event identifier.
//
// Sensitive fields are redacted, so Kafka does not receive secret data, hub kubeconfigs, storage backend passwords,
// user credentials, or tenant break-glass credentials.
func (p *EventPublisher) convertChange(change *eventPublisherChange) (event *privatev1.Event, err error) {
	if len(change.data) == 0 {
		err = errors.New("change data is empty")
		return
	}
	field := p.payloadFields[change.table]
	if field == nil {
		err = fmt.Errorf("failed to find payload field for table '%s'", change.table)
		return
	}
	kind, err := p.convertChangeOpToEventType(change.op)
	if err != nil {
		return
	}
	object, err := p.convertChangeData(field, change.data)
	if err != nil {
		return
	}
	event = privatev1.Event_builder{
		Id:        change.id,
		Type:      kind,
		Timestamp: timestamppb.New(change.timestamp),
	}.Build()
	event.ProtoReflect().Set(field, protoreflect.ValueOfMessage(object.ProtoReflect()))
	p.redactEvent(event)
	return
}

// redactEvent removes from the event secrets that should not be written to Kafka.
func (p *EventPublisher) redactEvent(event *privatev1.Event) {
	switch {
	case event.HasSecret():
		event.GetSecret().SetData(nil)
	case event.HasHub():
		spec := event.GetHub().GetSpec()
		if spec != nil {
			spec.SetKubeconfig(nil)
		}
	case event.HasStorageBackend():
		spec := event.GetStorageBackend().GetSpec()
		if spec == nil {
			return
		}
		credentials := spec.GetCredentials()
		if credentials == nil {
			return
		}
		credentials.SetPassword("")
	case event.HasUser():
		spec := event.GetUser().GetSpec()
		if spec != nil {
			spec.ClearCredentials()
		}
	case event.HasTenant():
		status := event.GetTenant().GetStatus()
		if status != nil {
			status.ClearBreakGlassCredentials()
		}
	}
}

// convertChangeOpToEventType converts a change operation to an event type.
func (p *EventPublisher) convertChangeOpToEventType(op string) (result privatev1.EventType, err error) {
	switch op {
	case eventPublisherOpInsert:
		result = privatev1.EventType_EVENT_TYPE_OBJECT_CREATED
	case eventPublisherOpUpdate:
		result = privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED
	case eventPublisherOpDelete:
		result = privatev1.EventType_EVENT_TYPE_OBJECT_DELETED
	case eventPublisherOpSignal:
		result = privatev1.EventType_EVENT_TYPE_OBJECT_SIGNALED
	default:
		err = fmt.Errorf("unknown change operation '%s'", op)
	}
	return
}

// convertChangeData converts the raw data of a change into the object that will be the payload of the event.
func (p *EventPublisher) convertChangeData(field protoreflect.FieldDescriptor, raw []byte) (result objectIface,
	err error) {
	// Parse the raw data:
	var data eventPublisherObjectJson
	err = json.Unmarshal(raw, &data)
	if err != nil {
		err = fmt.Errorf("failed to unmarshal data: %w", err)
		return
	}

	// Create the metadata:
	metadata := privatev1.Metadata_builder{
		Name:        data.Name,
		Creator:     data.Creator,
		Tenant:      data.Tenant,
		Project:     data.Project,
		Finalizers:  data.Finalizers,
		Labels:      data.Labels,
		Annotations: data.Annotations,
		Version:     data.Version,
	}.Build()
	if !data.CreationTimestamp.IsZero() && data.CreationTimestamp.Unix() != 0 {
		metadata.SetCreationTimestamp(timestamppb.New(data.CreationTimestamp))
	}
	if !data.DeletionTimestamp.IsZero() && data.DeletionTimestamp.Unix() != 0 {
		metadata.SetDeletionTimestamp(timestamppb.New(data.DeletionTimestamp))
	}

	// Create the object:
	messageDesc := field.Message()
	messageName := messageDesc.FullName()
	messageType, err := protoregistry.GlobalTypes.FindMessageByName(messageName)
	if err != nil {
		err = fmt.Errorf("failed to find payload message type '%s': %w", messageName, err)
		return
	}
	object := messageType.New().Interface()
	if len(data.Data) > 0 {
		err = protojson.UnmarshalOptions{DiscardUnknown: true}.Unmarshal(data.Data, object)
		if err != nil {
			err = fmt.Errorf("failed to unmarshal object data: %w", err)
			return
		}
	}
	typed, ok := object.(objectIface)
	if !ok {
		err = fmt.Errorf("payload type '%T' does not support identity fields", object)
		return
	}
	typed.SetId(data.ID)
	typed.SetMetadata(metadata)
	result = typed
	return
}

func (p *EventPublisher) payloadIdentity(event *privatev1.Event) (objectTenant, objectID string, err error) {
	eventReflect := event.ProtoReflect()
	fieldDesc := eventReflect.WhichOneof(p.payloadOneof)
	if fieldDesc == nil {
		err = errors.New("event has no payload")
		return
	}
	payload := eventReflect.Get(fieldDesc).Message().Interface()
	object, ok := payload.(objectIface)
	if !ok {
		err = fmt.Errorf("payload type '%T' does not support identity fields", payload)
		return
	}
	objectID = object.GetId()
	if objectID == "" {
		err = errors.New("event payload has empty object identifier")
		return
	}
	metadata := object.GetMetadata()
	if metadata == nil || metadata.GetTenant() == "" {
		err = errors.New("event payload has empty tenant")
		return
	}
	objectTenant = metadata.GetTenant()
	return
}

func (p *EventPublisher) registerUnpublishedCountMetric(registerer prometheus.Registerer) error {
	const name = "event_unpublished_count"
	metric := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: name,
		Help: "Number of events waiting to be published.",
	})
	err := registerer.Register(metric)
	if err != nil {
		var registered prometheus.AlreadyRegisteredError
		if !errors.As(err, &registered) {
			return err
		}
		existing, ok := registered.ExistingCollector.(prometheus.Gauge)
		if !ok {
			return fmt.Errorf(
				"metric '%s' can't be registered as a gauge because it is already registered as a '%T'",
				name, registered.ExistingCollector,
			)
		}
		p.metrics.unpublishedCount = existing
		return nil
	}
	p.metrics.unpublishedCount = metric
	return nil
}

func (p *EventPublisher) registerPublishLatencyMetric(registerer prometheus.Registerer) error {
	const name = "event_publish_latency_seconds"
	metric := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    name,
		Help:    "Time from change row creation to successful Kafka publish.",
		Buckets: prometheus.DefBuckets,
	})
	err := registerer.Register(metric)
	if err != nil {
		var registered prometheus.AlreadyRegisteredError
		if !errors.As(err, &registered) {
			return err
		}
		existing, ok := registered.ExistingCollector.(prometheus.Histogram)
		if !ok {
			return fmt.Errorf(
				"metric '%s' can't be registered as a histogram because it is already registered as a '%T'",
				name, registered.ExistingCollector,
			)
		}
		p.metrics.publishLatency = existing
		return nil
	}
	p.metrics.publishLatency = metric
	return nil
}

func (p *EventPublisher) registerPublishTotalMetric(registerer prometheus.Registerer) error {
	const name = "event_publish_total"
	metric := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: name,
		Help: "Number of events published to Kafka.",
	}, nil)
	err := registerer.Register(metric)
	if err != nil {
		var registered prometheus.AlreadyRegisteredError
		if !errors.As(err, &registered) {
			return err
		}
		existing, ok := registered.ExistingCollector.(*prometheus.CounterVec)
		if !ok {
			return fmt.Errorf(
				"metric '%s' can't be registered as a counter vector because it is already registered as a '%T'",
				name, registered.ExistingCollector,
			)
		}
		p.metrics.publishTotal = existing
		return nil
	}
	p.metrics.publishTotal = metric
	return nil
}

func (p *EventPublisher) registerPublishErrorsMetric(registerer prometheus.Registerer) error {
	const name = "event_publish_errors_total"
	metric := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: name,
		Help: "Number of errors publishing events, including rows that cannot be encoded or routed.",
	}, nil)
	err := registerer.Register(metric)
	if err != nil {
		var registered prometheus.AlreadyRegisteredError
		if !errors.As(err, &registered) {
			return err
		}
		existing, ok := registered.ExistingCollector.(*prometheus.CounterVec)
		if !ok {
			return fmt.Errorf(
				"metric '%s' can't be registered as a counter vector because it is already registered as a '%T'",
				name, registered.ExistingCollector,
			)
		}
		p.metrics.publishErrors = existing
		return nil
	}
	p.metrics.publishErrors = metric
	return nil
}

const (
	// DefaultEventTopicPrefix is prepended to the tenant identifier to form Kafka topic names.
	DefaultEventTopicPrefix = "osac.events."

	// defaultEventPublisherTable is the PostgreSQL table that stores change rows.
	defaultEventPublisherTable = "changes"

	// defaultEventPublisherChannel is the PostgreSQL LISTEN/NOTIFY channel that signals available change rows.
	defaultEventPublisherChannel = "changes"

	defaultListenWaitTimeout   = 5 * time.Second
	defaultListenRetryInterval = 5 * time.Second
	defaultBatchSize           = 100
	defaultMetricsInterval     = 15 * time.Second
)

// Possible values of the 'op' column of the changes table.
const (
	eventPublisherOpInsert = "INSERT"
	eventPublisherOpUpdate = "UPDATE"
	eventPublisherOpDelete = "DELETE"
	eventPublisherOpSignal = "SIGNAL"
)
