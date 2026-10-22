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
	"log/slog"
	"slices"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/vault"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type ComputeInstancesServerBuilder struct {
	logger            *slog.Logger
	attributionLogic  auth.AttributionLogic
	tenancyLogic      auth.TenancyLogic
	metricsRegisterer prometheus.Registerer
	secretStore       vault.SecretStore
}

var _ publicv1.ComputeInstancesServer = (*ComputeInstancesServer)(nil)

type ComputeInstancesServer struct {
	publicv1.UnimplementedComputeInstancesServer

	logger    *slog.Logger
	delegate  privatev1.ComputeInstancesServer
	inMapper  *GenericMapper[*publicv1.ComputeInstance, *privatev1.ComputeInstance]
	outMapper *GenericMapper[*privatev1.ComputeInstance, *publicv1.ComputeInstance]
}

func NewComputeInstancesServer() *ComputeInstancesServerBuilder {
	return &ComputeInstancesServerBuilder{}
}

// SetLogger sets the logger to use. This is mandatory.
func (b *ComputeInstancesServerBuilder) SetLogger(value *slog.Logger) *ComputeInstancesServerBuilder {
	b.logger = value
	return b
}

// SetAttributionLogic sets the attribution logic to use. This is optional.
func (b *ComputeInstancesServerBuilder) SetAttributionLogic(value auth.AttributionLogic) *ComputeInstancesServerBuilder {
	b.attributionLogic = value
	return b
}

// SetTenancyLogic sets the tenancy logic to use. This is mandatory.
func (b *ComputeInstancesServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *ComputeInstancesServerBuilder {
	b.tenancyLogic = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer used to register the metrics for the underlying database
// access objects. This is optional. If not set, no metrics will be recorded.
func (b *ComputeInstancesServerBuilder) SetMetricsRegisterer(value prometheus.Registerer) *ComputeInstancesServerBuilder {
	b.metricsRegisterer = value
	return b
}

func (b *ComputeInstancesServerBuilder) SetSecretStore(value vault.SecretStore) *ComputeInstancesServerBuilder {
	b.secretStore = value
	return b
}

func (b *ComputeInstancesServerBuilder) Build() (result *ComputeInstancesServer, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.tenancyLogic == nil {
		err = errors.New("tenancy logic is mandatory")
		return
	}

	// Create the mappers:
	inMapper, err := NewGenericMapper[*publicv1.ComputeInstance, *privatev1.ComputeInstance]().
		SetLogger(b.logger).
		SetStrict(true).
		// The status is output-only: only the system writes it. Users can read it
		// but never supply it.
		AddIgnoredFields("status").
		Build()
	if err != nil {
		return
	}
	outMapper, err := NewGenericMapper[*privatev1.ComputeInstance, *publicv1.ComputeInstance]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return
	}

	// Create the private server to delegate to:
	delegate, err := NewPrivateComputeInstancesServer().
		SetLogger(b.logger).
		SetAttributionLogic(b.attributionLogic).
		SetTenancyLogic(b.tenancyLogic).
		SetMetricsRegisterer(b.metricsRegisterer).
		SetSecretStore(b.secretStore).
		SetFilterDesc((*publicv1.ComputeInstance)(nil).ProtoReflect().Descriptor()).
		Build()
	if err != nil {
		return
	}

	// Create and populate the object:
	result = &ComputeInstancesServer{
		logger:    b.logger,
		delegate:  delegate,
		inMapper:  inMapper,
		outMapper: outMapper,
	}
	return
}

func (s *ComputeInstancesServer) List(ctx context.Context,
	request *publicv1.ComputeInstancesListRequest) (response *publicv1.ComputeInstancesListResponse, err error) {
	// Create private request with same parameters:
	privateRequest := &privatev1.ComputeInstancesListRequest{}
	privateRequest.SetOffset(request.GetOffset())
	if request.HasLimit() {
		privateRequest.SetLimit(request.GetLimit())
	}
	privateRequest.SetFilter(request.GetFilter())

	// Delegate to private server:
	privateResponse, err := s.delegate.List(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map private response to public format:
	privateItems := privateResponse.GetItems()
	publicItems := make([]*publicv1.ComputeInstance, len(privateItems))
	for i, privateItem := range privateItems {
		publicItem := &publicv1.ComputeInstance{}
		err = s.outMapper.Copy(ctx, privateItem, publicItem)
		if err != nil {
			s.logger.ErrorContext(
				ctx,
				"Failed to map private compute instance to public",
				slog.Any("error", err),
			)
			return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process compute instances")
		}
		publicItems[i] = publicItem
	}

	// Create the public response:
	response = &publicv1.ComputeInstancesListResponse{}
	response.SetSize(privateResponse.GetSize())
	response.SetTotal(privateResponse.GetTotal())
	response.SetItems(publicItems)
	return
}

func (s *ComputeInstancesServer) Get(ctx context.Context,
	request *publicv1.ComputeInstancesGetRequest) (response *publicv1.ComputeInstancesGetResponse, err error) {
	// Create private request:
	privateRequest := &privatev1.ComputeInstancesGetRequest{}
	privateRequest.SetId(request.GetId())

	// Delegate to private server:
	privateResponse, err := s.delegate.Get(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map private response to public format:
	privateComputeInstance := privateResponse.GetObject()
	publicComputeInstance := &publicv1.ComputeInstance{}
	err = s.outMapper.Copy(ctx, privateComputeInstance, publicComputeInstance)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private compute instance to public",
			slog.Any("error", err),
		)
		return nil, grpcstatus.Errorf(grpccodes.Internal, "failed to process compute instance")
	}

	// Create the public response:
	response = &publicv1.ComputeInstancesGetResponse{}
	response.SetObject(publicComputeInstance)
	return
}

func (s *ComputeInstancesServer) Create(ctx context.Context,
	request *publicv1.ComputeInstancesCreateRequest) (response *publicv1.ComputeInstancesCreateResponse, err error) {
	// Map the public compute instance to private format:
	publicComputeInstance := request.GetObject()
	if publicComputeInstance == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	privateComputeInstance := &privatev1.ComputeInstance{}
	err = s.inMapper.Copy(ctx, publicComputeInstance, privateComputeInstance)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public compute instance to private",
			slog.Any("error", err),
		)
		err = publicInputMappingError(err, "failed to process compute instance")
		return
	}
	// The status is output-only: only the system writes it. The metering source contract
	// is not enforced on inserts, so a caller-supplied status must never reach the
	// database as a fabricated fact.
	privateComputeInstance.ClearStatus()

	// Delegate to the private server:
	privateRequest := &privatev1.ComputeInstancesCreateRequest{}
	privateRequest.SetObject(privateComputeInstance)
	privateResponse, err := s.delegate.Create(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map the private response back to public format:
	createdPrivateComputeInstance := privateResponse.GetObject()
	createdPublicComputeInstance := &publicv1.ComputeInstance{}
	err = s.outMapper.Copy(ctx, createdPrivateComputeInstance, createdPublicComputeInstance)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private compute instance to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process compute instance")
		return
	}

	// Create the public response:
	response = &publicv1.ComputeInstancesCreateResponse{}
	response.SetObject(createdPublicComputeInstance)
	// Propagate warnings from the private server response (deprecation notices
	// for DEPRECATED instance types).
	response.SetWarnings(privateResponse.GetWarnings())
	return
}

// Update applies permitted public changes to a compute instance while
// preserving system-owned fields. The status is read-only: an omitted mask
// infers the supplied writable fields, an explicit mask selects only
// permitted roots, and the changes apply under the generic updater's row
// lock.
func (s *ComputeInstancesServer) Update(ctx context.Context,
	request *publicv1.ComputeInstancesUpdateRequest) (response *publicv1.ComputeInstancesUpdateResponse, err error) {
	ctx = withPublicAPIView(ctx)
	if err := validatePublicObjectMetadata(request.GetObject()); err != nil {
		return nil, err
	}

	// Validate the request:
	publicComputeInstance := request.GetObject()
	if publicComputeInstance == nil {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object is mandatory")
		return
	}
	id := publicComputeInstance.GetId()
	if id == "" {
		err = grpcstatus.Errorf(grpccodes.InvalidArgument, "object identifier is mandatory")
		return
	}

	// The public API has no writable status: build a mask that selects only the
	// permitted public fields, inferring them from the supplied input when the
	// caller omits the mask.
	updateMask, err := computeInstancePublicUpdateMask(publicComputeInstance, request.GetUpdateMask())
	if err != nil {
		return nil, err
	}

	// Map the public compute instance to a new private object and delegate to the
	// private server, which applies the permitted changes to the locked current
	// database object:
	privateComputeInstance := &privatev1.ComputeInstance{}
	err = s.inMapper.Copy(ctx, publicComputeInstance, privateComputeInstance)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map public compute instance to private",
			slog.Any("error", err),
		)
		err = publicInputMappingError(err, "failed to process compute instance")
		return
	}

	// Delegate to the private server:
	privateRequest := &privatev1.ComputeInstancesUpdateRequest{}
	privateRequest.SetObject(privateComputeInstance)
	privateRequest.SetUpdateMask(updateMask)
	privateRequest.SetLock(request.GetLock())
	privateResponse, err := s.delegate.Update(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Map the private response back to public format:
	updatedPrivateComputeInstance := privateResponse.GetObject()
	updatedPublicComputeInstance := &publicv1.ComputeInstance{}
	err = s.outMapper.Copy(ctx, updatedPrivateComputeInstance, updatedPublicComputeInstance)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to map private compute instance to public",
			slog.Any("error", err),
		)
		err = grpcstatus.Errorf(grpccodes.Internal, "failed to process compute instance")
		return
	}

	// Create the public response:
	response = &publicv1.ComputeInstancesUpdateResponse{}
	response.SetObject(updatedPublicComputeInstance)
	response.SetWarnings(privateResponse.GetWarnings())
	return
}

// computeInstancePublicUpdateMask selects only public writable fields. When the
// caller supplies an update mask it is validated and whole-metadata masks are
// expanded to the permitted child paths; status and any other non-writable
// field is rejected. When the caller omits the mask, paths are inferred from
// the populated writable fields of the object, so omitted fields stay
// unchanged. The returned mask is fresh and never nil.
func computeInstancePublicUpdateMask(
	object *publicv1.ComputeInstance,
	mask *fieldmaskpb.FieldMask,
) (*fieldmaskpb.FieldMask, error) {
	permittedMetadata := []string{
		"metadata.labels",
		"metadata.annotations",
		"metadata.display_name",
		"metadata.description",
	}
	paths := []string{}
	if mask != nil {
		for _, path := range mask.GetPaths() {
			path = normalizeFieldMaskPath(path)
			// The status is output-only: only the system writes it. The whole
			// status and every descendant are rejected.
			if path == "status" || strings.HasPrefix(path, "status.") {
				return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "%s is output-only", path)
			}
			if path == "spec" || strings.HasPrefix(path, "spec.") {
				// Spec path validity is checked by the private server's field-path
				// compiler, which owns schema knowledge for accepted subpaths.
				paths = append(paths, path)
				continue
			}
			if path == "metadata" {
				paths = append(paths, permittedMetadata...)
				continue
			}
			if slices.Contains(permittedMetadata, path) {
				paths = append(paths, path)
				continue
			}
			return nil, grpcstatus.Errorf(grpccodes.InvalidArgument, "%s is not writable", path)
		}
	} else if object != nil {
		// Infer paths from the populated writable fields of the object:
		message := object.ProtoReflect()
		if spec := populatedChild(message, "spec"); spec != nil {
			fields := spec.Descriptor().Fields()
			for i := range fields.Len() {
				field := fields.Get(i)
				if spec.Has(field) {
					paths = append(paths, "spec."+string(field.Name()))
				}
			}
		}
		if metadata := populatedChild(message, "metadata"); metadata != nil {
			for _, name := range permittedMetadata {
				child := strings.TrimPrefix(name, "metadata.")
				field := metadata.Descriptor().Fields().ByName(protoreflect.Name(child))
				if field != nil && metadata.Has(field) {
					paths = append(paths, name)
				}
			}
		}
	}
	slices.Sort(paths)
	paths = slices.Compact(paths)
	return &fieldmaskpb.FieldMask{Paths: paths}, nil
}

// populatedChild returns the named submessage of a message when it is set,
// or nil when the field is absent. The result is not recursive: only the
// direct child message is returned.
func populatedChild(message protoreflect.Message, name string) protoreflect.Message {
	field := message.Descriptor().Fields().ByName(protoreflect.Name(name))
	if field == nil || !message.Has(field) {
		return nil
	}
	return message.Get(field).Message()
}

// normalizeFieldMaskPath trims whitespace from each dot-separated part of a
// field-mask path and removes empty parts, so spellings like "status. state"
// and "status..state" normalize to "status.state".
func normalizeFieldMaskPath(path string) string {
	parts := strings.Split(path, ".")
	normalizedParts := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			normalizedParts = append(normalizedParts, part)
		}
	}
	return strings.Join(normalizedParts, ".")
}

func (s *ComputeInstancesServer) Delete(ctx context.Context,
	request *publicv1.ComputeInstancesDeleteRequest) (response *publicv1.ComputeInstancesDeleteResponse, err error) {
	// Create private request:
	privateRequest := &privatev1.ComputeInstancesDeleteRequest{}
	privateRequest.SetId(request.GetId())

	// Delegate to private server:
	_, err = s.delegate.Delete(ctx, privateRequest)
	if err != nil {
		return nil, err
	}

	// Create the public response:
	response = &publicv1.ComputeInstancesDeleteResponse{}
	return
}
