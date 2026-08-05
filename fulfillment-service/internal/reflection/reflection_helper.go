/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package reflection

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/gobuffalo/flect"
	"golang.org/x/exp/maps"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	// This is needed to ensure that the types and services are loaded into the protocol buffers registry, otherwise
	// they will be visible only if they are explicitly used in some part of the code.
	_ "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	_ "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
)

// Helper simplifies use of the protocol buffers reflection facility. It knows how to extract from the descriptors the
// list of message types that satisfy the conditions to be considered objects, as well as the services that support them
// and the methods to get, list, update and delete instances.
//
//go:generate mockgen -destination=reflection_helper_mock.go -package=reflection . Helper
type Helper interface {
	// Names returns the full names of the object types. The results are sorted by the order of the packages, and
	// alphabetically within each package.
	Names() []string

	// Singulars returns the object types in singular. The results are in lower case and sorted alphabetically.
	Singulars() []string

	// Plurals returns the object types in plural. The results are in lower case and sorted alphabetically.
	Plurals() []string

	// Lookup returns the helper for the given object type. Returns nil if there is no such object.
	Lookup(objectType string) ObjectHelper
}

// ObjectHelper contains information about a message type that satisfies the conditions to be considered an object.
//
//go:generate mockgen -destination=reflection_object_helper_mock.go -package=reflection . ObjectHelper
type ObjectHelper interface {
	Descriptor() protoreflect.MessageDescriptor
	Instance() proto.Message
	FullName() protoreflect.FullName
	String() string
	Singular() string
	Plural() string
	List(ctx context.Context, options ListOptions) (ListResult, error)
	Get(ctx context.Context, id string) (proto.Message, error)
	GetId(object proto.Message) string
	GetName(object proto.Message) string
	GetMetadata(object proto.Message) Metadata
	Create(ctx context.Context, object proto.Message) (proto.Message, error)
	Update(ctx context.Context, object proto.Message) (proto.Message, error)
	Delete(ctx context.Context, id string) error
	FindObject(ctx context.Context, ref string, console Renderer) (proto.Message, error)
	SetTenant(object proto.Message, tenant string)
	GetTenant(object proto.Message) string
	IsTenantScoped() bool
}

// HelperBuilder contains the data and logic needed to create a reflection helper.
//
// Don't create instances of this type directly, use the NewHelper function instead.
type HelperBuilder struct {
	logger     *slog.Logger
	connection *grpc.ClientConn
	packages   map[string]int
	tenantFunc any
}

// helper is the default implementation of the Helper interface.
type helper struct {
	logger     *slog.Logger
	connection *grpc.ClientConn
	packages   map[protoreflect.FullName]int
	scanOnce   *sync.Once
	helpers    []objectHelper
	tenantFunc func(context.Context) (string, error)
}

// NewHelper creates a builder that can then be used to configure a reflection helper.
func NewHelper() *HelperBuilder {
	return &HelperBuilder{}
}

// SetLogger sets the logger. This is mandatory.
func (b *HelperBuilder) SetLogger(value *slog.Logger) *HelperBuilder {
	b.logger = value
	return b
}

// SetConnection sets the gRPC connection that will be used to invoke mehods. This is mandatory.
func (b *HelperBuilder) SetConnection(value *grpc.ClientConn) *HelperBuilder {
	b.connection = value
	return b
}

// AddPackage adds a protobuf package that will be scanned looking for types and services. The order parameter is
// used to specify the relative order of the package when presented to the user.
func (b *HelperBuilder) AddPackage(name string, order int) *HelperBuilder {
	if b.packages == nil {
		b.packages = make(map[string]int)
	}
	b.packages[name] = order
	return b
}

// AddPackages adds a map of protobuf packages that will be scanned looking for types and services. The key of the map
// is the name of the package and the value is the relative order of the package when presented to the user.
func (b *HelperBuilder) AddPackages(values map[string]int) *HelperBuilder {
	if b.packages == nil {
		b.packages = make(map[string]int)
	}
	for name, order := range values {
		b.packages[name] = order
	}
	return b
}

// SetTenantFunc sets the function that returns the tenant. When this is set the helper will call the function to obtain
// the tenant, and if it isn't empty, it will use it to automatically filter the results by tenant, as well as to set
// automatically the tenant on new objects. The function can be of the following types:
//
//   - func() string
//   - func(context.Context) string
//   - func() (string, error)
//   - func(context.Context) (string, error)
//
// The Build method will verify that the function matches one of these signatures.
func (b *HelperBuilder) SetTenantFunc(value any) *HelperBuilder {
	b.tenantFunc = value
	return b
}

// Build uses the data stored in the builder to create a new reflection helper.
func (b *HelperBuilder) Build() (result Helper, err error) {
	// Check the parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.connection == nil {
		err = errors.New("gRPC connection is mandatory")
		return
	}
	if len(b.packages) == 0 {
		err = errors.New("at least one package is mandatory")
		return
	}

	// Normalize the tenant function to a canonical signature:
	var tenantFunc func(context.Context) (string, error)
	if b.tenantFunc != nil {
		tenantFunc, err = NormalizeFunc[string](b.tenantFunc)
		if err != nil {
			return
		}
	}

	// Prepare the set of packages:
	packages := make(map[protoreflect.FullName]int, len(b.packages))
	for name, order := range b.packages {
		packages[protoreflect.FullName(name)] = order
	}

	// Create and populate the object:
	result = &helper{
		logger:     b.logger,
		packages:   packages,
		connection: b.connection,
		scanOnce:   &sync.Once{},
		helpers:    []objectHelper{},
		tenantFunc: tenantFunc,
	}
	return
}

func (h *helper) scanIfNeeded() {
	h.scanOnce.Do(func() {
		h.scan()
	})
}

func (h *helper) scan() {
	protoregistry.GlobalFiles.RangeFiles(h.scanFile)
	sort.Slice(
		h.helpers,
		func(i, j int) bool {
			helperI, helperJ := h.helpers[i], h.helpers[j]
			nameI, nameJ := helperI.descriptor.FullName(), helperJ.descriptor.FullName()
			pkgI, pkgJ := nameI.Parent(), nameJ.Parent()
			orderI, orderJ := h.packages[pkgI], h.packages[pkgJ]
			if orderI != orderJ {
				return orderI < orderJ
			}
			return nameI < nameJ
		},
	)
}

func (h *helper) scanFile(fileDesc protoreflect.FileDescriptor) bool {
	_, ok := h.packages[fileDesc.Package()]
	if !ok {
		h.logger.Debug(
			"Ignoring file because it isn't in the list of enabled packages",
			slog.String("file", fileDesc.Path()),
			slog.String("package", string(fileDesc.Package())),
		)
		return true
	}
	h.logger.Debug(
		"Scanning file",
		slog.String("file", fileDesc.Path()),
	)
	serviceDescs := fileDesc.Services()
	for i := range serviceDescs.Len() {
		h.scanService(serviceDescs.Get(i))
	}
	return true
}

func (h *helper) scanService(serviceDesc protoreflect.ServiceDescriptor) {
	// The service must have the get, list, update and delete method:
	h.logger.Debug(
		"Scanning service",
		slog.String("service", string(serviceDesc.FullName())),
	)
	methodDescs := serviceDesc.Methods()
	listDesc := methodDescs.ByName(listMethodName)
	if listDesc == nil {
		return
	}
	getDesc := methodDescs.ByName(getMethodName)
	if getDesc == nil {
		return
	}
	createDesc := methodDescs.ByName(createMethodName)
	if createDesc == nil {
		return
	}
	updateDesc := methodDescs.ByName(updateMethodName)
	if updateDesc == nil {
		return
	}
	deleteDesc := methodDescs.ByName(deleteMethodName)
	if deleteDesc == nil {
		return
	}

	// The request of the get method must have an `id` field:
	getRequestIdFieldDesc := h.getIdField(getDesc.Input())
	if getRequestIdFieldDesc == nil {
		return
	}

	// The response of the get method must have an `object` field:
	getResponseObjectFieldDesc := h.getObjectField(getDesc.Output())
	objectDesc := getResponseObjectFieldDesc.Message()

	// The request of the list method must have a `filter` field:
	listRequestFilterFieldDesc := h.getFilterField(listDesc.Input())
	if listRequestFilterFieldDesc == nil {
		return
	}

	// The request of the list method may have a `limit` field:
	listRequestLimitFieldDesc := h.getLimitField(listDesc.Input())

	// The response of the list method must have an `items` field:
	listResponseItemsFieldDesc := h.getItemsField(listDesc.Output())
	if listResponseItemsFieldDesc == nil {
		return
	}
	if listResponseItemsFieldDesc.Message() != objectDesc {
		return
	}

	// The response of the list method may have a `total` field:
	listResponseTotalFieldDesc := h.getTotalField(listDesc.Output())

	// The request and response of the `Crate` method must have an `object` message field:
	createRequestObjectFieldDesc := h.getObjectField(createDesc.Input())
	if createRequestObjectFieldDesc == nil {
		return
	}
	if createRequestObjectFieldDesc.Message() != objectDesc {
		return
	}
	createResponseObjectFieldDesc := h.getObjectField(createDesc.Output())
	if createResponseObjectFieldDesc == nil {
		return
	}
	if createResponseObjectFieldDesc.Message() != objectDesc {
		return
	}

	// The request and response of the `Update` method must have an `object` message field:
	updateRequestObjectFieldDesc := h.getObjectField(updateDesc.Input())
	if updateRequestObjectFieldDesc == nil {
		return
	}
	if updateRequestObjectFieldDesc.Message() != objectDesc {
		return
	}
	updateResponseObjectFieldDesc := h.getObjectField(updateDesc.Output())
	if updateResponseObjectFieldDesc == nil {
		return
	}
	if updateResponseObjectFieldDesc.Message() != objectDesc {
		return
	}

	// The request of the `Delete` method must have an `id` string field:
	deleteRequestIdFieldDesc := h.getIdField(deleteDesc.Input())
	if deleteRequestIdFieldDesc == nil {
		return
	}

	// Create the object template:
	objectTemplate := h.makeTemplate(objectDesc)

	// Create templates for the request and response messages:
	getRequestTemplate, getResponseTemplate := h.makeMethodTemplates(getDesc)
	listRequestTemplate, listResponseTemplate := h.makeMethodTemplates(listDesc)
	createRequestTemplate, createResponseTemplate := h.makeMethodTemplates(createDesc)
	updateRequestTemplate, updateResponseTemplate := h.makeMethodTemplates(updateDesc)
	deleteRequestTemplate, deleteResponseTemplate := h.makeMethodTemplates(deleteDesc)

	// Calculate the singular and pluran names:
	objectName := string(objectDesc.Name())
	objectNameSingular := strings.ToLower(objectName)
	objectNamePlural := strings.ToLower(flect.Pluralize(objectName))

	// Get the descriptors of the fields of the object:
	objectFields := objectDesc.Fields()
	idFieldDesc := objectFields.ByName(idFieldName)
	metadataFieldDesc := objectFields.ByName(metadataFieldName)

	// This is a supported object type:
	helper := objectHelper{
		parent:        h,
		descriptor:    objectDesc,
		idField:       idFieldDesc,
		metadataField: metadataFieldDesc,
		singular:      objectNameSingular,
		plural:        objectNamePlural,
		tenantScoped:  !platformScopedTypes[objectDesc.Name()],
		template:      objectTemplate,
		get: getInfo{
			methodInfo: methodInfo{
				path:     h.makeMethodPath(getDesc),
				request:  getRequestTemplate,
				response: getResponseTemplate,
			},
			id:     getRequestIdFieldDesc,
			object: getResponseObjectFieldDesc,
		},
		list: listInfo{
			methodInfo: methodInfo{
				path:     h.makeMethodPath(listDesc),
				request:  listRequestTemplate,
				response: listResponseTemplate,
			},
			filter: listRequestFilterFieldDesc,
			limit:  listRequestLimitFieldDesc,
			items:  listResponseItemsFieldDesc,
			total:  listResponseTotalFieldDesc,
		},
		create: createInfo{
			methodInfo: methodInfo{
				path:     h.makeMethodPath(createDesc),
				request:  createRequestTemplate,
				response: createResponseTemplate,
			},
			in:  createRequestObjectFieldDesc,
			out: createResponseObjectFieldDesc,
		},
		update: updateInfo{
			methodInfo: methodInfo{
				path:     h.makeMethodPath(updateDesc),
				request:  updateRequestTemplate,
				response: updateResponseTemplate,
			},
			in:  updateRequestObjectFieldDesc,
			out: updateResponseObjectFieldDesc,
		},
		delete: deleteInfo{
			methodInfo: methodInfo{
				path:     h.makeMethodPath(deleteDesc),
				request:  deleteRequestTemplate,
				response: deleteResponseTemplate,
			},
			id: deleteRequestIdFieldDesc,
		},
	}
	h.helpers = append(h.helpers, helper)
}

func (h *helper) getIdField(messageDesc protoreflect.MessageDescriptor) protoreflect.FieldDescriptor {
	fieldDesc := messageDesc.Fields().ByName(idFieldName)
	if fieldDesc == nil {
		return nil
	}
	if fieldDesc.Cardinality() != protoreflect.Optional {
		return nil
	}
	if fieldDesc.Kind() != protoreflect.StringKind {
		return nil
	}
	return fieldDesc
}

func (h *helper) getObjectField(messageDesc protoreflect.MessageDescriptor) protoreflect.FieldDescriptor {
	fieldDesc := messageDesc.Fields().ByName(objectFieldName)
	if fieldDesc == nil {
		return nil
	}
	if fieldDesc.Cardinality() != protoreflect.Optional {
		return nil
	}
	if fieldDesc.Kind() != protoreflect.MessageKind {
		return nil
	}
	return fieldDesc
}

func (h *helper) getFilterField(messageDesc protoreflect.MessageDescriptor) protoreflect.FieldDescriptor {
	fieldDesc := messageDesc.Fields().ByName(filterFieldName)
	if fieldDesc == nil {
		return nil
	}
	if fieldDesc.Cardinality() == protoreflect.Repeated {
		return nil
	}
	if fieldDesc.Kind() != protoreflect.StringKind {
		return nil
	}
	return fieldDesc
}

func (h *helper) getLimitField(messageDesc protoreflect.MessageDescriptor) protoreflect.FieldDescriptor {
	fieldDesc := messageDesc.Fields().ByName(limitFieldName)
	if fieldDesc == nil {
		return nil
	}
	if fieldDesc.Cardinality() == protoreflect.Repeated {
		return nil
	}
	if fieldDesc.Kind() != protoreflect.Int32Kind {
		return nil
	}
	return fieldDesc
}

func (h *helper) getItemsField(messageDesc protoreflect.MessageDescriptor) protoreflect.FieldDescriptor {
	fieldDesc := messageDesc.Fields().ByName(itemsFieldName)
	if fieldDesc == nil {
		return nil
	}
	if fieldDesc.Cardinality() != protoreflect.Repeated {
		return nil
	}
	if fieldDesc.Kind() != protoreflect.MessageKind {
		return nil
	}
	return fieldDesc
}

func (h *helper) getTotalField(messageDesc protoreflect.MessageDescriptor) protoreflect.FieldDescriptor {
	fieldDesc := messageDesc.Fields().ByName(totalFieldName)
	if fieldDesc == nil {
		return nil
	}
	if fieldDesc.Cardinality() == protoreflect.Repeated {
		return nil
	}
	if fieldDesc.Kind() != protoreflect.Int32Kind {
		return nil
	}
	return fieldDesc
}

func (h *helper) Names() []string {
	h.scanIfNeeded()
	results := make([]string, len(h.helpers))
	for i, objectInfo := range h.helpers {
		results[i] = string(objectInfo.descriptor.FullName())
	}
	return results
}

func (h *helper) Singulars() []string {
	h.scanIfNeeded()
	set := make(map[string]bool, len(h.helpers))
	for _, objectInfo := range h.helpers {
		set[objectInfo.singular] = true
	}
	results := maps.Keys(set)
	sort.Strings(results)
	return results
}

func (h *helper) Plurals() []string {
	h.scanIfNeeded()
	set := make(map[string]bool, len(h.helpers))
	for _, objectInfo := range h.helpers {
		set[objectInfo.plural] = true
	}
	results := maps.Keys(set)
	sort.Strings(results)
	return results
}

func (h *helper) Lookup(objectType string) ObjectHelper {
	h.scanIfNeeded()
	for _, objectInfo := range h.helpers {
		if objectType == string(objectInfo.descriptor.FullName()) {
			return &objectInfo
		}
		if strings.EqualFold(objectType, objectInfo.singular) {
			return &objectInfo
		}
		if strings.EqualFold(objectType, objectInfo.plural) {
			return &objectInfo
		}
	}
	return nil
}

func (h *helper) makeMethodPath(methodDesc protoreflect.MethodDescriptor) string {
	return fmt.Sprintf("/%s/%s", methodDesc.FullName().Parent(), methodDesc.Name())
}

func (h *helper) makeMethodTemplates(methodDesc protoreflect.MethodDescriptor) (requestTemplate,
	responseTemplate proto.Message) {
	requestTemplate = h.makeTemplate(methodDesc.Input())
	responseTemplate = h.makeTemplate(methodDesc.Output())
	return
}

func (h *helper) makeTemplate(messageDesc protoreflect.MessageDescriptor) proto.Message {
	messageType, err := protoregistry.GlobalTypes.FindMessageByName(messageDesc.FullName())
	if err != nil {
		panic(err)
	}
	return messageType.New().Interface()
}

// objectHelper is the default implementation of the ObjectHelper interface.
type objectHelper struct {
	parent        *helper
	descriptor    protoreflect.MessageDescriptor
	singular      string
	plural        string
	tenantScoped  bool
	template      proto.Message
	list          listInfo
	get           getInfo
	create        createInfo
	update        updateInfo
	delete        deleteInfo
	idField       protoreflect.FieldDescriptor
	metadataField protoreflect.FieldDescriptor
}

type methodInfo struct {
	path     string
	request  proto.Message
	response proto.Message
}

type getInfo struct {
	methodInfo
	id     protoreflect.FieldDescriptor
	object protoreflect.FieldDescriptor
}

type listInfo struct {
	methodInfo
	filter protoreflect.FieldDescriptor
	limit  protoreflect.FieldDescriptor
	items  protoreflect.FieldDescriptor
	total  protoreflect.FieldDescriptor
}

type createInfo struct {
	methodInfo
	in  protoreflect.FieldDescriptor
	out protoreflect.FieldDescriptor
}

type updateInfo struct {
	methodInfo
	in  protoreflect.FieldDescriptor
	out protoreflect.FieldDescriptor
}

type deleteInfo struct {
	methodInfo
	id protoreflect.FieldDescriptor
}

func (h *objectHelper) Descriptor() protoreflect.MessageDescriptor {
	return h.descriptor
}

func (h *objectHelper) Instance() proto.Message {
	return proto.Clone(h.template)
}

func (h *objectHelper) FullName() protoreflect.FullName {
	return h.descriptor.FullName()
}

func (h *objectHelper) String() string {
	return string(h.descriptor.FullName())
}

func (h *objectHelper) Singular() string {
	return h.singular
}

func (h *objectHelper) Plural() string {
	return h.plural
}

type ListOptions struct {
	Filter string
	Limit  int32
}

type ListResult struct {
	Items []proto.Message
	Total int32
}

func (h *objectHelper) List(ctx context.Context, options ListOptions) (result ListResult, err error) {
	filter := options.Filter

	// Inject tenant filter from tenant function if needed:
	if h.IsTenantScoped() && h.parent.tenantFunc != nil {
		var tenant string
		tenant, err = h.parent.tenantFunc(ctx)
		if err != nil {
			return
		}
		if tenant != "" {
			tenantFilter := fmt.Sprintf("this.metadata.tenant == %q", tenant)
			if filter != "" {
				filter = fmt.Sprintf("%s && (%s)", tenantFilter, filter)
			} else {
				filter = tenantFilter
			}
		}
	}

	request := proto.Clone(h.list.request)
	if filter != "" {
		request.ProtoReflect().Set(h.list.filter, protoreflect.ValueOfString(filter))
	}
	if options.Limit > 0 && h.list.limit != nil {
		request.ProtoReflect().Set(h.list.limit, protoreflect.ValueOfInt32(options.Limit))
	}
	response := proto.Clone(h.list.response)
	err = h.parent.connection.Invoke(ctx, h.list.path, request, response)
	if err != nil {
		return
	}
	list := response.ProtoReflect().Get(h.list.items).List()
	result.Items = make([]proto.Message, list.Len())
	for i := range list.Len() {
		result.Items[i] = list.Get(i).Message().Interface()
	}
	if h.list.total != nil {
		result.Total = int32(response.ProtoReflect().Get(h.list.total).Int()) // #nosec G115 -- proto int32 field
	} else {
		result.Total = int32(len(result.Items)) // #nosec G115 -- bounded by MaxLimit
	}
	return
}

func (h *objectHelper) Get(ctx context.Context, id string) (result proto.Message, err error) {
	request := proto.Clone(h.get.request)
	h.setId(request, h.get.id, id)
	response := proto.Clone(h.get.response)
	err = h.parent.connection.Invoke(ctx, h.get.path, request, response)
	if err != nil {
		return
	}
	result = h.getObject(response, h.get.object)
	return
}

func (h *objectHelper) GetId(object proto.Message) string {
	return object.ProtoReflect().Get(h.idField).String()
}

func (h *objectHelper) GetName(object proto.Message) string {
	return h.GetMetadata(object).GetName()
}

func (h *objectHelper) GetMetadata(object proto.Message) Metadata {
	return object.ProtoReflect().Get(h.metadataField).Message().Interface().(Metadata)
}

func (h *objectHelper) Create(ctx context.Context, object proto.Message) (result proto.Message, err error) {
	request := proto.Clone(h.create.request)
	h.setObject(request, h.create.in, object)
	response := proto.Clone(h.create.response)
	err = h.parent.connection.Invoke(ctx, h.create.path, request, response)
	if err != nil {
		err = fmt.Errorf("failed to create object: %w", err)
	}
	result = h.getObject(response, h.create.out)
	return
}

func (h *objectHelper) Update(ctx context.Context, object proto.Message) (result proto.Message, err error) {
	request := proto.Clone(h.update.request)
	h.setObject(request, h.update.in, object)
	response := proto.Clone(h.update.response)
	err = h.parent.connection.Invoke(ctx, h.update.path, request, response)
	if err != nil {
		err = fmt.Errorf("failed to update object: %w", err)
	}
	result = h.getObject(response, h.update.out)
	return
}

func (h *objectHelper) Delete(ctx context.Context, id string) error {
	request := proto.Clone(h.delete.request)
	h.setId(request, h.delete.id, id)
	response := proto.Clone(h.delete.response)
	return h.parent.connection.Invoke(ctx, h.delete.path, request, response)
}

// tenantFieldName is the name of the tenant field in the metadata message.
const tenantFieldName = protoreflect.Name("tenant")

// SetTenant sets the tenant field on the object's metadata. If the metadata submessage does not
// exist it is created.
func (h *objectHelper) SetTenant(object proto.Message, tenant string) {
	metadata := object.ProtoReflect().Mutable(h.metadataField).Message()
	field := metadata.Descriptor().Fields().ByName(tenantFieldName)
	if field != nil {
		metadata.Set(field, protoreflect.ValueOfString(tenant))
	}
}

// GetTenant returns the tenant field from the object's metadata.
func (h *objectHelper) GetTenant(object proto.Message) string {
	metadata := object.ProtoReflect().Get(h.metadataField).Message()
	field := metadata.Descriptor().Fields().ByName(tenantFieldName)
	if field == nil {
		return ""
	}
	return metadata.Get(field).String()
}

// IsTenantScoped returns true if this resource type is scoped to a tenant.
func (h *objectHelper) IsTenantScoped() bool {
	return h.tenantScoped
}

func (h *objectHelper) setId(message proto.Message, field protoreflect.FieldDescriptor, value string) {
	message.ProtoReflect().Set(field, protoreflect.ValueOfString(value))
}

func (h *objectHelper) setObject(message proto.Message, field protoreflect.FieldDescriptor, value proto.Message) {
	message.ProtoReflect().Set(field, protoreflect.ValueOfMessage(value.ProtoReflect()))
}

func (h *objectHelper) getObject(message proto.Message, field protoreflect.FieldDescriptor) proto.Message {
	return message.ProtoReflect().Get(field).Message().Interface()
}

// Frequently used names:
const (
	// Methods:
	createMethodName = protoreflect.Name("Create")
	deleteMethodName = protoreflect.Name("Delete")
	getMethodName    = protoreflect.Name("Get")
	listMethodName   = protoreflect.Name("List")
	updateMethodName = protoreflect.Name("Update")

	// Fields:
	filterFieldName   = protoreflect.Name("filter")
	idFieldName       = protoreflect.Name("id")
	itemsFieldName    = protoreflect.Name("items")
	limitFieldName    = protoreflect.Name("limit")
	metadataFieldName = protoreflect.Name("metadata")
	objectFieldName   = protoreflect.Name("object")
	totalFieldName    = protoreflect.Name("total")
)

// platformScopedTypes lists the resource types that are NOT scoped to a tenant. Types not listed
// here default to tenant-scoped, which means they get automatic tenant filter injection on List,
// automatic tenant field setting on generic Create, and the TENANT column in table output. Keyed
// by short message name so that public and private API variants are handled automatically.
var platformScopedTypes = map[protoreflect.Name]bool{
	"Capabilities":     true,
	"ClusterVersion":   true,
	"ConsoleSession":   true,
	"ExternalIPPool":   true,
	"HostType":         true,
	"Hub":              true,
	"IdentityProvider": true,
	"InstanceType":     true,
	"NetworkClass":     true,
	"Role":             true,
	"StorageBackend":   true,
	"StorageTier":      true,
	"Tenant":           true,
	"User":             true,
}
