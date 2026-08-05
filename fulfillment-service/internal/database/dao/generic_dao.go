/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package dao

import (
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/gobuffalo/flect"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/json"
)

// Object is the interface that should be satisfied by objects to be managed by the generic DAO.
type Object interface {
	proto.Message
	GetId() string
	SetId(string)
}

// GenericDAOBuilder is a builder for creating generic data access objects.
type GenericDAOBuilder[O Object] struct {
	logger            *slog.Logger
	table             string
	defaultLimit      int32
	maxLimit          int32
	eventCallbacks    []EventCallback
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

// GenericDAO provides generic data access operations for protocol buffers messages. It assumes that objects will be
// stored in tables with the following columns:
//
//   - `id` - The unique identifier of the object.
//   - `name` - The human friendly name of the object.
//   - `creation_timestamp` - The time the object was created.
//   - `deletion_timestamp` - The time the object was deleted.
//   - `finalizers` - The list of finalizers for the object.
//   - `creator` - The creator of the object.
//   - `tenant` - The tenant that the object belongs to.
//   - `project` - The project that the object is assigned to.
//   - `labels` - The labels assigned to the object.
//   - `annotations` - The annotations assigned to the object.
//   - `version` - The version number, automatically incremented on every update.
//   - `data` - The serialized object, using the protocol buffers JSON serialization.
//
// Objects must have field named `id` of string type.
type GenericDAO[O Object] struct {
	// Basic fields:
	logger           *slog.Logger
	table            string
	defaultLimit     int32
	maxLimit         int32
	timestampDesc    protoreflect.MessageDescriptor
	eventCallbacks   []EventCallback
	objectTemplate   protoreflect.Message
	metadataField    protoreflect.FieldDescriptor
	metadataTemplate protoreflect.Message
	jsonEncoder      *json.Encoder
	marshalOptions   protojson.MarshalOptions
	unmarshalOptions protojson.UnmarshalOptions
	filterTranslator *FilterTranslator[O]
	tenancyLogic     auth.TenancyLogic

	// Metrics:
	opDurationMetric *prometheus.HistogramVec
}

type metadataIface interface {
	proto.Message
	GetName() string
	SetName(string)
	GetCreationTimestamp() *timestamppb.Timestamp
	SetCreationTimestamp(*timestamppb.Timestamp)
	GetDeletionTimestamp() *timestamppb.Timestamp
	SetDeletionTimestamp(*timestamppb.Timestamp)
	GetFinalizers() []string
	SetFinalizers([]string)
	GetCreator() string
	SetCreator(string)
	GetTenant() string
	SetTenant(string)
	GetProject() string
	SetProject(string)
	GetLabels() map[string]string
	SetLabels(map[string]string)
	GetAnnotations() map[string]string
	SetAnnotations(map[string]string)
	GetVersion() int32
	SetVersion(int32)
}

// NewGenericDAO creates a builder that can then be used to configure and create a generic DAO.
func NewGenericDAO[O Object]() *GenericDAOBuilder[O] {
	return &GenericDAOBuilder[O]{
		defaultLimit: 100,
		maxLimit:     1000,
	}
}

// SetLogger sets the logger. This is mandatory.
func (b *GenericDAOBuilder[O]) SetLogger(value *slog.Logger) *GenericDAOBuilder[O] {
	b.logger = value
	return b
}

// SetDefaultLimit sets the default number of items returned. It will be used when the value of the limit parameter
// of the list request is zero. This is optional, and the default is 100.
func (b *GenericDAOBuilder[O]) SetDefaultLimit(value int) *GenericDAOBuilder[O] {
	b.defaultLimit = int32(value) // #nosec G115 -- small pagination constant
	return b
}

// SetMaxLimit sets the maximum number of items returned. This is optional and the default value is 1000.
func (b *GenericDAOBuilder[O]) SetMaxLimit(value int) *GenericDAOBuilder[O] {
	b.maxLimit = int32(value) // #nosec G115 -- small pagination constant
	return b
}

// AddEventCallback adds a function that will be called to process events when the DAO creates, updates or deletes
// an object.
//
// The functions are called synchronously, in the same order they were added, and with the same context used by the
// DAO for its operations. If any of them returns an error the transaction will be rolled back.
func (b *GenericDAOBuilder[O]) AddEventCallback(value EventCallback) *GenericDAOBuilder[O] {
	b.eventCallbacks = append(b.eventCallbacks, value)
	return b
}

// SetTenancyLogic sets the tenancy logic. This is mandatory.
func (b *GenericDAOBuilder[O]) SetTenancyLogic(value auth.TenancyLogic) *GenericDAOBuilder[O] {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics.
//
// When enabled the following metrics will be registered:
//
//	db_operation_duration_sum - Total time executing database operations, in seconds.
//	db_operation_duration_count - Total number of database operations measured.
//	db_operation_duration_bucket - Database operation durations organized in buckets.
//
// The metrics will have the following labels:
//
//	error - The PostgreSQL error code, for example `23505` for unique violations. It will be empty
//	  when the operation succeeds.
//	table - Name of the database table, for example `clusters` or `hosts`.
//	type - Name of the DAO operation, for example `create`, `get`, `list`, `update`, `delete`,
//	  `exists`, `lock`, `count` or `archive`.
//
// To calculate the average duration for create operations on the clusters table during the last 10 minutes,
// for example, use a Prometheus expression like this:
//
//	rate(db_operation_duration_sum{table="clusters",type="create"}[10m]) /
//	  rate(db_operation_duration_count{table="clusters",type="create"}[10m])
//
// This is optional. If not set, no metrics will be recorded.
func (b *GenericDAOBuilder[O]) SetMetricsRegisterer(value prometheus.Registerer) *GenericDAOBuilder[O] {
	b.metricsRegisterer = value
	return b
}

// SetTableName overrides the database table name. By default the table name is derived from the
// protobuf message type name (e.g. Cluster becomes clusters). Use this when the table name does not
// match the type name.
func (b *GenericDAOBuilder[O]) SetTableName(value string) *GenericDAOBuilder[O] {
	b.table = value
	return b
}

// Build creates a new generic DAO using the configuration stored in the builder.
func (b *GenericDAOBuilder[O]) Build() (result *GenericDAO[O], err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.defaultLimit <= 0 {
		err = fmt.Errorf("default limit must be a possitive integer, but it is %d", b.defaultLimit)
		return
	}
	if b.maxLimit <= 0 {
		err = fmt.Errorf("max limit must be a possitive integer, but it is %d", b.maxLimit)
		return
	}
	if b.maxLimit < b.defaultLimit {
		err = fmt.Errorf(
			"max limit must be greater or equal to default limit, but max limit is %d and default limit "+
				"is %d",
			b.maxLimit, b.defaultLimit,
		)
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	// Get descriptors of well known types:
	var timestamp *timestamppb.Timestamp
	timestampDesc := timestamp.ProtoReflect().Descriptor()

	// Create the template that we will clone when we need to create a new object:
	var object O
	objectTemplate := object.ProtoReflect()

	// Get the field descriptors:
	objectDesc := objectTemplate.Descriptor()
	objectFields := objectDesc.Fields()
	idField := objectFields.ByName(idFieldName)
	if idField == nil {
		err = fmt.Errorf(
			"object of type '%s' doesn't have a '%s' field",
			objectDesc.FullName(), idFieldName,
		)
		return
	}
	metadataField := objectFields.ByName(metadataFieldName)
	if metadataField == nil {
		err = fmt.Errorf(
			"object of type '%s' doesn't have a '%s' field",
			objectDesc.FullName(), metadataFieldName,
		)
		return
	}

	// Create the template that we will clone when we need to create a new metadata object:
	metadataTemplate := objectTemplate.NewField(metadataField).Message()

	// Create the JSON encoder. We need this special encoder in order to ignore the 'id' and 'metadata' fields
	// because we save those in separate database columns and not in the JSON document where we save everything
	// else.
	jsonEncoder, err := json.NewEncoder().
		SetLogger(b.logger).
		AddIgnoredFields(
			idField.FullName(),
			metadataField.FullName(),
		).
		Build()
	if err != nil {
		err = fmt.Errorf("failed to create JSON encoder: %w", err)
		return
	}

	// Prepare the JSON marshalling options:
	marshalOptions := protojson.MarshalOptions{
		UseProtoNames: true,
	}
	unmarshalOptions := protojson.UnmarshalOptions{
		DiscardUnknown: true,
	}

	// Create the filter translator:
	filterTranslator, err := NewFilterTranslator[O]().
		SetLogger(b.logger).
		Build()
	if err != nil {
		err = fmt.Errorf("failed to create filter translator: %w", err)
		return
	}

	// Register metrics:
	var opDurationMetric *prometheus.HistogramVec
	if b.metricsRegisterer != nil {
		opDurationMetric, err = b.registerOpDurationMetric()
		if err != nil {
			return
		}
	}

	// Create and populate the object:
	result = &GenericDAO[O]{
		logger:           b.logger,
		table:            b.resolveTableName(),
		defaultLimit:     b.defaultLimit,
		maxLimit:         b.maxLimit,
		timestampDesc:    timestampDesc,
		eventCallbacks:   slices.Clone(b.eventCallbacks),
		objectTemplate:   objectTemplate,
		metadataField:    metadataField,
		metadataTemplate: metadataTemplate,
		jsonEncoder:      jsonEncoder,
		marshalOptions:   marshalOptions,
		unmarshalOptions: unmarshalOptions,
		filterTranslator: filterTranslator,
		tenancyLogic:     b.tenancyLogic,
		opDurationMetric: opDurationMetric,
	}
	return
}

// resolveTableName returns the explicit table name if one was set via SetTableName, otherwise it
// derives the name from the protobuf message type.
func (b *GenericDAOBuilder[O]) resolveTableName() string {
	if b.table != "" {
		return b.table
	}
	return b.tableName()
}

// tableName calculates the table name from the protobuf message type name. It converts the CamelCase type
// name to snake_case and pluralizes it. For example, `Cluster` becomes `clusters` and `ComputeInstance` becomes
// `compute_instances`.
func (b *GenericDAOBuilder[O]) tableName() string {
	var object O
	return flect.Pluralize(flect.Underscore(string(object.ProtoReflect().Descriptor().Name())))
}

func (b *GenericDAOBuilder[O]) registerOpDurationMetric() (result *prometheus.HistogramVec, err error) {
	registerer := b.metricsRegisterer
	if registerer == nil {
		registerer = prometheus.DefaultRegisterer
	}
	const name = "operation_duration"
	metric := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Subsystem: "db",
			Name:      name,
			Help:      "Duration of database operations in seconds.",
			Buckets: []float64{
				0.001,
				0.005,
				0.01,
				0.025,
				0.05,
				0.1,
				0.25,
				0.5,
				1.0,
				2.5,
				5.0,
			},
		},
		[]string{
			errorMetricLabel,
			tableMetricLabel,
			typeMetricLabel,
		},
	)
	err = registerer.Register(metric)
	if err != nil {
		var alreadyRegisteredErr prometheus.AlreadyRegisteredError
		if !errors.As(err, &alreadyRegisteredErr) {
			return
		}
		registered, ok := alreadyRegisteredErr.ExistingCollector.(*prometheus.HistogramVec)
		if !ok {
			err = fmt.Errorf(
				"metric '%s' can't be registered as an histogram vector because it is already registered as a '%T'",
				name, alreadyRegisteredErr.ExistingCollector,
			)
			return
		}
		result = registered
		err = nil
		return
	}
	result = metric
	return
}

// Names of well known fields:
var (
	idFieldName       = protoreflect.Name("id")
	metadataFieldName = protoreflect.Name("metadata")
)

// opType is used to describe the types of operations performed by the DAO in logs and metrics.
type opType string

// Operation types used as metric labels:
const (
	archiveOpType opType = "archive"
	countOpType   opType = "count"
	createOpType  opType = "create"
	deleteOpType  opType = "delete"
	existsOpType  opType = "exists"
	getOpType     opType = "get"
	listOpType    opType = "list"
	lockOpType    opType = "lock"
	updateOpType  opType = "update"
)

// Label names and values for metrics:
const (
	errorMetricLabel = "error"
	tableMetricLabel = "table"
	typeMetricLabel  = "type"
)
