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

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/collections"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/masks"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// PrepareCandidateFunc may modify the proposed object (candidate) before it is returned or saved.
// On Create, current is nil. On Update, current is a copy of the stored object before the request
// changes, and candidate is a separate copy containing those changes. Returning an error rejects
// the operation without changing the request or stored object.
type PrepareCandidateFunc[O dao.Object] func(ctx context.Context, current, candidate O) error

// GenericServerBuilder contains the data and logic needed to create new generic servers.
type GenericServerBuilder[O dao.Object] struct {
	logger            *slog.Logger
	service           string
	table             string
	ignoredFields     []any
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	allowedTenants    collections.Set[string]
	metricsRegisterer prometheus.Registerer
	filterDesc        protoreflect.MessageDescriptor
}

// GenericServer is a gRPC server that knows how to implement the List, Get, Create, Update and Delete operators for
// any object that has identifier and metadata fields.
type GenericServer[O dao.Object] struct {
	logger           *slog.Logger
	service          string
	dao              *dao.GenericDAO[O]
	attributionLogic auth.AttributionLogic
	tenancyLogic     auth.TenancyLogic
	allowedTenants   collections.Set[string]
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
	pathCompiler     *masks.PathCompiler[O]
	pathCache        map[string]*masks.Path[O]
	pathCacheLock    *sync.Mutex
	validator        protovalidate.Validator
}

type objectIface interface {
	proto.Message
	GetId() string
	SetId(string)
	GetMetadata() *privatev1.Metadata
	SetMetadata(*privatev1.Metadata)
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
	return &GenericServerBuilder[O]{
		allowedTenants: auth.DefaultAllowedTenants,
	}
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

// AddAllowedTenants adds tenant names to the set of tenants where objects can be created or moved
// into. By default the shared and system tenants are excluded. Use this to opt in platform-scoped
// resource servers that legitimately need to create objects in those tenants.
func (b *GenericServerBuilder[O]) AddAllowedTenants(values ...string) *GenericServerBuilder[O] {
	b.allowedTenants = b.allowedTenants.Union(collections.NewSet(values...))
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics. This is optional. If not set, no
// metrics will be recorded.
func (b *GenericServerBuilder[O]) SetMetricsRegisterer(value prometheus.Registerer) *GenericServerBuilder[O] {
	b.metricsRegisterer = value
	return b
}

// SetFilterDesc sets the protobuf message descriptor used to validate and translate CEL filter expressions. This is
// optional. When unset, the descriptor of the O generic parameter is used. Forwarded to
// [dao.GenericDAOBuilder.SetFilterDesc].
func (b *GenericServerBuilder[O]) SetFilterDesc(value protoreflect.MessageDescriptor) *GenericServerBuilder[O] {
	b.filterDesc = value
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
		allowedTenants:   b.allowedTenants,
		pathCompiler:     pathCompiler,
		pathCache:        map[string]*masks.Path[O]{},
		pathCacheLock:    &sync.Mutex{},
		validator:        validator,
	}

	// Create the DAO:
	daoBuilder := dao.NewGenericDAO[O]()
	daoBuilder.SetLogger(b.logger)
	if b.table != "" {
		daoBuilder.SetTableName(b.table)
	}
	daoBuilder.SetFilterDesc(b.filterDesc)
	daoBuilder.SetTenancyLogic(b.tenancyLogic)
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

	// Keep an empty object to clone when a Create request omits one:
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

	// Find the request and response types for each method. Responses are cloned when needed.
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

func (s *GenericServer[O]) List(ctx context.Context, request any, response any) error {
	// Extract the request message:
	type requestIface interface {
		GetOffset() int32
		GetLimit() int32
		HasLimit() bool
		GetFilter() string
	}
	requestMsg := request.(requestIface)

	// List the objects:
	listRequest := s.dao.List().
		SetFilter(requestMsg.GetFilter()).
		SetOffset(requestMsg.GetOffset())
	if requestMsg.HasLimit() {
		listRequest.SetLimit(requestMsg.GetLimit())
	}
	daoResponse, err := listRequest.Do(ctx)
	if err != nil {
		var validationErr *dao.ErrValidation
		if errors.As(err, &validationErr) {
			return grpcstatus.Errorf(grpccodes.InvalidArgument, "%s", validationErr.Reason)
		}
		var deniedErr *dao.ErrDenied
		if errors.As(err, &deniedErr) {
			return grpcstatus.Errorf(grpccodes.PermissionDenied, "%s", deniedErr.Reason)
		}
		var deadlockErr *dao.ErrDeadlock
		if errors.As(err, &deadlockErr) {
			return grpcstatus.Errorf(grpccodes.Aborted, "%s", deadlockErr.Error())
		}
		var invalidFilterErr *dao.ErrInvalidFilter
		if errors.As(err, &invalidFilterErr) {
			return grpcstatus.Errorf(grpccodes.InvalidArgument, "%s", invalidFilterErr.Error())
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

// isSingletonConstraintViolation reports whether constraintName identifies a unique partial index that enforces a
// "singleton" or "single default" invariant (e.g. network_classes_singleton, network_classes_single_default) rather
// than an ordinary per-object name/ID uniqueness constraint.
func isSingletonConstraintViolation(constraintName string) bool {
	return strings.HasSuffix(constraintName, "_singleton") || strings.HasSuffix(constraintName, "_single_default")
}

func (s *GenericServer[O]) Create(ctx context.Context, request any, response any) error {
	return s.CreateWithCandidatePreparation(ctx, request, response, nil)
}

// CreateWithCandidatePreparation copies the requested object and assigns its creator and tenant.
// If provided, prepareCandidate may modify that copy before its final validation. A successful
// request saves the result; a dry run returns it without saving.
func (s *GenericServer[O]) CreateWithCandidatePreparation(
	ctx context.Context,
	request any,
	response any,
	prepareCandidate PrepareCandidateFunc[O],
) error {
	requestObject, err := s.prepareForCreate(ctx, request)
	if err != nil {
		return err
	}

	var nilObject O
	if prepareCandidate != nil {
		preparedID := requestObject.GetId()
		preparedMetadata := proto.Clone(s.getMetadata(requestObject)).(metadataIface)
		if err = prepareCandidate(ctx, nilObject, requestObject); err != nil {
			return err
		}
		if err = s.validatePreparedCandidate(ctx, requestObject, preparedID, preparedMetadata); err != nil {
			return err
		}
	}

	return s.createPrepared(ctx, requestObject, response)
}

func (s *GenericServer[O]) createPrepared(ctx context.Context, requestObject O, response any) error {
	if s.isNil(requestObject) {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
	}

	// In dry-run mode, return the validated candidate in a fresh create response.
	// The response includes defaults and resolved references produced during
	// preparation. Database writes and creation events are skipped; validation may
	// still perform database reads.
	if isDryRun(ctx) {
		type responseIface interface {
			SetObject(O)
		}
		responseMsg := proto.Clone(s.createResponse).(responseIface)
		responseMsg.SetObject(requestObject)
		s.setPointer(response, responseMsg)
		return nil
	}

	daoResponse, err := s.dao.Create().SetObject(requestObject).Do(ctx)
	if err != nil {
		var alreadyExistsErr *dao.ErrAlreadyExists
		if errors.As(err, &alreadyExistsErr) {
			// A unique partial index on a constant expression (e.g. network_classes_singleton,
			// network_classes_single_default) models a "singleton" or "single default" invariant rather than a
			// per-object name/ID collision. Report those as FailedPrecondition (retry may succeed once the
			// conflicting row is gone) instead of AlreadyExists (which implies the *new* object is a duplicate).
			if isSingletonConstraintViolation(alreadyExistsErr.ConstraintName) {
				return grpcstatus.Errorf(grpccodes.FailedPrecondition,
					"concurrent create violated a singleton invariant (constraint '%s'); please retry",
					alreadyExistsErr.ConstraintName)
			}
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
		s.logger.ErrorContext(ctx, "Failed to create", slog.Any("error", err))
		return grpcstatus.Errorf(grpccodes.Internal, "failed to create object")
	}

	// Create the response message:
	type responseIface interface {
		SetObject(O)
	}
	responseMsg := proto.Clone(s.createResponse).(responseIface)
	responseMsg.SetObject(daoResponse.GetObject())
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
		requestObject = proto.Clone(requestObject).(O)
	}

	requestMetadata := s.getMetadata(requestObject)
	if requestMetadata == nil {
		return nilObject, grpcstatus.Errorf(grpccodes.InvalidArgument, "metadata is required")
	}
	if err := s.validateMetadata(ctx, requestMetadata); err != nil {
		return nilObject, err
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
	if err = s.checkAllowedTenant(assignedTenant); err != nil {
		return nilObject, err
	}

	return requestObject, nil
}

func (s *GenericServer[O]) checkAllowedTenant(tenant string) error {
	if !s.allowedTenants.Contains(tenant) {
		return grpcstatus.Errorf(
			grpccodes.PermissionDenied,
			"objects cannot be placed in the '%s' tenant",
			tenant,
		)
	}
	return nil
}

// validatePreparedCandidate checks the object after preparation. The callback may change its
// contents, but not the ID, creator, tenant, or project established before the callback. The
// resulting metadata and object must also pass validation.
func (s *GenericServer[O]) validatePreparedCandidate(
	ctx context.Context, candidate O, preparedID string, preparedMetadata metadataIface,
) error {
	metadata := s.getMetadata(candidate)
	if metadata == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "metadata is required")
	}
	if candidate.GetId() != preparedID || metadata.GetTenant() != preparedMetadata.GetTenant() ||
		metadata.GetProject() != preparedMetadata.GetProject() || metadata.GetCreator() != preparedMetadata.GetCreator() {
		return grpcstatus.Errorf(grpccodes.PermissionDenied, "candidate preparation cannot change identity or ownership metadata")
	}
	if err := s.validateMetadata(ctx, metadata); err != nil {
		return err
	}
	if err := s.validator.Validate(candidate); err != nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "validation failed: %s", err)
	}
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
	return s.UpdateWithCandidatePreparation(ctx, request, response, nil)
}

// UpdateWithCandidatePreparation builds a proposed object by applying masked fields to a copy
// of the stored object, or copying the full request when there is no mask. If provided, the
// callback sees a separate copy of the stored object and may modify the proposal. The result
// is validated, then saved only if it differs from the stored object.
func (s *GenericServer[O]) UpdateWithCandidatePreparation(
	ctx context.Context,
	request any,
	response any,
	prepareCandidate PrepareCandidateFunc[O],
) error {
	return s.updateWithCandidatePreparation(ctx, request, response, prepareCandidate, false)
}

func (s *GenericServer[O]) updateWithCandidatePreparation(
	ctx context.Context,
	request any,
	response any,
	prepareCandidate PrepareCandidateFunc[O],
	prepareBeforeValidation bool,
) error {
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

	// Update the fields indicated in the update mask, or all the fields if there is no update mask:
	requestMask := requestMsg.GetUpdateMask()
	var tmpObject O
	if requestMask != nil {
		// Keep the stored object unchanged for comparison and detach any values copied from the request.
		tmpObject = proto.Clone(currentObject).(O)
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
		tmpObject = proto.Clone(tmpObject).(O)
	} else {
		tmpObject = proto.Clone(requestObject).(O)
	}

	if !prepareBeforeValidation {
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
	// Only check the tenant restriction when the tenant is changing — existing objects
	// in reserved tenants can be updated in place but cannot be moved into them.
	currentTenant := s.getMetadata(currentObject).GetTenant()
	if assignedTenant != currentTenant {
		if err = s.checkAllowedTenant(assignedTenant); err != nil {
			return err
		}
	}

	if prepareCandidate != nil {
		preparedID := tmpObject.GetId()
		preparedMetadata := proto.Clone(s.getMetadata(tmpObject)).(metadataIface)
		if err = prepareCandidate(ctx, proto.Clone(currentObject).(O), tmpObject); err != nil {
			return err
		}
		if err = s.validatePreparedCandidate(ctx, tmpObject, preparedID, preparedMetadata); err != nil {
			return err
		}
	}

	// Save the object only if there is any actual difference:
	var responseObject O
	if !s.equivalentObjects(tmpObject, currentObject) {
		updateResponse, err := s.dao.Update().
			SetObject(tmpObject).
			Do(ctx)
		if err != nil {
			return s.translateUpdateError(ctx, requestId, err)
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

// UpdateWithValidation adapts the networking resource validators to the generic
// candidate-preparation update path. It preserves the validator's candidate-first
// argument order while keeping the stored object read and field-mask merge single-pass.
func (s *GenericServer[O]) UpdateWithValidation(
	ctx context.Context,
	request any,
	response any,
	validate func(context.Context, O, O) error,
) error {
	return s.updateWithCandidatePreparation(ctx, request, response,
		func(ctx context.Context, current, candidate O) error {
			return validate(ctx, candidate, current)
		}, true)
}

func (s *GenericServer[O]) translateUpdateError(ctx context.Context, requestId string, err error) error {
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
	var inUseErr *dao.ErrInUse
	if errors.As(err, &inUseErr) {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition, "%s", inUseErr.Error())
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

	// Signal the object:
	_, err := s.dao.Signal().
		SetId(requestId).
		Do(ctx)
	if err != nil {
		if _, ok := errors.AsType[*dao.ErrNotFound](err); ok {
			return grpcstatus.Errorf(
				grpccodes.NotFound,
				"object with identifier '%s' not found",
				requestId,
			)
		}
		if deniedErr, ok := errors.AsType[*dao.ErrDenied](err); ok {
			return grpcstatus.Errorf(grpccodes.PermissionDenied, "%s", deniedErr.Error())
		}
		if deadlockErr, ok := errors.AsType[*dao.ErrDeadlock](err); ok {
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

	// Create the response:
	responseMsg := proto.Clone(s.signalResponse)
	s.setPointer(response, responseMsg)

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
// Label keys consist of an optional prefix and a name separated by '/':
//   - Prefix: DNS subdomain (1-253 chars), dot-separated DNS labels
//   - Name: 1-63 chars, must start and end with alphanumeric, allows lowercase letters, digits, hyphens, underscores, dots
//   - Total key length: up to 317 chars (253 + "/" + 63)
//
// Label values: 0-63 chars (empty allowed), same character restrictions as names.
//
// This validation is performed in Go code (no protovalidate annotations on the labels field).
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
	// Determine the visibility:
	visibility, err := s.tenancyLogic.DetermineVisibility(ctx)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to determine visibility",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to determine visibility")
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
		if !visibility.IsTenantVisible(requestTenant) {
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
