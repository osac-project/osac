/*
Copyright (c) 2025 Red Hat Inc.

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
	"log/slog"
	"reflect"
	"strings"
	"sync"

	"buf.build/go/protovalidate"
	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcmetadata "google.golang.org/grpc/metadata"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/database/dao"
	"github.com/osac-project/fulfillment-service/internal/events"
	"github.com/osac-project/fulfillment-service/internal/masks"
	"github.com/osac-project/fulfillment-service/internal/util"
	"github.com/osac-project/fulfillment-service/internal/uuid"
)

// GenericServerBuilder contains the data and logic needed to create new generic servers.
type GenericServerBuilder[O dao.Object] struct {
	logger            *slog.Logger
	service           string
	table             string
	ignoredFields     []any
	notifier          events.Notifier
	redactFunc        func(O) O
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
}

// GenericServer is a gRPC server that knows how to implement the List, Get, Create, Update and Delete operators for
// any object that has identifier and metadata fields.
type GenericServer[O dao.Object] struct {
	logger           *slog.Logger
	service          string
	dao              *dao.GenericDAO[O]
	attributionLogic auth.AttributionLogic
	tenancyLogic     auth.TenancyLogic
	template         proto.Message
	metadataField    protoreflect.FieldDescriptor
	listRequest      proto.Message
	listResponse     proto.Message
	getRequest       proto.Message
	getResponse      proto.Message
	createRequest    proto.Message
	createResponse   proto.Message
	updateRequest    proto.Message
	updateResponse   proto.Message
	deleteRequest    proto.Message
	deleteResponse   proto.Message
	signalRequest    proto.Message
	signalResponse   proto.Message
	notifier         events.Notifier
	redactFunc       func(O) O
	payloadField     protoreflect.FieldDescriptor
	pathCompiler     *masks.PathCompiler[O]
	pathCache        map[string]*masks.Path[O]
	pathCacheLock    *sync.Mutex
	validator        protovalidate.Validator
}

type metadataIface interface {
	proto.Message
	GetName() string
	GetLabels() map[string]string
	GetAnnotations() map[string]string
	GetCreator() string
	SetCreator(string)
	GetTenant() string
	SetTenant(string)
	GetProject() string
	SetProject(string)
	GetVersion() int32
}

// NewGenericServer creates a builder that can then be used to configure and create a new generic server.
func NewGenericServer[O dao.Object]() *GenericServerBuilder[O] {
	return &GenericServerBuilder[O]{}
}

// SetLogger sets the logger. This is mandatory.
func (b *GenericServerBuilder[O]) SetLogger(value *slog.Logger) *GenericServerBuilder[O] {
	b.logger = value
	return b
}

// SetService sets the service description. This is mandatory.
func (b *GenericServerBuilder[O]) SetService(value string) *GenericServerBuilder[O] {
	b.service = value
	return b
}

// SetTableName overrides the database table name. By default the table name is derived from the
// protobuf message type name. Use this when the table name does not match the type name.
func (b *GenericServerBuilder[O]) SetTableName(value string) *GenericServerBuilder[O] {
	b.table = value
	return b
}

// AddIgnoredFields adds a set of fields to be omitted when mapping objects. The values passed can be of the following
// types:
//
// string - This should be a field name, for example 'status' and then any field with that name in any object will
// be ignored.
//
// protoreflect.Name - Like string.
//
// protoreflect.FullName - This indicates a field of a particular type. For example, if the value is
// 'osac.public.v1.Cluster.status' then the field 'status' of the 'osac.public.v1.Cluster' type will be ignored, but
// the 'status' field of other types will not be ignored.
func (b *GenericServerBuilder[O]) AddIgnoredFields(values ...any) *GenericServerBuilder[O] {
	b.ignoredFields = append(b.ignoredFields, values...)
	return b
}

// SetNotifier sets the notifier that the server will use to send change notifications. This is optional.
func (b *GenericServerBuilder[O]) SetNotifier(value events.Notifier) *GenericServerBuilder[O] {
	b.notifier = util.NormalizeNil(value)
	return b
}

// SetRedactFunc sets a function that will be called to redact sensitive fields from objects before they are included in
// event notification payloads. The function receives a clone of the object and should return it with the sensitive
// fields cleared. This is optional.
func (b *GenericServerBuilder[O]) SetRedactFunc(value func(O) O) *GenericServerBuilder[O] {
	b.redactFunc = value
	return b
}

// SetAttributionLogic sets the logic that will be used to determine the creator for objects.
func (b *GenericServerBuilder[O]) SetAttributionLogic(value auth.AttributionLogic) *GenericServerBuilder[O] {
	b.attributionLogic = value
	return b
}

// SetTenancyLogic sets the tenancy logic that will be used to determine the tenants for objects. The logic receives the
// context as a parameter and should return the names of the tenants. If not provided, no tenants will be set.
func (b *GenericServerBuilder[O]) SetTenancyLogic(value auth.TenancyLogic) *GenericServerBuilder[O] {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics. This is optional. If not set, no
// metrics will be recorded.
func (b *GenericServerBuilder[O]) SetMetricsRegisterer(value prometheus.Registerer) *GenericServerBuilder[O] {
	b.metricsRegisterer = value
	return b
}

// Build uses the configuration stored in the builder to create and configure a new generic server.
func (b *GenericServerBuilder[O]) Build() (result *GenericServer[O], err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.service == "" {
		err = errors.New("service name is mandatory")
		return
	}
	if b.attributionLogic == nil {
		err = errors.New("attribution logic is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	// Create the path compiler:
	pathCompiler, err := masks.NewPathCompiler[O]().
		SetLogger(b.logger).
		Build()
	if err != nil {
		err = fmt.Errorf("failed to create path compiler: %w", err)
		return
	}

	// Create the protovalidate validator:
	validator, err := protovalidate.New()
	if err != nil {
		err = fmt.Errorf("failed to create protovalidate validator: %w", err)
		return
	}

	// Create the object early so that we can use its methods as callbacks:
	s := &GenericServer[O]{
		logger:           b.logger,
		service:          b.service,
		attributionLogic: b.attributionLogic,
		tenancyLogic:     b.tenancyLogic,
		notifier:         b.notifier,
		pathCompiler:     pathCompiler,
		pathCache:        map[string]*masks.Path[O]{},
		pathCacheLock:    &sync.Mutex{},
		validator:        validator,
	}

	// Set the redact function:
	s.redactFunc = b.redactFunc

	// Create the DAO:
	daoBuilder := dao.NewGenericDAO[O]()
	daoBuilder.SetLogger(b.logger)
	if b.table != "" {
		daoBuilder.SetTableName(b.table)
	}
	daoBuilder.SetTenancyLogic(b.tenancyLogic)
	if b.notifier != nil {
		daoBuilder.AddEventCallback(s.notifyEvent)
	}
	if b.metricsRegisterer != nil {
		daoBuilder.SetMetricsRegisterer(b.metricsRegisterer)
	}
	s.dao, err = daoBuilder.Build()
	if err != nil {
		err = fmt.Errorf("failed to create DAO: %w", err)
		return
	}

	// Find the descriptor:
	service, err := b.findService()
	if err != nil {
		return
	}

	// Prepare the template for the object:
	var object O
	reflect := object.ProtoReflect()
	s.template = reflect.New().Interface()

	// Find the metadata field:
	descriptor := reflect.Descriptor()
	fields := descriptor.Fields()
	s.metadataField = fields.ByName("metadata")
	if s.metadataField == nil {
		err = fmt.Errorf("object of type '%s' doesn't have a 'metadata' field", descriptor.FullName())
		return
	}

	// Prepare templates for the request and response types. These are empty messages that will be cloned when
	// it is necessary to create new instances.
	s.listRequest, s.listResponse, err = b.findRequestAndResponse(service, listMethod)
	if err != nil {
		return
	}
	s.getRequest, s.getResponse, err = b.findRequestAndResponse(service, getMethod)
	if err != nil {
		return
	}
	s.createRequest, s.createResponse, err = b.findRequestAndResponse(service, createMethod)
	if err != nil {
		return
	}
	s.deleteRequest, s.deleteResponse, err = b.findRequestAndResponse(service, deleteMethod)
	if err != nil {
		return
	}
	s.updateRequest, s.updateResponse, err = b.findRequestAndResponse(service, updateMethod)
	if err != nil {
		return
	}
	s.signalRequest, s.signalResponse, err = b.findRequestAndResponse(service, signalMethod)
	if err != nil {
		return
	}

	// Find the payload field in the event message:
	s.payloadField, err = b.findPayloadField()
	if err != nil {
		return
	}

	result = s
	return
}

// findService finds the service descriptor using the service name given to the builder.
func (b *GenericServerBuilder[O]) findService() (result protoreflect.ServiceDescriptor, err error) {
	packageFullName := (privatev1.EventType)(0).Descriptor().FullName().Parent()
	protoregistry.GlobalFiles.RangeFilesByPackage(packageFullName, func(desc protoreflect.FileDescriptor) bool {
		for i := range desc.Services().Len() {
			serviceDesc := desc.Services().Get(i)
			if string(serviceDesc.FullName()) == b.service {
				result = serviceDesc
				return false
			}
		}
		return true
	})
	if result == nil {
		err = fmt.Errorf("failed to find service '%s'", b.service)
		return
	}
	return
}

// findRequestAndResponse finds the request and response message types for the given method.
func (b *GenericServerBuilder[O]) findRequestAndResponse(service protoreflect.ServiceDescriptor,
	methodName string) (request proto.Message, response proto.Message, err error) {
	for i := range service.Methods().Len() {
		method := service.Methods().Get(i)
		if string(method.Name()) == methodName {
			requestType, err := protoregistry.GlobalTypes.FindMessageByName(method.Input().FullName())
			if err != nil {
				return nil, nil, fmt.Errorf(
					"failed to find request message type '%s': %w",
					method.Input().FullName(), err,
				)
			}
			responseType, err := protoregistry.GlobalTypes.FindMessageByName(method.Output().FullName())
			if err != nil {
				return nil, nil, fmt.Errorf(
					"failed to find response message type '%s': %w",
					method.Output().FullName(), err,
				)
			}
			request = requestType.New().Interface()
			response = responseType.New().Interface()
			return request, response, nil
		}
	}
	err = fmt.Errorf("failed to find method '%s' in service '%s'", methodName, service.FullName())
	return
}

// findPayloadField finds the field in the event message that corresponds to this object type. This is used later to
// set the payload of event notifications without having to iterate the oneof fields every time. Returns nil if there
// is no such field.
func (b *GenericServerBuilder[O]) findPayloadField() (result protoreflect.FieldDescriptor, err error) {
	var objectTempl O
	objectDesc := objectTempl.ProtoReflect().Descriptor()
	var eventTempl *privatev1.Event
	eventDesc := eventTempl.ProtoReflect().Descriptor()
	oneofDesc := eventDesc.Oneofs().ByName(eventPayloadField)
	if oneofDesc == nil {
		err = fmt.Errorf("failed to find the 'payload' field of the event type '%s'", eventDesc.FullName())
		return
	}
	oneofFields := oneofDesc.Fields()
	for i := range oneofFields.Len() {
		payloadField := oneofFields.Get(i)
		if payloadField.Message() != nil && payloadField.Message() == objectDesc {
			result = payloadField
			break
		}
	}
	return
}

func (s *GenericServer[O]) List(ctx context.Context, request any, response any) error {
	// Extract the request message:
	type requestIface interface {
		GetOffset() int32
		GetLimit() int32
		GetFilter() string
	}
	requestMsg := request.(requestIface)

	// List the objects:
	daoResponse, err := s.dao.List().
		SetFilter(requestMsg.GetFilter()).
		SetOffset(requestMsg.GetOffset()).
		SetLimit(requestMsg.GetLimit()).
		Do(ctx)
	if err != nil {
		var deniedErr *dao.ErrDenied
		if errors.As(err, &deniedErr) {
			return grpcstatus.Errorf(grpccodes.PermissionDenied, "%s", deniedErr.Reason)
		}
		var deadlockErr *dao.ErrDeadlock
		if errors.As(err, &deadlockErr) {
			return grpcstatus.Errorf(grpccodes.Aborted, "%s", deadlockErr.Error())
		}
		s.logger.ErrorContext(
			ctx,
			"Failed to list",
			slog.Any("error", err),
		)
		return grpcstatus.Errorf(grpccodes.Internal, "failed to list")
	}

	// Create the response message:
	type responseIface interface {
		SetSize(int32)
		SetTotal(int32)
		SetItems([]O)
	}
	responseMsg := proto.Clone(s.listResponse).(responseIface)
	responseMsg.SetSize(daoResponse.GetSize())
	responseMsg.SetTotal(daoResponse.GetTotal())
	responseMsg.SetItems(daoResponse.GetItems())
	s.setPointer(response, responseMsg)

	return nil
}

func (s *GenericServer[O]) Get(ctx context.Context, request any, response any) error {
	// Extract the object identifier from the request:
	type requestIface interface {
		GetId() string
	}
	requestMsg := request.(requestIface)
	requestId := requestMsg.GetId()
	if requestId == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "identifier is mandatory")
	}

	// Fetch the object:
	daoResponse, err := s.dao.Get().
		SetId(requestId).
		Do(ctx)
	if err != nil {
		var notFoundErr *dao.ErrNotFound
		if errors.As(err, &notFoundErr) {
			return grpcstatus.Errorf(grpccodes.NotFound, "object with identifier '%s' not found", requestId)
		}
		var deniedErr *dao.ErrDenied
		if errors.As(err, &deniedErr) {
			return grpcstatus.Errorf(grpccodes.PermissionDenied, "%s", deniedErr.Reason)
		}
		var deadlockErr *dao.ErrDeadlock
		if errors.As(err, &deadlockErr) {
			return grpcstatus.Errorf(grpccodes.Aborted, "%s", deadlockErr.Error())
		}
		s.logger.ErrorContext(
			ctx,
			"Failed to get",
			slog.String("id", requestId),
			slog.Any("error", err),
		)
		return grpcstatus.Errorf(grpccodes.Internal, "failed to get object with identifier '%s'", requestId)
	}
	object := daoResponse.GetObject()

	// Create the response message:
	type responseIface interface {
		SetObject(O)
	}
	responseMsg := proto.Clone(s.getResponse).(responseIface)
	responseMsg.SetObject(object)
	s.setPointer(response, responseMsg)

	return nil
}

func (s *GenericServer[O]) Create(ctx context.Context, request any, response any) error {
	// Route dry-run requests to skip persistence and event emission. Resource-specific
	// validation (template resolution, catalog item field definitions, spec defaults)
	// runs in the calling server before reaching GenericServer. The dry-run flag is
	// carried as gRPC metadata (HTTP header X-Dry-Run: true) rather than a proto field
	// to keep request messages purely declarative.
	if isDryRun(ctx) {
		return s.createDryRun(ctx, request, response)
	}

	requestObject, err := s.prepareForCreate(ctx, request)
	if err != nil {
		return err
	}

	// Save the object:
	daoResponse, err := s.dao.Create().
		SetObject(requestObject).
		Do(ctx)
	if err != nil {
		var alreadyExistsErr *dao.ErrAlreadyExists
		if errors.As(err, &alreadyExistsErr) {
			return grpcstatus.Errorf(grpccodes.AlreadyExists, "%s", alreadyExistsErr.Error())
		}
		var notUniqueErr *dao.ErrNotUnique
		if errors.As(err, &notUniqueErr) {
			return grpcstatus.Errorf(grpccodes.AlreadyExists, "%s", notUniqueErr.Error())
		}
		var deniedErr *dao.ErrDenied
		if errors.As(err, &deniedErr) {
			return grpcstatus.Errorf(grpccodes.PermissionDenied, "%s", deniedErr.Error())
		}
		var referenceErr *dao.ErrReference
		if errors.As(err, &referenceErr) {
			return grpcstatus.Errorf(grpccodes.InvalidArgument, "%s", referenceErr.Error())
		}
		var deadlockErr *dao.ErrDeadlock
		if errors.As(err, &deadlockErr) {
			return grpcstatus.Errorf(grpccodes.Aborted, "%s", deadlockErr.Error())
		}
		s.logger.ErrorContext(
			ctx,
			"Failed to create",
			slog.Any("error", err),
		)
		return grpcstatus.Errorf(grpccodes.Internal, "failed to create object")
	}
	responseObject := daoResponse.GetObject()

	// Create the response message:
	type responseIface interface {
		SetObject(O)
	}
	responseMsg := proto.Clone(s.createResponse).(responseIface)
	responseMsg.SetObject(responseObject)
	s.setPointer(response, responseMsg)

	return nil
}

func (s *GenericServer[O]) prepareForCreate(ctx context.Context, request any) (O, error) {
	var nilObject O

	type requestIface interface {
		GetObject() O
	}
	requestMsg := request.(requestIface)
	requestObject := requestMsg.GetObject()
	if s.isNil(requestObject) {
		requestObject = proto.Clone(s.template).(O)
	} else {
		requestMetadata := s.getMetadata(requestObject)
		if requestMetadata != nil {
			if err := s.validateMetadata(ctx, requestMetadata); err != nil {
				return nilObject, err
			}
		}
	}

	assignedCreator, err := s.determineAssignedCreator(ctx)
	if err != nil {
		return nilObject, err
	}
	if err = s.setCreator(ctx, requestObject, assignedCreator); err != nil {
		return nilObject, err
	}

	assignedTenant, err := s.determineAssignedTenant(ctx, requestObject, requestObject)
	if err != nil {
		return nilObject, err
	}
	if err = s.setTenant(ctx, requestObject, assignedTenant); err != nil {
		return nilObject, err
	}

	return requestObject, nil
}

func (s *GenericServer[O]) createDryRun(ctx context.Context, request any, response any) error {
	requestObject, err := s.prepareForCreate(ctx, request)
	if err != nil {
		return err
	}

	type responseIface interface {
		SetObject(O)
	}
	responseMsg := proto.Clone(s.createResponse).(responseIface)
	responseMsg.SetObject(requestObject)
	s.setPointer(response, responseMsg)

	return nil
}

// DryRunHTTPHeader is the HTTP header name REST clients use to request dry-run mode.
// The REST gateway forwards it as gRPC metadata with key DryRunMetadataKey.
const DryRunHTTPHeader = "X-Dry-Run"

const DryRunMetadataKey = "x-dry-run"

func isDryRun(ctx context.Context) bool {
	md, ok := grpcmetadata.FromIncomingContext(ctx)
	if !ok {
		return false
	}
	values := md.Get(DryRunMetadataKey)
	for _, v := range values {
		if strings.EqualFold(v, "true") {
			return true
		}
	}
	return false
}

func (s *GenericServer[O]) Update(ctx context.Context, request any, response any) error {
	// Extract the object from the request message:
	type requestIface interface {
		GetObject() O
		GetUpdateMask() *fieldmaskpb.FieldMask
		GetLock() bool
	}
	requestMsg := request.(requestIface)
	requestObject := requestMsg.GetObject()
	if s.isNil(requestObject) {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
	}
	requestId := requestObject.GetId()
	if requestId == "" {
		return grpcstatus.Errorf(grpccodes.Internal, "object identifier is mandatory")
	}

	// Fetch the current representation of the object:
	getResponse, err := s.dao.Get().
		SetId(requestId).
		SetLock(true).
		Do(ctx)
	if err != nil {
		var notFoundErr *dao.ErrNotFound
		if errors.As(err, &notFoundErr) {
			return grpcstatus.Errorf(
				grpccodes.NotFound,
				"object with identifier '%s' not found",
				requestId,
			)
		}
		var deniedErr *dao.ErrDenied
		if errors.As(err, &deniedErr) {
			return grpcstatus.Errorf(grpccodes.PermissionDenied, "%s", deniedErr.Reason)
		}
		var deadlockErr *dao.ErrDeadlock
		if errors.As(err, &deadlockErr) {
			return grpcstatus.Errorf(grpccodes.Aborted, "%s", deadlockErr.Error())
		}
		s.logger.ErrorContext(
			ctx,
			"Failed to get object",
			slog.String("id", requestId),
			slog.Any("error", err),
		)
		return grpcstatus.Errorf(
			grpccodes.Internal,
			"failed to get object with identifier '%s'",
			requestId,
		)
	}
	currentObject := getResponse.GetObject()
	if s.isNil(currentObject) {
		return grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"object with identifier '%s' doesn't exist",
			requestId,
		)
	}

	// If optimistic locking is enabled then compare the version provided by the caller with the current
	// version before applying any changes:
	if requestMsg.GetLock() {
		requestMetadata := s.getMetadata(requestObject)
		currentMetadata := s.getMetadata(currentObject)
		if requestMetadata != nil && currentMetadata != nil {
			if requestMetadata.GetVersion() != currentMetadata.GetVersion() {
				return grpcstatus.Errorf(
					grpccodes.Aborted,
					"object with identifier '%s' has been modified: requested version is %d "+
						"but current version is %d",
					requestId, requestMetadata.GetVersion(), currentMetadata.GetVersion(),
				)
			}
		}
	}

	// Clone the current object so that in-place modifications (mask application, tenant calculation) don't
	// affect the original that we use for the equivalence comparison later.
	tmpObject := proto.Clone(currentObject).(O)

	// Update the fields indicated in the update mask, or all the fields if there is no update mask:
	requestMask := requestMsg.GetUpdateMask()
	if requestMask != nil {
		fieldPaths, err := s.compilePaths(requestMask.GetPaths())
		if err != nil {
			return err
		}
		for _, fieldPath := range fieldPaths {
			value, ok := fieldPath.Get(requestObject)
			if ok {
				fieldPath.Set(tmpObject, value)
			} else {
				fieldPath.Clear(tmpObject)
			}
		}
	} else {
		tmpObject = requestObject
	}

	// Validate the merged object using protovalidate.
	// This ensures all validation constraints are checked after applying the update mask,
	// avoiding false positives from partial request objects.
	err = s.validator.Validate(tmpObject)
	if err != nil {
		s.logger.DebugContext(ctx, "Object validation failed after mask merge", "error", err.Error())
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "validation failed: %s", err.Error())
	}

	// Validate the resulting metadata:
	tmpMetadata := s.getMetadata(tmpObject)
	if tmpMetadata != nil {
		err = s.validateMetadata(ctx, tmpMetadata)
		if err != nil {
			return err
		}
	}

	// Calculate the tenant for the updated object:
	assignedTenant, err := s.determineAssignedTenant(ctx, tmpObject, currentObject)
	if err != nil {
		return err
	}
	err = s.setTenant(ctx, tmpObject, assignedTenant)
	if err != nil {
		return err
	}

	// Save the object only if there is any actual difference:
	var responseObject O
	if !s.equivalentObjects(tmpObject, currentObject) {
		updateResponse, err := s.dao.Update().
			SetObject(tmpObject).
			Do(ctx)
		if err != nil {
			var conflictErr *dao.ErrConflict
			if errors.As(err, &conflictErr) {
				return grpcstatus.Errorf(grpccodes.Aborted, "%s", conflictErr.Error())
			}
			var alreadyExistsErr *dao.ErrAlreadyExists
			if errors.As(err, &alreadyExistsErr) {
				return grpcstatus.Errorf(grpccodes.AlreadyExists, "%s", alreadyExistsErr.Error())
			}
			var referenceErr *dao.ErrReference
			if errors.As(err, &referenceErr) {
				return grpcstatus.Errorf(grpccodes.FailedPrecondition, "%s", referenceErr.Error())
			}
			var notUniqueErr *dao.ErrNotUnique
			if errors.As(err, &notUniqueErr) {
				return grpcstatus.Errorf(grpccodes.AlreadyExists, "%s", notUniqueErr.Error())
			}
			var deniedErr *dao.ErrDenied
			if errors.As(err, &deniedErr) {
				return grpcstatus.Errorf(grpccodes.PermissionDenied, "%s", deniedErr.Error())
			}
			var immutableErr *dao.ErrImmutable
			if errors.As(err, &immutableErr) {
				return grpcstatus.Errorf(grpccodes.InvalidArgument, "%s", immutableErr.Error())
			}
			var deadlockErr *dao.ErrDeadlock
			if errors.As(err, &deadlockErr) {
				return grpcstatus.Errorf(grpccodes.Aborted, "%s", deadlockErr.Error())
			}
			s.logger.ErrorContext(
				ctx,
				"Failed to update object",
				slog.String("id", requestId),
				slog.Any("error", err),
			)
			return grpcstatus.Errorf(
				grpccodes.Internal,
				"failed to update object with identifier '%s'",
				requestId,
			)
		}
		responseObject = updateResponse.GetObject()
	} else {
		responseObject = tmpObject
	}

	// Create the response message:
	type responseIface interface {
		SetObject(O)
	}
	responseMsg := proto.Clone(s.updateResponse).(responseIface)
	responseMsg.SetObject(responseObject)
	s.setPointer(response, responseMsg)

	return nil
}

func (s *GenericServer[O]) compilePaths(paths []string) (result []*masks.Path[O], err error) {
	fieldPaths := make([]*masks.Path[O], len(paths))
	for i, path := range paths {
		fieldPaths[i], err = s.compilePath(path)
		if err != nil {
			return
		}
	}
	result = fieldPaths
	return
}

func (s *GenericServer[O]) compilePath(path string) (result *masks.Path[O], err error) {
	s.pathCacheLock.Lock()
	defer s.pathCacheLock.Unlock()
	result, ok := s.pathCache[path]
	if ok {
		return
	}
	result, err = s.pathCompiler.Compile(path)
	if err != nil {
		return
	}
	s.pathCache[path] = result
	return
}

func (s *GenericServer[O]) Delete(ctx context.Context, request any, response any) error {
	// Extract object identifier from the request:
	type requestIface interface {
		GetId() string
	}
	requestMsg := request.(requestIface)
	requestId := requestMsg.GetId()
	if requestId == "" {
		return grpcstatus.Errorf(grpccodes.Internal, "object identifier is mandatory")
	}

	// Delete the object:
	_, err := s.dao.Delete().
		SetId(requestId).
		Do(ctx)
	if err != nil {
		_, ok := errors.AsType[*dao.ErrNotFound](err)
		if ok {
			return grpcstatus.Errorf(
				grpccodes.NotFound,
				"object with identifier '%s' not found",
				requestId,
			)
		}
		deniedErr, ok := errors.AsType[*dao.ErrDenied](err)
		if ok {
			return grpcstatus.Errorf(grpccodes.PermissionDenied, "%s", deniedErr.Error())
		}
		inUseErr, ok := errors.AsType[*dao.ErrInUse](err)
		if ok {
			return grpcstatus.Errorf(grpccodes.FailedPrecondition, "%s", inUseErr.Error())
		}
		if _, ok := errors.AsType[*dao.ErrDeadlock](err); ok {
			return grpcstatus.Errorf(grpccodes.Aborted, "concurrent modification detected, please retry")
		}
		s.logger.ErrorContext(
			ctx,
			"Failed to delete object",
			slog.String("id", requestId),
			slog.Any("error", err),
		)
		return grpcstatus.Errorf(
			grpccodes.Internal,
			"failed to delete object with identifier '%s'",
			requestId,
		)
	}

	// Create the response message:
	responseMsg := proto.Clone(s.deleteResponse)
	s.setPointer(response, responseMsg)

	return nil
}

func (s *GenericServer[O]) Signal(ctx context.Context, request any, response any) error {
	// Extract the object identifier from the request:
	type requestIface interface {
		GetId() string
	}
	requestMsg := request.(requestIface)
	requestId := requestMsg.GetId()
	if requestId == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "identifier is mandatory")
	}

	// Fetch the current representation of the object:
	daoResponse, err := s.dao.Get().
		SetId(requestId).
		Do(ctx)
	if err != nil {
		var notFoundErr *dao.ErrNotFound
		if errors.As(err, &notFoundErr) {
			return grpcstatus.Errorf(
				grpccodes.NotFound,
				"object with identifier '%s' not found",
				requestId,
			)
		}
		var deniedErr *dao.ErrDenied
		if errors.As(err, &deniedErr) {
			return grpcstatus.Errorf(grpccodes.PermissionDenied, "%s", deniedErr.Reason)
		}
		var deadlockErr *dao.ErrDeadlock
		if errors.As(err, &deadlockErr) {
			return grpcstatus.Errorf(grpccodes.Aborted, "%s", deadlockErr.Error())
		}
		s.logger.ErrorContext(
			ctx,
			"Failed to signal object",
			slog.String("id", requestId),
			slog.Any("error", err),
		)
		return grpcstatus.Errorf(
			grpccodes.Internal,
			"failed to signal object with identifier '%s'",
			requestId,
		)
	}
	object := daoResponse.GetObject()

	// Send the signal event:
	if s.notifier != nil {
		event := privatev1.Event_builder{
			Id:   uuid.New(),
			Type: privatev1.EventType_EVENT_TYPE_OBJECT_SIGNALED,
		}.Build()
		err = s.setPayload(event, object)
		if err != nil {
			return err
		}
		err = s.notifier.Notify(ctx, event)
		if err != nil {
			s.logger.ErrorContext(
				ctx,
				"Failed to send signal notification",
				slog.String("id", requestId),
				slog.Any("error", err),
			)
		}
	}

	// Create the response:
	responseMsg := proto.Clone(s.signalResponse)
	s.setPointer(response, responseMsg)

	return nil
}

// notifyEvent converts the DAO event into an API event and publishes it using the PostgreSQL NOTIFY command.
func (s *GenericServer[O]) notifyEvent(ctx context.Context, e dao.Event) error {
	event := &privatev1.Event{}
	event.SetId(uuid.New())
	switch e.Type {
	case dao.EventTypeCreated:
		event.SetType(privatev1.EventType_EVENT_TYPE_OBJECT_CREATED)
	case dao.EventTypeUpdated:
		event.SetType(privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED)
	case dao.EventTypeDeleted:
		event.SetType(privatev1.EventType_EVENT_TYPE_OBJECT_DELETED)
	default:
		return fmt.Errorf("unknown event kind '%s'", e.Type)
	}
	err := s.setPayload(event, e.Object)
	if err != nil {
		return err
	}
	return s.notifier.Notify(ctx, event)
}

// setPayload sets the payload of the event message. If the payload field is not found the event is left unchanged. If a
// redact function has been configured, the object is cloned and redacted before being set.
func (s *GenericServer[O]) setPayload(event *privatev1.Event, object proto.Message) error {
	if s.payloadField == nil {
		return nil
	}
	if s.redactFunc != nil {
		object = s.redactFunc(proto.Clone(object).(O))
	}
	event.ProtoReflect().Set(s.payloadField, protoreflect.ValueOfMessage(object.ProtoReflect()))
	return nil
}

func (s *GenericServer[O]) isNil(object proto.Message) bool {
	return reflect.ValueOf(object).IsNil()
}

func (s *GenericServer[O]) setPointer(pointer any, value any) {
	reflect.ValueOf(pointer).Elem().Set(reflect.ValueOf(value))
}

func (s *GenericServer[O]) validateMetadata(ctx context.Context, metadata metadataIface) error {
	// Note: Name validation is handled by protovalidate annotations and the validation interceptor.
	// No need to re-validate here.

	labels := metadata.GetLabels()
	if len(labels) > 0 {
		err := s.validateLabels(labels)
		if err != nil {
			return err
		}
	}
	annotations := metadata.GetAnnotations()
	if len(annotations) > 0 {
		err := s.validateAnnotations(annotations)
		if err != nil {
			return err
		}
	}
	return nil
}

// validateLabels validates label keys and values according to Kubernetes label naming conventions.
//
// This validation complements protovalidate annotations on the Metadata message:
// - Proto annotations enforce length constraints (keys: 1-316 chars, values: max 63 chars)
// - This Go code enforces complex DNS subdomain rules that cannot be expressed in simple regex:
//   - Prefix/name structure (optional "prefix/" followed by name)
//   - Each dot-separated segment must be a valid DNS label
//   - Character restrictions (alphanumeric, hyphens, underscores, dots)
//   - Alphanumeric start/end requirements
func (s *GenericServer[O]) validateLabels(labels map[string]string) error {
	for key, value := range labels {
		err := s.validateLabelKey("metadata.labels", key)
		if err != nil {
			return err
		}
		if value == "" {
			continue
		}
		err = s.validateLabelNameOrValue("metadata.labels", key, "value", value)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *GenericServer[O]) validateAnnotations(annotations map[string]string) error {
	for key := range annotations {
		err := s.validateLabelKey("metadata.annotations", key)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *GenericServer[O]) validateLabelKey(field string, key string) error {
	if key == "" {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "field '%s' has empty key", field)
	}
	parts := strings.Split(key, "/")
	if len(parts) > 2 {
		return grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"field '%s' key '%s' must contain at most one '/'",
			field, key,
		)
	}
	var prefix string
	var name string
	if len(parts) == 2 {
		prefix = parts[0]
		name = parts[1]
		if prefix == "" || name == "" {
			return grpcstatus.Errorf(
				grpccodes.InvalidArgument,
				"field '%s' key '%s' must have non-empty prefix and name",
				field, key,
			)
		}
	} else {
		name = parts[0]
	}
	if prefix != "" {
		err := s.validateLabelPrefix(field, key, prefix)
		if err != nil {
			return err
		}
	}
	return s.validateLabelNameOrValue(field, key, "name", name)
}

func (s *GenericServer[O]) validateLabelPrefix(field string, key string, prefix string) error {
	if len(prefix) < 1 || len(prefix) > 253 {
		return grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"field '%s' key '%s' prefix must be between 1 and 253 characters long",
			field, key,
		)
	}
	segments := strings.Split(prefix, ".")
	for _, segment := range segments {
		if segment == "" {
			return grpcstatus.Errorf(
				grpccodes.InvalidArgument,
				"field '%s' key '%s' prefix must not contain empty segments",
				field, key,
			)
		}
		err := s.validateDNSLabel(field, key, "prefix segment", segment)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *GenericServer[O]) validateDNSLabel(field string, key string, labelKind string, label string) error {
	if len(label) > 63 {
		return grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"field '%s' key '%s' %s must be at most 63 characters long",
			field, key, labelKind,
		)
	}
	for i, c := range label {
		isLower := c >= 'a' && c <= 'z'
		isDigit := c >= '0' && c <= '9'
		isHyphen := c == '-'
		if !isLower && !isDigit && !isHyphen {
			return grpcstatus.Errorf(
				grpccodes.InvalidArgument,
				"field '%s' key '%s' %s must only contain lowercase letters (a-z), digits (0-9) and "+
					"hyphens (-), but contains '%c' at position %d",
				field, key, labelKind, c, i,
			)
		}
	}
	if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
		return grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"field '%s' key '%s' %s cannot start or end with a hyphen",
			field, key, labelKind,
		)
	}
	return nil
}

func (s *GenericServer[O]) validateLabelNameOrValue(field string, key string, labelKind string, value string) error {
	if len(value) < 1 || len(value) > 63 {
		return grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"field '%s' key '%s' %s must be between 1 and 63 characters long",
			field, key, labelKind,
		)
	}
	for i, c := range value {
		isLower := c >= 'a' && c <= 'z'
		isDigit := c >= '0' && c <= '9'
		isHyphen := c == '-'
		isUnderscore := c == '_'
		isDot := c == '.'
		if !isLower && !isDigit && !isHyphen && !isUnderscore && !isDot {
			return grpcstatus.Errorf(
				grpccodes.InvalidArgument,
				"field '%s' key '%s' %s must only contain lowercase letters (a-z), digits (0-9), "+
					"hyphens (-), underscores (_) or dots (.), but contains '%c' at position %d",
				field, key, labelKind, c, i,
			)
		}
	}
	first := value[0]
	last := value[len(value)-1]
	firstIsAlnum := (first >= 'a' && first <= 'z') || (first >= '0' && first <= '9')
	lastIsAlnum := (last >= 'a' && last <= 'z') || (last >= '0' && last <= '9')
	if !firstIsAlnum || !lastIsAlnum {
		return grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"field '%s' key '%s' %s must start and end with an alphanumeric character",
			field, key, labelKind,
		)
	}
	return nil
}

func (s *GenericServer[O]) getMetadata(object O) metadataIface {
	objectReflect := object.ProtoReflect()
	if !objectReflect.Has(s.metadataField) {
		return nil
	}
	return objectReflect.Get(s.metadataField).Message().Interface().(metadataIface)
}

func (s *GenericServer[O]) setMetadata(object O, metadata metadataIface) {
	objectReflect := object.ProtoReflect()
	if metadata != nil {
		objectReflect.Set(s.metadataField, protoreflect.ValueOfMessage(metadata.ProtoReflect()))
	} else {
		objectReflect.Clear(s.metadataField)
	}
}

// determineAssignedCreator calls the attribution logic to determine the creator that will be assigned to an object
// that is being created. In case of error it returns a gRPC error that can be directly returned to the client.
func (s *GenericServer[O]) determineAssignedCreator(ctx context.Context) (result string, err error) {
	result, err = s.attributionLogic.DetermineAssignedCreator(ctx)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to determine assigned creator",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to determine assigned creator")
		return
	}
	return
}

// setCreator sets the creator in the object's metadata, creating the metadata if necessary. In case of error it
// returns a gRPC error that can be directly returned to the client.
func (s *GenericServer[O]) setCreator(ctx context.Context, object O, creator string) error {
	metadata := s.getMetadata(object)
	if metadata == nil {
		metadata = s.newMetadata()
		s.setMetadata(object, metadata)
	}
	metadata.SetCreator(creator)
	return nil
}

// determineAssignedTenant calls the tenancy logic to determine which tenant will be assigned to an object that is
// being created or updated. In case of error it returns a gRPC error that can be directly returned to the client.
func (s *GenericServer[O]) determineAssignedTenant(ctx context.Context,
	requestObject, currentObject O) (result string, err error) {
	// Check that there are visible tenants:
	visibleTenants, err := s.tenancyLogic.DetermineVisibleTenants(ctx)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to determine visible tenants",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to determine visible tenants")
		return
	}
	if visibleTenants.Empty() {
		err = grpcstatus.Errorf(grpccodes.PermissionDenied, "there are no visible tenants")
		return
	}

	// Determine the tenants that can be assigned to the object:
	assignableTenants, err := s.tenancyLogic.DetermineAssignableTenants(ctx)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to determine assignable tenants",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to determine assignable tenants")
		return
	}
	if assignableTenants.Empty() {
		err = grpcstatus.Errorf(grpccodes.PermissionDenied, "there are no assignable tenants")
		return
	}

	// Determine the default tenant:
	defaultTenant, err := s.tenancyLogic.DetermineDefaultTenant(ctx)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to determine default tenant",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to determine default tenant")
		return
	}
	if defaultTenant == "" {
		err = grpcstatus.Errorf(grpccodes.PermissionDenied, "there is no default tenant")
		return
	}

	// Get the tenant from the request and current object:
	requestTenant := s.getTenant(requestObject)
	currentTenant := s.getTenant(currentObject)

	// If the request specifies a tenant, check that it is visible and assignable:
	if requestTenant != "" {
		if !visibleTenants.Contains(requestTenant) {
			s.logger.WarnContext(
				ctx,
				"User is trying to assign a tenant that is invisible to them",
				slog.String("requested", requestTenant),
			)
			err = grpcstatus.Errorf(
				grpccodes.PermissionDenied,
				"tenant '%s' doesn't exist",
				requestTenant,
			)
			return
		}
		if !assignableTenants.Contains(requestTenant) {
			s.logger.WarnContext(
				ctx,
				"User is trying to assign a tenant that is unassignable",
				slog.String("requested", requestTenant),
			)
			err = grpcstatus.Errorf(
				grpccodes.PermissionDenied,
				"tenant '%s' can't be assigned",
				requestTenant,
			)
			return
		}
		result = requestTenant
		return
	}

	// Fall back to the current tenant or the default:
	if currentTenant != "" {
		result = currentTenant
	} else {
		result = defaultTenant
	}
	return
}

// getTenant extracts the tenant from an object's metadata.
func (s *GenericServer[O]) getTenant(object O) string {
	metadata := s.getMetadata(object)
	if metadata != nil {
		return metadata.GetTenant()
	}
	return ""
}

// setTenant sets the tenant in the object's metadata, creating the metadata if necessary. In case of error it
// returns a gRPC error that can be directly returned to the client.
func (s *GenericServer[O]) setTenant(ctx context.Context, object O, tenant string) error {
	metadata := s.getMetadata(object)
	if metadata == nil {
		metadata = s.newMetadata()
		s.setMetadata(object, metadata)
	}
	metadata.SetTenant(tenant)
	return nil
}

// newMetadata creates a new empty metadata message for the object type.
func (s *GenericServer[O]) newMetadata() metadataIface {
	var object O
	objectReflect := object.ProtoReflect()
	return objectReflect.NewField(s.metadataField).Message().Interface().(metadataIface)
}

// equivalentObjects checks if two objects are equivalentObjects, meaning they are equal except for the creation
// timestamp, deletion timestamp, and version fields in the metadata.
func (s *GenericServer[O]) equivalentObjects(x, y O) bool {
	return s.equivalentMessages(x.ProtoReflect(), y.ProtoReflect())
}

func (s *GenericServer[O]) equivalentMessages(x, y protoreflect.Message) bool {
	if x.IsValid() != y.IsValid() {
		return false
	}
	fields := x.Descriptor().Fields()
	for i := range fields.Len() {
		field := fields.Get(i)
		xPresent := x.Has(field)
		yPresent := y.Has(field)
		if xPresent != yPresent {
			return false
		}
		if !xPresent && !yPresent {
			continue
		}
		xValue := x.Get(field)
		yValue := y.Get(field)
		if field.Name() == "metadata" {
			if !s.equivalentMetadata(xValue.Message(), yValue.Message()) {
				return false
			}
		} else if !xValue.Equal(yValue) {
			return false
		}
	}
	return true
}

func (s *GenericServer[O]) equivalentMetadata(x, y protoreflect.Message) bool {
	if x.IsValid() != y.IsValid() {
		return false
	}
	fields := x.Descriptor().Fields()
	for i := range fields.Len() {
		field := fields.Get(i)
		name := field.Name()
		if name == "creation_timestamp" || name == "deletion_timestamp" || name == "version" {
			continue
		}
		xv := x.Get(field)
		yv := y.Get(field)
		if !xv.Equal(yv) {
			return false
		}
	}
	return true
}

// Names of gRPC methods:
const (
	listMethod   = "List"
	getMethod    = "Get"
	createMethod = "Create"
	updateMethod = "Update"
	deleteMethod = "Delete"
	signalMethod = "Signal"
)

// Names of fields:
const (
	eventPayloadField = "payload"
)
