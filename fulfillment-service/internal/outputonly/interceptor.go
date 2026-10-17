/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

// Package outputonly provides the gRPC interceptor that keeps output-only fields out of
// user-initiated updates. The resource contract lives in the proto files: a field annotated
// with google.api.field_behavior = OUTPUT_ONLY is written by the system and can only be read
// by users. The interceptor enforces that contract for update masks on the public API, so
// every public Update method is covered without per-server code.
package outputonly

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/masks"
)

const (
	// publicMethodPrefix matches the methods of the public API. The private API is excluded on
	// purpose: the fulfillment controller and the operator feedback write status through it.
	publicMethodPrefix = "/osac.public.v1."

	// updateMethodSuffix matches the update methods, the only ones whose request carries an
	// update mask.
	updateMethodSuffix = "/Update"
)

// InterceptorBuilder contains the data and logic needed to build the interceptor. Don't create
// instances of this type directly, use the NewInterceptor function instead.
type InterceptorBuilder struct {
	logger *slog.Logger
}

// Interceptor is a gRPC interceptor that rejects public API updates whose mask touches a field
// annotated as OUTPUT_ONLY.
type Interceptor struct {
	logger *slog.Logger
}

// NewInterceptor creates a builder that can then be used to configure and create the interceptor.
func NewInterceptor() *InterceptorBuilder {
	return &InterceptorBuilder{}
}

// SetLogger sets the logger that will be used to write to the log. This is mandatory.
func (b *InterceptorBuilder) SetLogger(value *slog.Logger) *InterceptorBuilder {
	b.logger = value
	return b
}

// Build uses the information stored in the builder to create the interceptor.
func (b *InterceptorBuilder) Build() (result *Interceptor, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}

	// Create and populate the object:
	result = &Interceptor{
		logger: b.logger,
	}
	return
}

// UnaryServer is the unary server interceptor function. Update requests are unary, so no stream
// variant is needed.
func (i *Interceptor) UnaryServer(ctx context.Context, request any, info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler) (response any, err error) {
	if !isPublicUpdate(info.FullMethod) {
		return handler(ctx, request)
	}
	err = i.checkUpdateMask(ctx, request)
	if err != nil {
		return
	}
	return handler(ctx, request)
}

// isPublicUpdate reports whether the method is an update of the public API.
func isPublicUpdate(method string) bool {
	return strings.HasPrefix(method, publicMethodPrefix) && strings.HasSuffix(method, updateMethodSuffix)
}

// checkUpdateMask rejects the request when its update mask touches a field annotated as
// OUTPUT_ONLY. Masks that name fields the interceptor doesn't recognize are passed through:
// the resource server's own mask handling rejects invalid paths.
func (i *Interceptor) checkUpdateMask(ctx context.Context, request any) error {
	message, ok := request.(proto.Message)
	if !ok {
		return nil
	}
	value := message.ProtoReflect()
	mask := fieldMaskOf(value)
	if mask == nil || len(mask.GetPaths()) == 0 {
		return nil
	}
	desc := objectDescriptorOf(value)
	if desc == nil {
		return nil
	}
	for _, path := range mask.GetPaths() {
		field, outputOnly := outputOnlyPath(desc, path)
		if !outputOnly {
			continue
		}
		i.logger.DebugContext(ctx, "Rejected update mask touching an output-only field",
			"method", protoreflect.FullName(value.Descriptor().FullName()),
			"path", path,
			"field", field.FullName(),
		)
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"update_mask path '%s' touches the output-only field '%s'", path, field.FullName())
	}
	return nil
}

// fieldMaskOf extracts the update mask of the request message, if it has one.
func fieldMaskOf(value protoreflect.Message) *fieldmaskpb.FieldMask {
	field := value.Descriptor().Fields().ByName("update_mask")
	if field == nil || field.Kind() != protoreflect.MessageKind ||
		field.Message().FullName() != "google.protobuf.FieldMask" {
		return nil
	}
	if !value.Has(field) {
		return nil
	}
	mask, ok := value.Get(field).Message().Interface().(*fieldmaskpb.FieldMask)
	if !ok {
		return nil
	}
	return mask
}

// objectDescriptorOf returns the descriptor of the resource object the update mask applies to.
func objectDescriptorOf(value protoreflect.Message) protoreflect.MessageDescriptor {
	field := value.Descriptor().Fields().ByName("object")
	if field == nil || field.Kind() != protoreflect.MessageKind {
		return nil
	}
	return field.Message()
}

// outputOnlyPath walks the mask path over the resource descriptor and reports the first field
// it touches that is annotated as OUTPUT_ONLY. Segments are normalized exactly like the
// server's path compiler (masks.SplitPath: trimmed, empty chunks dropped) and are matched by
// proto name and by JSON name, so both snake_case and camelCase masks are covered. Map fields
// are followed by their key segment and list fields by their index segment, mirroring the
// compiler, so annotated fields nested in maps and lists are covered too.
func outputOnlyPath(desc protoreflect.MessageDescriptor, path string) (protoreflect.FieldDescriptor, bool) {
	segments := masks.SplitPath(path)
	current := desc
	for i := 0; i < len(segments); i++ {
		field := current.Fields().ByName(protoreflect.Name(segments[i]))
		if field == nil {
			field = current.Fields().ByJSONName(segments[i])
		}
		if field == nil {
			return nil, false
		}
		if hasOutputOnlyBehavior(field) {
			return field, true
		}
		switch {
		case field.IsMap():
			value := field.MapValue()
			if value.Kind() != protoreflect.MessageKind {
				return nil, false
			}
			i++ // the segment after a map field is its key, not a field
			current = value.Message()
		case field.IsList():
			if field.Kind() != protoreflect.MessageKind {
				return nil, false
			}
			if i+1 < len(segments) {
				if _, err := strconv.Atoi(segments[i+1]); err != nil {
					return nil, false // invalid index: the server rejects the path
				}
				i++
			}
			current = field.Message()
		default:
			if field.Kind() != protoreflect.MessageKind {
				return nil, false
			}
			current = field.Message()
		}
	}
	return nil, false
}

// hasOutputOnlyBehavior reports whether the field is annotated as OUTPUT_ONLY.
func hasOutputOnlyBehavior(field protoreflect.FieldDescriptor) bool {
	behaviors, ok := proto.GetExtension(field.Options(), annotations.E_FieldBehavior).([]annotations.FieldBehavior)
	if !ok {
		return false
	}
	for _, behavior := range behaviors {
		if behavior == annotations.FieldBehavior_OUTPUT_ONLY {
			return true
		}
	}
	return false
}
