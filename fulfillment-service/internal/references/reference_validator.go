/*
Copyright (c) 2025 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package references

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// ResolvedRef is the result of resolving a reference to a known resource.
type ResolvedRef struct {
	ID      string
	Tenant  string
	Project string
	Name    string
}

// ReferenceLookupFunc resolves a resource by id, name, or both within a tenant/project scope.
// Returns the fully-resolved reference or an error. Errors that satisfy the IsNotFound() interface
// method are treated as invalid references; all other errors are treated as internal failures.
type ReferenceLookupFunc func(
	ctx context.Context,
	tenant, project, id, name string,
) (*ResolvedRef, error)

// ReferenceValidatorBuilder configures and creates a ReferenceValidator. Don't create instances
// of this type directly, use the NewReferenceValidator function instead.
type ReferenceValidatorBuilder struct {
	logger     *slog.Logger
	registerer prometheus.Registerer
}

// ReferenceValidator is a gRPC interceptor that validates resource references in Create and Update
// requests using protoreflect. It discovers reference-typed fields by naming convention (message
// types ending with "Reference" or "LocalReference"), validates them against registered lookup
// functions, and mutates the request to auto-populate missing id or name fields.
type ReferenceValidator struct {
	logger             *slog.Logger
	registry           map[protoreflect.FullName]ReferenceLookupFunc
	sealed             atomic.Bool
	validationTotal    *prometheus.CounterVec
	validationDuration *prometheus.HistogramVec
}

// NewReferenceValidator creates a builder that can then be used to configure and create a new
// reference validation interceptor.
func NewReferenceValidator() *ReferenceValidatorBuilder {
	return &ReferenceValidatorBuilder{}
}

// SetLogger sets the logger. This is mandatory.
func (b *ReferenceValidatorBuilder) SetLogger(value *slog.Logger) *ReferenceValidatorBuilder {
	b.logger = value
	return b
}

// SetMetricsRegisterer sets the Prometheus registerer for metrics. This is optional.
func (b *ReferenceValidatorBuilder) SetMetricsRegisterer(value prometheus.Registerer) *ReferenceValidatorBuilder {
	b.registerer = value
	return b
}

// Build uses the data stored in the builder to create and configure a new interceptor.
func (b *ReferenceValidatorBuilder) Build() (result *ReferenceValidator, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}

	var validationTotal *prometheus.CounterVec
	var validationDuration *prometheus.HistogramVec

	if b.registerer != nil {
		validationTotal, err = registerOrReuse(b.registerer, prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "osac_reference_validation_total",
				Help: "Total number of reference validations performed.",
			},
			[]string{"resource_type", "result"},
		))
		if err != nil {
			return
		}

		validationDuration, err = registerOrReuse(b.registerer, prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "osac_reference_validation_duration_seconds",
				Help:    "Duration of individual reference validation lookups in seconds.",
				Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0},
			},
			[]string{"resource_type"},
		))
		if err != nil {
			return
		}
	}

	result = &ReferenceValidator{
		logger:             b.logger,
		registry:           make(map[protoreflect.FullName]ReferenceLookupFunc),
		validationTotal:    validationTotal,
		validationDuration: validationDuration,
	}
	return
}

// Register associates a reference message type with a lookup function. Must be called before the
// gRPC server starts accepting requests.
func (v *ReferenceValidator) Register(fullName protoreflect.FullName, lookupFunc ReferenceLookupFunc) {
	if v.sealed.Load() {
		panic(fmt.Sprintf("Register called after interceptor started serving: %s", fullName))
	}
	v.registry[fullName] = lookupFunc
}

// UnaryServer is the unary server interceptor function.
func (v *ReferenceValidator) UnaryServer(ctx context.Context, request any, info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler) (response any, err error) {
	v.sealed.CompareAndSwap(false, true)
	if !isCreateOrUpdate(info.FullMethod) {
		return handler(ctx, request)
	}

	err = v.validate(ctx, request)
	if err != nil {
		return
	}

	return handler(ctx, request)
}

// StreamServer is the stream server interceptor function. Reference validation is not applicable
// to streams, so this is a pass-through.
func (v *ReferenceValidator) StreamServer(srv any, stream grpc.ServerStream,
	info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	return handler(srv, stream)
}

// validate walks the request message and validates all reference-typed fields.
func (v *ReferenceValidator) validate(ctx context.Context, request any) error {
	message, ok := request.(proto.Message)
	if !ok {
		return nil
	}

	tenant, project := extractTenantProject(message)

	var violations []*errdetails.BadRequest_FieldViolation
	err := v.walkMessage(ctx, message.ProtoReflect(), nil, &violations, tenant, project)
	if err != nil {
		return err
	}

	if len(violations) > 0 {
		sort.Slice(violations, func(i, j int) bool {
			return violations[i].GetField() < violations[j].GetField()
		})
		descriptions := make([]string, len(violations))
		for i, fv := range violations {
			descriptions[i] = fmt.Sprintf("%s: %s", fv.GetField(), fv.GetDescription())
		}

		st, detailErr := grpcstatus.New(
			grpccodes.InvalidArgument,
			fmt.Sprintf("reference validation failed: %s", strings.Join(descriptions, "; ")),
		).WithDetails(&errdetails.BadRequest{FieldViolations: violations})
		if detailErr != nil {
			return grpcstatus.Errorf(grpccodes.Internal,
				"failed to attach error details: %v", detailErr)
		}
		return st.Err()
	}

	return nil
}

// walkMessage recursively walks a protoreflect.Message, discovering and validating reference-typed
// fields. Appends FieldViolation entries for invalid references. Mutates the message to fill in
// missing reference fields.
func (v *ReferenceValidator) walkMessage(ctx context.Context, msg protoreflect.Message, path []string,
	violations *[]*errdetails.BadRequest_FieldViolation, tenant, project string) error {
	var internalErr error

	msg.Range(func(fd protoreflect.FieldDescriptor, val protoreflect.Value) bool {
		if fd.Kind() != protoreflect.MessageKind {
			return true
		}

		fieldPath := append(append([]string{}, path...), string(fd.Name()))

		if fd.IsMap() {
			if fd.MapValue().Kind() == protoreflect.MessageKind {
				v.logger.WarnContext(ctx, "Skipping map field with message values — map reference validation not yet supported",
					"field_path", strings.Join(fieldPath, "."),
				)
			}
			return true
		}

		if fd.IsList() {
			list := val.List()
			for i := 0; i < list.Len(); i++ {
				elemMsg := list.Get(i).Message()
				fullName := elemMsg.Descriptor().FullName()
				indexedPath := append(append([]string{}, fieldPath[:len(fieldPath)-1]...),
					fmt.Sprintf("%s[%d]", fd.Name(), i))

				if isReferenceType(fullName) {
					err := v.resolveAndMutate(ctx, elemMsg, fullName, indexedPath,
						violations, tenant, project)
					if err != nil {
						internalErr = err
						return false
					}
					continue
				}
				err := v.walkMessage(ctx, elemMsg, indexedPath, violations, tenant, project)
				if err != nil {
					internalErr = err
					return false
				}
			}
			return true
		}

		subMsg := val.Message()
		fullName := subMsg.Descriptor().FullName()

		if isReferenceType(fullName) {
			err := v.resolveAndMutate(ctx, subMsg, fullName, fieldPath,
				violations, tenant, project)
			if err != nil {
				internalErr = err
				return false
			}
			return true
		}
		err := v.walkMessage(ctx, subMsg, fieldPath, violations, tenant, project)
		if err != nil {
			internalErr = err
			return false
		}

		return true
	})

	return internalErr
}

// resolveAndMutate validates a single reference field against its registered lookup function and
// mutates the reference message to fill in missing fields.
func (v *ReferenceValidator) resolveAndMutate(ctx context.Context, refMsg protoreflect.Message,
	fullName protoreflect.FullName, path []string,
	violations *[]*errdetails.BadRequest_FieldViolation,
	callerTenant, callerProject string) error {

	lookupFunc, ok := v.registry[fullName]
	if !ok {
		v.logger.ErrorContext(ctx, "No lookup registered for reference type",
			"reference_type", string(fullName),
			"field_path", strings.Join(path, "."),
		)
		return grpcstatus.Errorf(grpccodes.Internal,
			"no lookup registered for reference type %q", fullName)
	}

	refTenant, refProject := resolveTenantProject(refMsg, fullName, callerTenant, callerProject)

	idField := refMsg.Descriptor().Fields().ByName("id")
	nameField := refMsg.Descriptor().Fields().ByName("name")
	if !isStringField(idField) || !isStringField(nameField) {
		return grpcstatus.Errorf(grpccodes.Internal,
			"reference type %q does not have required string id/name fields", fullName)
	}
	id := refMsg.Get(idField).String()
	name := refMsg.Get(nameField).String()

	resourceType := string(fullName.Name())
	fieldPath := strings.Join(path, ".")

	if id == "" && name == "" {
		*violations = append(*violations, &errdetails.BadRequest_FieldViolation{
			Field:       fieldPath,
			Description: fmt.Sprintf("%s reference must specify id or name", shortName(fullName)),
		})
		v.recordResult(resourceType, "invalid")
		return nil
	}

	start := time.Now()
	resolved, err := lookupFunc(ctx, refTenant, refProject, id, name)
	elapsed := time.Since(start)

	v.recordDuration(resourceType, elapsed)

	if err != nil {
		if isNotFoundErr(err) {
			desc := fmt.Sprintf("%s %q not found", shortName(fullName), identifier(id, name))
			*violations = append(*violations, &errdetails.BadRequest_FieldViolation{
				Field:       fieldPath,
				Description: desc,
			})
			v.logger.WarnContext(ctx, "Reference not found",
				"field_path", fieldPath,
				"reference_type", resourceType,
				"!identifier", identifier(id, name),
				"!tenant", refTenant,
				"!project", refProject,
			)
			v.recordResult(resourceType, "invalid")
			return nil
		}
		v.logger.ErrorContext(ctx, "Reference lookup failed",
			"field_path", fieldPath,
			"reference_type", resourceType,
			"error", err,
		)
		v.recordResult(resourceType, "error")
		return grpcstatus.Errorf(grpccodes.Internal,
			"internal error resolving reference at %s", fieldPath)
	}

	if resolved == nil {
		v.logger.ErrorContext(ctx, "Reference lookup returned no result and no error",
			"field_path", fieldPath,
			"reference_type", resourceType,
		)
		v.recordResult(resourceType, "error")
		return grpcstatus.Errorf(grpccodes.Internal,
			"internal error resolving reference at %s", fieldPath)
	}

	if id == "" && resolved.ID != "" {
		refMsg.Set(idField, protoreflect.ValueOfString(resolved.ID))
	}
	if name == "" && resolved.Name != "" {
		refMsg.Set(nameField, protoreflect.ValueOfString(resolved.Name))
	}

	if id != "" && name != "" && (resolved.ID != id || resolved.Name != name) {
		desc := fmt.Sprintf("id %q and name %q do not refer to the same resource", id, name)
		*violations = append(*violations, &errdetails.BadRequest_FieldViolation{
			Field:       fieldPath,
			Description: desc,
		})
		v.logger.WarnContext(ctx, "Reference id/name mismatch",
			"field_path", fieldPath,
			"reference_type", resourceType,
			"!id", id,
			"!name", name,
		)
		v.recordResult(resourceType, "invalid")
		return nil
	}

	v.logger.DebugContext(ctx, "Reference validated",
		"field_path", fieldPath,
		"reference_type", resourceType,
		"!resolved_id", resolved.ID,
		"!resolved_name", resolved.Name,
	)
	v.recordResult(resourceType, "valid")
	return nil
}

func (v *ReferenceValidator) recordResult(resourceType, result string) {
	if v.validationTotal != nil {
		v.validationTotal.With(prometheus.Labels{
			"resource_type": resourceType,
			"result":        result,
		}).Inc()
	}
}

func (v *ReferenceValidator) recordDuration(resourceType string, d time.Duration) {
	if v.validationDuration != nil {
		v.validationDuration.With(prometheus.Labels{
			"resource_type": resourceType,
		}).Observe(d.Seconds())
	}
}

// resolveTenantProject determines the tenant and project for a reference lookup based on the
// reference type and fields.
func resolveTenantProject(refMsg protoreflect.Message, fullName protoreflect.FullName,
	callerTenant, callerProject string) (string, string) {
	if isLocalReference(fullName) {
		return callerTenant, callerProject
	}

	tenant := callerTenant
	project := callerProject

	sharedField := refMsg.Descriptor().Fields().ByName("shared")
	if sharedField != nil && refMsg.Get(sharedField).Bool() {
		tenant = "shared"
	}

	projectField := refMsg.Descriptor().Fields().ByName("project")
	if projectField != nil {
		explicitProject := refMsg.Get(projectField).String()
		if explicitProject != "" {
			project = explicitProject
		}
	}

	return tenant, project
}

// extractTenantProject extracts the tenant and project from the request's embedded object metadata
// using protoreflect (request → object → metadata → {tenant, project}).
func extractTenantProject(request proto.Message) (tenant, project string) {
	msg := request.ProtoReflect()

	objectField := msg.Descriptor().Fields().ByName("object")
	if objectField == nil {
		return
	}
	objectMsg := msg.Get(objectField).Message()

	metadataField := objectMsg.Descriptor().Fields().ByName("metadata")
	if metadataField == nil {
		return
	}
	metadataMsg := objectMsg.Get(metadataField).Message()

	tenantField := metadataMsg.Descriptor().Fields().ByName("tenant")
	if tenantField != nil {
		tenant = metadataMsg.Get(tenantField).String()
	}

	projectField := metadataMsg.Descriptor().Fields().ByName("project")
	if projectField != nil {
		project = metadataMsg.Get(projectField).String()
	}

	return
}

// isReferenceType checks if a message type is a reference type based on its name suffix.
func isReferenceType(fullName protoreflect.FullName) bool {
	return strings.HasSuffix(string(fullName), "Reference")
}

// isLocalReference checks if a reference type is a local reference.
func isLocalReference(fullName protoreflect.FullName) bool {
	return strings.HasSuffix(string(fullName), "LocalReference")
}

// isCreateOrUpdate checks if the gRPC method is a Create or Update operation.
func isCreateOrUpdate(method string) bool {
	return strings.HasSuffix(method, "/Create") || strings.HasSuffix(method, "/Update")
}

// isNotFoundErr checks whether an error represents a "not found" condition.
func isNotFoundErr(err error) bool {
	var nf interface{ IsNotFound() bool }
	if errors.As(err, &nf) {
		return nf.IsNotFound()
	}
	return false
}

// shortName extracts the unqualified message name from a fully-qualified protobuf name and strips
// common prefixes/suffixes for human-readable error messages.
func shortName(fullName protoreflect.FullName) string {
	name := string(fullName.Name())
	name = strings.TrimSuffix(name, "LocalReference")
	name = strings.TrimSuffix(name, "Reference")
	return name
}

// identifier returns a human-readable identifier string from id and/or name.
func identifier(id, name string) string {
	if name != "" {
		return name
	}
	if id != "" {
		return id
	}
	return "(empty reference)"
}

func isStringField(fd protoreflect.FieldDescriptor) bool {
	return fd != nil && fd.Kind() == protoreflect.StringKind
}

// registerOrReuse registers a Prometheus collector and returns it. If the collector is already
// registered, it returns the existing one.
func registerOrReuse[C prometheus.Collector](registerer prometheus.Registerer, c C) (C, error) {
	err := registerer.Register(c)
	if err != nil {
		var registered prometheus.AlreadyRegisteredError
		if errors.As(err, &registered) {
			existing, ok := registered.ExistingCollector.(C)
			if !ok {
				var zero C
				return zero, fmt.Errorf("existing collector has unexpected type %T", registered.ExistingCollector)
			}
			return existing, nil
		}
		var zero C
		return zero, err
	}
	return c, nil
}
