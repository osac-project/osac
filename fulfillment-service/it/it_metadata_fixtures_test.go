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
	"fmt"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

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
func (f *computeInstanceMetadataFixture) updateMetadata(
	ctx context.Context,
	labels, annotations map[string]string,
) (*privatev1.ComputeInstance, error) {
	object, err := f.privateSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	if object.GetMetadata() == nil {
		return nil, fmt.Errorf("private ComputeInstance %q has no metadata", f.id)
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
		return nil, fmt.Errorf("update private ComputeInstance %q metadata: %w", f.id, err)
	}
	if response == nil || response.GetObject() == nil {
		return nil, fmt.Errorf("private Update returned no ComputeInstance for %q", f.id)
	}
	return proto.Clone(response.GetObject()).(*privatev1.ComputeInstance), nil
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
