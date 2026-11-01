/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package it

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"k8s.io/client-go/util/retry"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type metadataMethodCapabilities struct {
	PrivateCreate bool
	PrivateGet    bool
	PrivateList   bool
	PrivateUpdate bool
	PrivateDelete bool
	PublicCreate  bool
	PublicGet     bool
	PublicList    bool
	PublicUpdate  bool
	PublicDelete  bool
}

type metadataTestIdentity struct {
	Conn       *grpc.ClientConn
	HTTPClient *http.Client
	TenantID   string
	Username   string
	Password   string
}

type metadataTestAccounts struct {
	adminPrivate     *grpc.ClientConn
	tenantAdmin      metadataTestIdentity
	tenantUser       metadataTestIdentity
	secondTenantUser metadataTestIdentity
}

// metadataResourceServiceNames is the persistent-resource matrix split across
// the two metadata implementation stories. Keeping service names alongside
// the expected method capabilities makes additions visible to the unit test
// matrix without coupling the test helpers to generated client interfaces.
var metadataResourceServiceNames = []string{
	"AddOnOperators",
	"BareMetalInstanceCatalogItems",
	"BareMetalInstanceTemplates",
	"BareMetalInstanceTypes",
	"BareMetalInstances",
	"ClusterCatalogItems",
	"ClusterTemplates",
	"ClusterVersions",
	"Clusters",
	"ComputeInstanceCatalogItems",
	"ComputeInstanceTemplates",
	"ComputeInstances",
	"DiskImages",
	"Hubs",
	"IdentityProviders",
	"InstanceTypes",
	"ProjectMemberships",
	"Projects",
	"RoleBindings",
	"Roles",
	"Secrets",
	"StorageBackends",
	"StorageTiers",
	"Tenants",
	"Users",
	"Volumes",
}

type metadataServiceDescriptors struct {
	private protoreflect.ServiceDescriptor
	public  protoreflect.ServiceDescriptor
}

// metadataServices finds both API views for a resource service. Some resources
// intentionally have no public service; callers use a nil public descriptor
// to assert the existing method boundary.
func metadataServices(name string) (metadataServiceDescriptors, error) {
	find := func(prefix string) (protoreflect.ServiceDescriptor, error) {
		descriptor, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(prefix + name))
		if err != nil {
			return nil, err
		}
		service, ok := descriptor.(protoreflect.ServiceDescriptor)
		if !ok {
			return nil, fmt.Errorf("descriptor %q is %T, not a service", prefix+name, descriptor)
		}
		return service, nil
	}

	private, err := find("osac.private.v1.")
	if err != nil {
		return metadataServiceDescriptors{}, fmt.Errorf("find private %s service: %w", name, err)
	}
	public, err := find("osac.public.v1.")
	if err != nil {
		return metadataServiceDescriptors{private: private}, nil
	}
	return metadataServiceDescriptors{private: private, public: public}, nil
}

func metadataRPCMethodName(service protoreflect.ServiceDescriptor, method protoreflect.Name) string {
	return "/" + string(service.FullName()) + "/" + string(method)
}

// privateMetadataSnapshot performs a descriptor-driven private Get and
// returns the complete object message, including private metadata fields.
func privateMetadataSnapshot(
	ctx context.Context,
	conn grpc.ClientConnInterface,
	service protoreflect.ServiceDescriptor,
	id string,
) (*dynamicpb.Message, error) {
	return metadataObjectSnapshot(ctx, conn, service, id)
}

func metadataObjectSnapshot(
	ctx context.Context,
	conn grpc.ClientConnInterface,
	service protoreflect.ServiceDescriptor,
	id string,
) (*dynamicpb.Message, error) {
	if conn == nil {
		return nil, fmt.Errorf("metadata gRPC connection is required")
	}
	if service == nil || id == "" {
		return nil, fmt.Errorf("metadata service and object ID are required")
	}
	method := service.Methods().ByName("Get")
	if method == nil {
		return nil, fmt.Errorf("metadata service %q has no Get method", service.FullName())
	}
	request := dynamicpb.NewMessage(method.Input())
	idField := method.Input().Fields().ByName("id")
	if idField == nil || idField.Kind() != protoreflect.StringKind {
		return nil, fmt.Errorf("private metadata Get request %q has no string id field", method.Input().FullName())
	}
	request.Set(idField, protoreflect.ValueOfString(id))
	response := dynamicpb.NewMessage(method.Output())
	if err := conn.Invoke(ctx, metadataRPCMethodName(service, method.Name()), request, response); err != nil {
		return nil, fmt.Errorf("metadata Get %q: %w", service.FullName(), err)
	}
	objectField := method.Output().Fields().ByName("object")
	if objectField == nil || !response.Has(objectField) {
		return nil, fmt.Errorf("metadata Get %q returned no object", service.FullName())
	}
	object := response.Get(objectField).Message()
	if object == nil || !object.IsValid() {
		return nil, fmt.Errorf("metadata Get %q returned an invalid object", service.FullName())
	}
	metadataField := object.Descriptor().Fields().ByName("metadata")
	if metadataField == nil || !object.Has(metadataField) {
		return nil, fmt.Errorf("metadata Get %q returned an object without metadata", service.FullName())
	}
	return proto.Clone(object.Interface()).(*dynamicpb.Message), nil
}

func metadataList(
	ctx context.Context,
	conn grpc.ClientConnInterface,
	service protoreflect.ServiceDescriptor,
	filter string,
) (*dynamicpb.Message, error) {
	if conn == nil {
		return nil, fmt.Errorf("metadata gRPC connection is required")
	}
	if service == nil {
		return nil, fmt.Errorf("metadata service is required")
	}
	method := service.Methods().ByName("List")
	if method == nil {
		return nil, fmt.Errorf("metadata service %q has no List method", service.FullName())
	}
	filterField := method.Input().Fields().ByName("filter")
	if filterField == nil || filterField.Kind() != protoreflect.StringKind {
		return nil, fmt.Errorf("metadata List request %q has no string filter field", method.Input().FullName())
	}
	request := dynamicpb.NewMessage(method.Input())
	request.Set(filterField, protoreflect.ValueOfString(filter))
	response := dynamicpb.NewMessage(method.Output())
	if err := conn.Invoke(ctx, metadataRPCMethodName(service, method.Name()), request, response); err != nil {
		return nil, fmt.Errorf("metadata List %q: %w", service.FullName(), err)
	}
	return response, nil
}

func metadataListObjects(response *dynamicpb.Message) ([]*dynamicpb.Message, int32, error) {
	if response == nil {
		return nil, 0, fmt.Errorf("metadata List response is required")
	}
	fields := response.Descriptor().Fields()
	itemsField := fields.ByName("items")
	if itemsField == nil || !itemsField.IsList() || itemsField.Kind() != protoreflect.MessageKind {
		return nil, 0, fmt.Errorf("metadata List response %q has no message items field", response.Descriptor().FullName())
	}
	totalField := fields.ByName("total")
	if totalField == nil || totalField.Kind() != protoreflect.Int32Kind {
		return nil, 0, fmt.Errorf("metadata List response %q has no int32 total field", response.Descriptor().FullName())
	}
	list := response.Get(itemsField).List()
	items := make([]*dynamicpb.Message, 0, list.Len())
	for index := 0; index < list.Len(); index++ {
		items = append(items, proto.Clone(list.Get(index).Message().Interface()).(*dynamicpb.Message))
	}
	return items, int32(response.Get(totalField).Int()), nil
}

func publicMetadataCreateObject(
	existing *dynamicpb.Message,
	name string,
	labels, annotations map[string]string,
) (*dynamicpb.Message, error) {
	if existing == nil || name == "" {
		return nil, fmt.Errorf("public metadata create object and name are required")
	}
	object := proto.Clone(existing).(*dynamicpb.Message)
	fields := object.Descriptor().Fields()
	if idField := fields.ByName("id"); idField != nil {
		object.Clear(idField)
	}
	if statusField := fields.ByName("status"); statusField != nil {
		object.Clear(statusField)
	}
	metadataField := fields.ByName("metadata")
	if metadataField == nil || metadataField.Message() == nil {
		return nil, fmt.Errorf("public create object %q has no metadata", object.Descriptor().FullName())
	}
	metadata := object.Mutable(metadataField).Message()
	nameField := metadata.Descriptor().Fields().ByName("name")
	if nameField == nil || nameField.Kind() != protoreflect.StringKind {
		return nil, fmt.Errorf("public metadata %q has no string name field", metadata.Descriptor().FullName())
	}
	metadata.Set(nameField, protoreflect.ValueOfString(name))
	for _, fieldName := range []protoreflect.Name{"creation_timestamp", "deletion_timestamp", "creator", "version"} {
		if field := metadata.Descriptor().Fields().ByName(fieldName); field != nil {
			metadata.Clear(field)
		}
	}
	if err := replaceMetadataMap(metadata, "labels", labels); err != nil {
		return nil, err
	}
	if err := replaceMetadataMap(metadata, "annotations", annotations); err != nil {
		return nil, err
	}
	return object, nil
}

func createPublicMetadataObject(
	ctx context.Context,
	conn grpc.ClientConnInterface,
	service protoreflect.ServiceDescriptor,
	object *dynamicpb.Message,
) (*dynamicpb.Message, error) {
	if conn == nil || service == nil || object == nil {
		return nil, fmt.Errorf("public metadata Create connection, service, and object are required")
	}
	method := service.Methods().ByName("Create")
	if method == nil {
		return nil, fmt.Errorf("public metadata service %q has no Create method", service.FullName())
	}
	objectField := method.Input().Fields().ByName("object")
	if objectField == nil || objectField.Message() == nil {
		return nil, fmt.Errorf("public metadata Create request %q has no object field", method.Input().FullName())
	}
	request := dynamicpb.NewMessage(method.Input())
	request.Set(objectField, protoreflect.ValueOfMessage(object.ProtoReflect()))
	response := dynamicpb.NewMessage(method.Output())
	if err := conn.Invoke(ctx, metadataRPCMethodName(service, method.Name()), request, response); err != nil {
		return nil, fmt.Errorf("public metadata Create %q: %w", service.FullName(), err)
	}
	createdField := method.Output().Fields().ByName("object")
	if createdField == nil || !response.Has(createdField) {
		return nil, fmt.Errorf("public metadata Create %q returned no object", service.FullName())
	}
	return proto.Clone(response.Get(createdField).Message().Interface()).(*dynamicpb.Message), nil
}

func deleteMetadataObject(
	ctx context.Context,
	conn grpc.ClientConnInterface,
	service protoreflect.ServiceDescriptor,
	id string,
) error {
	if conn == nil || service == nil || id == "" {
		return fmt.Errorf("metadata Delete connection, service, and object ID are required")
	}
	method := service.Methods().ByName("Delete")
	if method == nil {
		return fmt.Errorf("metadata service %q has no Delete method", service.FullName())
	}
	idField := method.Input().Fields().ByName("id")
	if idField == nil || idField.Kind() != protoreflect.StringKind {
		return fmt.Errorf("metadata Delete request %q has no string id field", method.Input().FullName())
	}
	request := dynamicpb.NewMessage(method.Input())
	request.Set(idField, protoreflect.ValueOfString(id))
	response := dynamicpb.NewMessage(method.Output())
	if err := conn.Invoke(ctx, metadataRPCMethodName(service, method.Name()), request, response); err != nil {
		return fmt.Errorf("metadata Delete %q: %w", service.FullName(), err)
	}
	return nil
}

func cleanupMetadataObjectsNamed(
	ctx context.Context,
	conn grpc.ClientConnInterface,
	service protoreflect.ServiceDescriptor,
	name string,
) error {
	filter := fmt.Sprintf("this.metadata.name == %q", name)
	response, err := metadataList(ctx, conn, service, filter)
	if err != nil {
		return err
	}
	items, _, err := metadataListObjects(response)
	if err != nil {
		return err
	}
	for _, item := range items {
		idField := item.Descriptor().Fields().ByName("id")
		if idField == nil || idField.Kind() != protoreflect.StringKind {
			return fmt.Errorf("metadata object %q has no string id field", item.Descriptor().FullName())
		}
		if err := deleteMetadataObject(ctx, conn, service, item.Get(idField).String()); err != nil {
			return err
		}
	}
	return nil
}

// updatePrivateMetadata replaces only the selected metadata maps and keeps the
// complete object snapshot intact for Update APIs that require a full object.
func updatePrivateMetadata(
	ctx context.Context,
	conn grpc.ClientConnInterface,
	service protoreflect.ServiceDescriptor,
	id string,
	labels, annotations map[string]string,
) (*dynamicpb.Message, error) {
	if service == nil || !serviceHasMetadataMethod(service, "Update") {
		return nil, fmt.Errorf("private metadata service has no Update method")
	}
	method := service.Methods().ByName("Update")
	var updated *dynamicpb.Message
	err := retry.OnError(retry.DefaultBackoff, func(err error) bool {
		return status.Code(err) == codes.Aborted
	}, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		current, err := privateMetadataSnapshot(ctx, conn, service, id)
		if err != nil {
			return err
		}
		metadataField := current.Descriptor().Fields().ByName("metadata")
		metadata := current.Mutable(metadataField).Message()
		if err := replaceMetadataMap(metadata, "labels", labels); err != nil {
			return err
		}
		if err := replaceMetadataMap(metadata, "annotations", annotations); err != nil {
			return err
		}

		request := dynamicpb.NewMessage(method.Input())
		objectField := method.Input().Fields().ByName("object")
		if objectField == nil {
			return fmt.Errorf("private metadata Update request %q has no object field", method.Input().FullName())
		}
		request.Set(objectField, protoreflect.ValueOfMessage(current.ProtoReflect()))
		maskField := method.Input().Fields().ByName("update_mask")
		if maskField != nil {
			mask := dynamicpb.NewMessage(maskField.Message())
			pathsField := maskField.Message().Fields().ByName("paths")
			if pathsField == nil || pathsField.Kind() != protoreflect.StringKind || !pathsField.IsList() {
				return fmt.Errorf("private metadata Update mask %q has no string paths", maskField.Message().FullName())
			}
			paths := mask.Mutable(pathsField).List()
			paths.Append(protoreflect.ValueOfString("metadata.labels"))
			paths.Append(protoreflect.ValueOfString("metadata.annotations"))
			request.Set(maskField, protoreflect.ValueOfMessage(mask))
		}
		if lockField := method.Input().Fields().ByName("lock"); lockField != nil && lockField.Kind() == protoreflect.BoolKind {
			request.Set(lockField, protoreflect.ValueOfBool(true))
		}
		response := dynamicpb.NewMessage(method.Output())
		if err := conn.Invoke(ctx, metadataRPCMethodName(service, method.Name()), request, response); err != nil {
			return fmt.Errorf("private metadata Update %q: %w", service.FullName(), err)
		}
		updatedField := method.Output().Fields().ByName("object")
		if updatedField == nil || !response.Has(updatedField) {
			return fmt.Errorf("private metadata Update %q returned no object", service.FullName())
		}
		updated = proto.Clone(response.Get(updatedField).Message().Interface()).(*dynamicpb.Message)
		return nil
	})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}
	return updated, nil
}

// updatePublicMetadata submits a map-level public update with only the object
// ID and metadata name populated. Keeping the object partial avoids copying
// sensitive resource payloads (for example Secret data) through this helper.
func updatePublicMetadata(
	ctx context.Context,
	conn grpc.ClientConnInterface,
	service protoreflect.ServiceDescriptor,
	id string,
	labels, annotations map[string]string,
) error {
	if conn == nil {
		return fmt.Errorf("public metadata connection is required")
	}
	if service == nil {
		return fmt.Errorf("public metadata service is required")
	}
	method := service.Methods().ByName("Update")
	if method == nil {
		return fmt.Errorf("public metadata service %q has no Update method", service.FullName())
	}
	request := dynamicpb.NewMessage(method.Input())
	objectField := method.Input().Fields().ByName("object")
	if objectField == nil || objectField.Message() == nil {
		return fmt.Errorf("public metadata Update request %q has no object field", method.Input().FullName())
	}
	object := dynamicpb.NewMessage(objectField.Message())
	idField := object.Descriptor().Fields().ByName("id")
	if idField == nil || idField.Kind() != protoreflect.StringKind {
		return fmt.Errorf("public metadata Update object %q has no string id field", object.Descriptor().FullName())
	}
	object.Set(idField, protoreflect.ValueOfString(id))
	metadataField := object.Descriptor().Fields().ByName("metadata")
	if metadataField == nil || metadataField.Message() == nil {
		return fmt.Errorf("public metadata Update object %q has no metadata field", object.Descriptor().FullName())
	}
	current, err := metadataObjectSnapshot(ctx, conn, service, id)
	if err != nil {
		return fmt.Errorf("get public metadata name for %q: %w", service.FullName(), err)
	}
	currentMetadataField := current.Descriptor().Fields().ByName("metadata")
	if currentMetadataField == nil || !current.Has(currentMetadataField) {
		return fmt.Errorf("public metadata Get %q returned an object without metadata", service.FullName())
	}
	currentMetadata := current.Get(currentMetadataField).Message()
	nameField := currentMetadata.Descriptor().Fields().ByName("name")
	if nameField == nil || nameField.Kind() != protoreflect.StringKind {
		return fmt.Errorf("public metadata Get %q returned metadata without a string name", service.FullName())
	}
	metadata := object.Mutable(metadataField).Message()
	metadataNameField := metadata.Descriptor().Fields().ByName("name")
	if metadataNameField == nil || metadataNameField.Kind() != protoreflect.StringKind {
		return fmt.Errorf("public metadata Update object %q has metadata without a string name", object.Descriptor().FullName())
	}
	metadata.Set(metadataNameField, protoreflect.ValueOfString(currentMetadata.Get(nameField).String()))
	paths := make([]string, 0, 2)
	if labels != nil {
		if err := replaceMetadataMap(metadata, "labels", labels); err != nil {
			return err
		}
		paths = append(paths, "metadata.labels")
	}
	if annotations != nil {
		if err := replaceMetadataMap(metadata, "annotations", annotations); err != nil {
			return err
		}
		paths = append(paths, "metadata.annotations")
	}
	if len(paths) == 0 {
		return fmt.Errorf("public metadata Update requires at least one map")
	}
	maskField := method.Input().Fields().ByName("update_mask")
	if maskField == nil || maskField.Message() == nil {
		return fmt.Errorf("public metadata Update request %q has no update_mask", method.Input().FullName())
	}
	mask := dynamicpb.NewMessage(maskField.Message())
	pathsField := maskField.Message().Fields().ByName("paths")
	if pathsField == nil || !pathsField.IsList() || pathsField.Kind() != protoreflect.StringKind {
		return fmt.Errorf("public metadata Update mask %q has no string paths", maskField.Message().FullName())
	}
	maskPaths := mask.Mutable(pathsField).List()
	for _, path := range paths {
		maskPaths.Append(protoreflect.ValueOfString(path))
	}
	request.Set(objectField, protoreflect.ValueOfMessage(object))
	request.Set(maskField, protoreflect.ValueOfMessage(mask))
	response := dynamicpb.NewMessage(method.Output())
	if err := conn.Invoke(ctx, metadataRPCMethodName(service, method.Name()), request, response); err != nil {
		return fmt.Errorf("public metadata Update %q: %w", service.FullName(), err)
	}
	return nil
}

func replaceMetadataMap(metadata protoreflect.Message, fieldName protoreflect.Name, values map[string]string) error {
	field := metadata.Descriptor().Fields().ByName(fieldName)
	if field == nil || !field.IsMap() || field.MapKey().Kind() != protoreflect.StringKind || field.MapValue().Kind() != protoreflect.StringKind {
		return fmt.Errorf("metadata field %q is not a string map", fieldName)
	}
	valueMap := metadata.Mutable(field).Map()
	keys := make([]protoreflect.MapKey, 0, valueMap.Len())
	valueMap.Range(func(key protoreflect.MapKey, _ protoreflect.Value) bool {
		keys = append(keys, key)
		return true
	})
	for _, key := range keys {
		valueMap.Clear(key)
	}
	for key, value := range values {
		valueMap.Set(protoreflect.ValueOfString(key).MapKey(), protoreflect.ValueOfString(value))
	}
	return nil
}

func metadataMapValues(object protoreflect.Message, fieldName protoreflect.Name) (map[string]string, error) {
	metadataField := object.Descriptor().Fields().ByName("metadata")
	if metadataField == nil || !object.Has(metadataField) {
		return nil, fmt.Errorf("object %q has no metadata", object.Descriptor().FullName())
	}
	metadata := object.Get(metadataField).Message()
	mapField := metadata.Descriptor().Fields().ByName(fieldName)
	if mapField == nil || !mapField.IsMap() {
		return nil, fmt.Errorf("metadata field %q is not a map", fieldName)
	}
	values := make(map[string]string, metadata.Get(mapField).Map().Len())
	metadata.Get(mapField).Map().Range(func(key protoreflect.MapKey, value protoreflect.Value) bool {
		values[key.String()] = value.String()
		return true
	})
	return values, nil
}

type computeInstanceMetadataClient interface {
	Get(context.Context, *privatev1.ComputeInstancesGetRequest, ...grpc.CallOption) (*privatev1.ComputeInstancesGetResponse, error)
	Update(context.Context, *privatev1.ComputeInstancesUpdateRequest, ...grpc.CallOption) (*privatev1.ComputeInstancesUpdateResponse, error)
}

type computeInstanceMetadataFixture struct {
	client   computeInstanceMetadataClient
	id       string
	tenantID string
}

func discoverMetadataMethodCapabilities(
	private, public protoreflect.ServiceDescriptor,
) metadataMethodCapabilities {
	return metadataMethodCapabilities{
		PrivateCreate: serviceHasMetadataMethod(private, "Create"),
		PrivateGet:    serviceHasMetadataMethod(private, "Get"),
		PrivateList:   serviceHasMetadataMethod(private, "List"),
		PrivateUpdate: serviceHasMetadataMethod(private, "Update"),
		PrivateDelete: serviceHasMetadataMethod(private, "Delete"),
		PublicCreate:  serviceHasMetadataMethod(public, "Create"),
		PublicGet:     serviceHasMetadataMethod(public, "Get"),
		PublicList:    serviceHasMetadataMethod(public, "List"),
		PublicUpdate:  serviceHasMetadataMethod(public, "Update"),
		PublicDelete:  serviceHasMetadataMethod(public, "Delete"),
	}
}

func serviceHasMetadataMethod(service protoreflect.ServiceDescriptor, name protoreflect.Name) bool {
	return service != nil && service.Methods().ByName(name) != nil
}

func metadataAccountsFromTool(
	t *Tool,
	tenantAdmin, tenantUser, secondTenantUser metadataTestIdentity,
) metadataTestAccounts {
	return metadataTestAccounts{
		adminPrivate:     t.InternalView().AdminConn(),
		tenantAdmin:      tenantAdmin,
		tenantUser:       tenantUser,
		secondTenantUser: secondTenantUser,
	}
}

func metadataIdentityForExistingUser(
	ctx context.Context,
	username, password, tenantID string,
) (metadataTestIdentity, error) {
	token, err := tool.makeKeycloakTokenSource(ctx, username, password)
	if err != nil {
		return metadataTestIdentity{}, fmt.Errorf("create token source for %q: %w", username, err)
	}
	conn, err := tool.makeGrpcConn(externalServiceAddr, token)
	if err != nil {
		return metadataTestIdentity{}, fmt.Errorf("create public gRPC connection for %q: %w", username, err)
	}
	DeferCleanup(func() { _ = conn.Close() })
	httpClient := tool.makeHttpClient(externalServiceAddr, token)
	DeferCleanup(func() { httpClient.CloseIdleConnections() })
	return metadataTestIdentity{
		Conn:       conn,
		HTTPClient: httpClient,
		TenantID:   tenantID,
		Username:   username,
		Password:   password,
	}, nil
}

// createMetadataUserFixture creates an OSAC User backed by an ephemeral
// Keycloak identity. The OSAC User cleanup is registered after the identity
// cleanup so Ginkgo removes the dependent object first.
func createMetadataUserFixture(ctx context.Context) string {
	GinkgoHelper()
	username := catalogItemFixtureName()
	statusCode, _, err := tool.KeycloakAdminRequest(ctx, http.MethodPost, "/users", map[string]any{
		"username": username,
		"enabled":  true,
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(statusCode).To(Equal(http.StatusCreated))

	statusCode, body, err := tool.KeycloakAdminRequest(ctx, http.MethodGet,
		"/users?username="+username+"&exact=true", nil)
	Expect(err).NotTo(HaveOccurred())
	Expect(statusCode).To(Equal(http.StatusOK))
	var keycloakUsers []map[string]any
	Expect(json.Unmarshal(body, &keycloakUsers)).To(Succeed())
	Expect(keycloakUsers).To(HaveLen(1))
	keycloakID, ok := keycloakUsers[0]["id"].(string)
	Expect(ok).To(BeTrue())
	Expect(keycloakID).NotTo(BeEmpty())
	DeferCleanup(func(cleanupCtx context.Context) {
		_, _, deleteErr := tool.KeycloakAdminRequest(cleanupCtx, http.MethodDelete, "/users/"+keycloakID, nil)
		Expect(deleteErr).NotTo(HaveOccurred())
	})
	_, err = tool.keycloakEnsureUserReady(ctx, username)
	Expect(err).NotTo(HaveOccurred())

	users := privatev1.NewUsersClient(tool.InternalView().AdminConn())
	created, err := users.Create(ctx, privatev1.UsersCreateRequest_builder{
		Object: privatev1.User_builder{
			Metadata: privatev1.Metadata_builder{Name: username}.Build(),
			Status:   privatev1.UserStatus_builder{KeycloakUserId: keycloakID}.Build(),
		}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred())
	userID := created.GetObject().GetId()
	DeferCleanup(func(cleanupCtx context.Context) {
		_, deleteErr := users.Delete(cleanupCtx, privatev1.UsersDeleteRequest_builder{Id: userID}.Build())
		Expect(deleteErr).NotTo(HaveOccurred())
	})
	return userID
}

// newComputeInstanceMetadataFixture creates an instance in the tenant user's scope. The catalog
// fixture helpers register cleanup as each dependency is created, and createComputeInstanceFixture
// registers the instance cleanup last so Ginkgo removes resources in reverse dependency order.
func newComputeInstanceMetadataFixture(ctx context.Context, accounts metadataTestAccounts) (*computeInstanceMetadataFixture, error) {
	GinkgoHelper()
	if accounts.adminPrivate == nil {
		return nil, fmt.Errorf("private administrator connection is required")
	}
	if accounts.tenantAdmin.Conn == nil {
		return nil, fmt.Errorf("tenant administrator connection is required")
	}
	if accounts.tenantUser.Conn == nil {
		return nil, fmt.Errorf("tenant user connection is required")
	}
	if accounts.tenantUser.TenantID == "" {
		return nil, fmt.Errorf("tenant user tenant ID is required")
	}
	if accounts.tenantAdmin.TenantID != accounts.tenantUser.TenantID {
		return nil, fmt.Errorf("tenant administrator and tenant user must belong to the same tenant")
	}

	network := createCatalogItemNetworkFixture(ctx, accounts.tenantUser.TenantID, "")
	templateID := createCatalogItemComputeInstanceProvisioningTemplateFixture(ctx, nil)
	catalogItem := createComputeInstanceCatalogItemFixture(ctx, accounts.tenantAdmin.Conn,
		publicv1.ComputeInstanceCatalogItem_builder{
			Metadata:  publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
			Template:  publicv1.ComputeInstanceTemplateReference_builder{Id: templateID}.Build(),
			Published: true,
			Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
				RunStrategy: publicv1.ComputeInstanceRunStrategyFieldPolicy_builder{
					Locked: new(publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_HALTED),
				}.Build(),
			}.Build(),
		}.Build())
	instance, err := createComputeInstanceFixture(ctx, accounts.tenantUser.Conn, publicv1.ComputeInstanceSpec_builder{
		CatalogItem:        publicv1.ComputeInstanceCatalogItemReference_builder{Id: catalogItem.GetId()}.Build(),
		NetworkAttachments: []*publicv1.ComputeNetworkAttachment{network.computeInstanceAttachment()},
	}.Build())
	if err != nil {
		return nil, fmt.Errorf("create metadata ComputeInstance fixture: %w", err)
	}
	return &computeInstanceMetadataFixture{
		client:   privatev1.NewComputeInstancesClient(accounts.adminPrivate),
		id:       instance.GetId(),
		tenantID: accounts.tenantUser.TenantID,
	}, nil
}

// privateSnapshot returns a detached copy of the complete private object, including metadata the
// public mapper omits.
func (f *computeInstanceMetadataFixture) privateSnapshot(ctx context.Context) (*privatev1.ComputeInstance, error) {
	if f == nil || f.client == nil {
		return nil, fmt.Errorf("private ComputeInstance client is required")
	}
	if f.id == "" {
		return nil, fmt.Errorf("ComputeInstance ID is required")
	}
	response, err := f.client.Get(ctx, privatev1.ComputeInstancesGetRequest_builder{Id: f.id}.Build())
	if err != nil {
		return nil, fmt.Errorf("get private ComputeInstance %q: %w", f.id, err)
	}
	if response == nil || response.GetObject() == nil {
		return nil, fmt.Errorf("private Get returned no ComputeInstance for %q", f.id)
	}
	if response.GetObject().GetMetadata() == nil {
		return nil, fmt.Errorf("private Get returned a ComputeInstance without metadata for %q", f.id)
	}
	return proto.Clone(response.GetObject()).(*privatev1.ComputeInstance), nil
}

// updateMetadata overlays both metadata maps on a complete private snapshot. The explicit mask
// replaces only those maps; all existing entries and unrelated object fields are retained.
// Version conflicts trigger a fresh snapshot and merge on each bounded retry.
func (f *computeInstanceMetadataFixture) updateMetadata(
	ctx context.Context,
	labels, annotations map[string]string,
) (*privatev1.ComputeInstance, error) {
	var updated *privatev1.ComputeInstance
	err := retry.OnError(retry.DefaultBackoff, func(err error) bool {
		return status.Code(err) == codes.Aborted
	}, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		object, err := f.privateSnapshot(ctx)
		if err != nil {
			return err
		}
		if object.GetMetadata() == nil {
			return fmt.Errorf("private ComputeInstance %q has no metadata", f.id)
		}
		metadata := proto.Clone(object.GetMetadata()).(*privatev1.Metadata)
		metadata.SetLabels(mergeMetadataValues(metadata.GetLabels(), labels))
		metadata.SetAnnotations(mergeMetadataValues(metadata.GetAnnotations(), annotations))
		object.SetMetadata(metadata)
		response, err := f.client.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
			Object:     object,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.labels", "metadata.annotations"}},
			Lock:       true,
		}.Build())
		if err != nil {
			return fmt.Errorf("update private ComputeInstance %q metadata: %w", f.id, err)
		}
		if response == nil || response.GetObject() == nil {
			return fmt.Errorf("private Update returned no ComputeInstance for %q", f.id)
		}
		updated = proto.Clone(response.GetObject()).(*privatev1.ComputeInstance)
		return nil
	})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}
	return updated, nil
}

func mergeMetadataValues(existing, updates map[string]string) map[string]string {
	result := make(map[string]string, len(existing)+len(updates))
	for key, value := range existing {
		result[key] = value
	}
	for key, value := range updates {
		result[key] = value
	}
	return result
}
