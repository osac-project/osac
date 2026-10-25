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
	"errors"
	"net/http"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

func TestDiscoverMetadataMethodCapabilities(t *testing.T) {
	fullPrivate := privatev1.File_osac_private_v1_compute_instances_service_proto.Services().ByName("ComputeInstances")
	fullPublic := publicv1.File_osac_public_v1_compute_instances_service_proto.Services().ByName("ComputeInstances")
	createOnlyPrivate := privatev1.File_osac_private_v1_self_subject_access_review_service_proto.Services().ByName("SelfSubjectAccessReviews")
	createOnlyPublic := publicv1.File_osac_public_v1_self_subject_access_review_service_proto.Services().ByName("SelfSubjectAccessReviews")
	privateNATGateways := privatev1.File_osac_private_v1_nat_gateways_service_proto.Services().ByName("NATGateways")
	publicNATGateways := publicv1.File_osac_public_v1_nat_gateways_service_proto.Services().ByName("NATGateways")

	allMethods := metadataMethodCapabilities{
		PrivateCreate: true, PrivateGet: true, PrivateList: true, PrivateUpdate: true, PrivateDelete: true,
		PublicCreate: true, PublicGet: true, PublicList: true, PublicUpdate: true, PublicDelete: true,
	}
	createOnly := metadataMethodCapabilities{PrivateCreate: true, PublicCreate: true}
	privateUpdateOnly := metadataMethodCapabilities{
		PrivateCreate: true, PrivateGet: true, PrivateList: true, PrivateUpdate: true, PrivateDelete: true,
		PublicCreate: true, PublicGet: true, PublicList: true, PublicDelete: true,
	}
	noMethods := metadataMethodCapabilities{}

	for _, test := range []struct {
		name    string
		private protoreflect.ServiceDescriptor
		public  protoreflect.ServiceDescriptor
		want    metadataMethodCapabilities
	}{
		{name: "CRUD services", private: fullPrivate, public: fullPublic, want: allMethods},
		{name: "create-only services", private: createOnlyPrivate, public: createOnlyPublic, want: createOnly},
		{name: "private-only update", private: privateNATGateways, public: publicNATGateways, want: privateUpdateOnly},
		{name: "missing descriptors", want: noMethods},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := discoverMetadataMethodCapabilities(test.private, test.public); got != test.want {
				t.Fatalf("discoverMetadataMethodCapabilities() = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestMetadataAccountsFromToolKeepsExistingIdentityViews(t *testing.T) {
	adminConn := &grpc.ClientConn{}
	tool := &Tool{internalView: &ToolView{adminConn: adminConn}}
	identities := []metadataTestIdentity{
		{Conn: &grpc.ClientConn{}, HTTPClient: &http.Client{}, TenantID: "tenant-admin"},
		{Conn: &grpc.ClientConn{}, HTTPClient: &http.Client{}, TenantID: "tenant-user"},
		{Conn: &grpc.ClientConn{}, HTTPClient: &http.Client{}, TenantID: "second-tenant"},
	}

	accounts := metadataAccountsFromTool(tool, identities[0], identities[1], identities[2])
	if accounts.adminPrivate != adminConn {
		t.Fatal("private administrator connection was not taken from the internal view")
	}
	for i, got := range []metadataTestIdentity{accounts.tenantAdmin, accounts.tenantUser, accounts.secondTenantUser} {
		want := identities[i]
		if got != want {
			t.Errorf("identity %d = %+v, want %+v", i, got, want)
		}
	}
}

type fakeComputeInstanceMetadataClient struct {
	object            *privatev1.ComputeInstance
	getErr            error
	getNilResponse    bool
	getNilObject      bool
	updateErr         error
	updateNilResponse bool
	updateNilObject   bool
	updateRequest     *privatev1.ComputeInstancesUpdateRequest
	updatedResponse   *privatev1.ComputeInstance
}

func (f *fakeComputeInstanceMetadataClient) Get(
	_ context.Context,
	_ *privatev1.ComputeInstancesGetRequest,
	_ ...grpc.CallOption,
) (*privatev1.ComputeInstancesGetResponse, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.getNilResponse {
		return nil, nil
	}
	if f.getNilObject {
		return privatev1.ComputeInstancesGetResponse_builder{}.Build(), nil
	}
	return privatev1.ComputeInstancesGetResponse_builder{
		Object: proto.Clone(f.object).(*privatev1.ComputeInstance),
	}.Build(), nil
}

func (f *fakeComputeInstanceMetadataClient) Update(
	_ context.Context,
	request *privatev1.ComputeInstancesUpdateRequest,
	_ ...grpc.CallOption,
) (*privatev1.ComputeInstancesUpdateResponse, error) {
	f.updateRequest = proto.Clone(request).(*privatev1.ComputeInstancesUpdateRequest)
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	if f.updateNilResponse {
		return nil, nil
	}
	if f.updateNilObject {
		return privatev1.ComputeInstancesUpdateResponse_builder{}.Build(), nil
	}
	if f.updatedResponse != nil {
		return privatev1.ComputeInstancesUpdateResponse_builder{
			Object: proto.Clone(f.updatedResponse).(*privatev1.ComputeInstance),
		}.Build(), nil
	}
	f.object = proto.Clone(request.GetObject()).(*privatev1.ComputeInstance)
	return privatev1.ComputeInstancesUpdateResponse_builder{Object: proto.Clone(f.object).(*privatev1.ComputeInstance)}.Build(), nil
}

func TestComputeInstanceMetadataFixturePrivateSnapshotReturnsCompleteCopy(t *testing.T) {
	want := privatev1.ComputeInstance_builder{
		Id: "instance-123",
		Metadata: privatev1.Metadata_builder{
			Name:    "instance",
			Tenant:  "tenant-a",
			Project: "engineering",
			Labels: map[string]string{
				"admin.osac.openshift.io/system": "private-label",
				"example.com/visible":            "ordinary-label",
			},
			Annotations: map[string]string{
				"admin.osac.openshift.io/system":    "private-annotation",
				"osac.openshift.io/owner-reference": "parent-123",
			},
		}.Build(),
		Spec: privatev1.ComputeInstanceSpec_builder{
			Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "template-123"}.Build(),
		}.Build(),
		Status: privatev1.ComputeInstanceStatus_builder{
			State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
		}.Build(),
	}.Build()
	client := &fakeComputeInstanceMetadataClient{object: want}
	fixture := &computeInstanceMetadataFixture{id: want.GetId(), client: client}

	got, err := fixture.privateSnapshot(context.Background())
	if err != nil {
		t.Fatalf("privateSnapshot() error = %v", err)
	}
	if !proto.Equal(got, want) {
		t.Fatalf("privateSnapshot() = %v, want full object %v", got, want)
	}
	got.GetMetadata().GetLabels()["example.com/visible"] = "changed"
	if client.object.GetMetadata().GetLabels()["example.com/visible"] != "ordinary-label" {
		t.Fatal("privateSnapshot() returned the fake client's stored object instead of an independent copy")
	}
}

func TestComputeInstanceMetadataFixturePrivateSnapshotRejectsMissingClientData(t *testing.T) {
	validObject := privatev1.ComputeInstance_builder{
		Id:       "instance-123",
		Metadata: privatev1.Metadata_builder{Tenant: "tenant-a"}.Build(),
	}.Build()
	for _, test := range []struct {
		name    string
		fixture *computeInstanceMetadataFixture
	}{
		{name: "nil fixture"},
		{name: "nil client", fixture: &computeInstanceMetadataFixture{id: "instance-123"}},
		{name: "empty ID", fixture: &computeInstanceMetadataFixture{client: &fakeComputeInstanceMetadataClient{object: validObject}}},
		{name: "nil response", fixture: &computeInstanceMetadataFixture{id: "instance-123", client: &fakeComputeInstanceMetadataClient{getNilResponse: true}}},
		{name: "nil object", fixture: &computeInstanceMetadataFixture{id: "instance-123", client: &fakeComputeInstanceMetadataClient{getNilObject: true}}},
		{name: "missing metadata", fixture: &computeInstanceMetadataFixture{id: "instance-123", client: &fakeComputeInstanceMetadataClient{object: privatev1.ComputeInstance_builder{Id: "instance-123"}.Build()}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got, err := test.fixture.privateSnapshot(context.Background()); err == nil || got != nil {
				t.Fatalf("privateSnapshot() = %v, %v; want nil object and an error", got, err)
			}
		})
	}
}

func TestComputeInstanceMetadataFixtureUpdateMetadataMergesMapsAndUsesExactMask(t *testing.T) {
	initial := privatev1.ComputeInstance_builder{
		Id: "instance-123",
		Metadata: privatev1.Metadata_builder{
			Name:    "instance",
			Tenant:  "tenant-a",
			Version: 7,
			Labels: map[string]string{
				"admin.osac.openshift.io/existing": "private-label",
				"example.com/existing":             "ordinary-label",
			},
			Annotations: map[string]string{
				"admin.osac.openshift.io/existing":  "private-annotation",
				"osac.openshift.io/owner-reference": "parent-123",
			},
		}.Build(),
		Spec: privatev1.ComputeInstanceSpec_builder{
			Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "template-123"}.Build(),
		}.Build(),
	}.Build()
	client := &fakeComputeInstanceMetadataClient{object: initial}
	fixture := &computeInstanceMetadataFixture{id: initial.GetId(), client: client}
	labels := map[string]string{
		"admin.osac.openshift.io/new": "written-label",
		"example.com/caller":          "caller-value",
	}
	annotations := map[string]string{"admin.osac.openshift.io/new": "written-annotation"}

	got, err := fixture.updateMetadata(context.Background(), labels, annotations)
	if err != nil {
		t.Fatalf("updateMetadata() error = %v", err)
	}
	if !proto.Equal(got, client.object) {
		t.Fatalf("updateMetadata() = %v, want complete updated object %v", got, client.object)
	}
	if client.updateRequest == nil {
		t.Fatal("updateMetadata() did not send an Update request")
	}
	if gotPaths := client.updateRequest.GetUpdateMask().GetPaths(); !equalStrings(gotPaths, []string{"metadata.labels", "metadata.annotations"}) {
		t.Fatalf("UpdateMask.Paths = %v, want only both metadata maps", gotPaths)
	}
	if !client.updateRequest.GetLock() {
		t.Fatal("Update request should use the version from the private snapshot")
	}
	updated := client.updateRequest.GetObject()
	if updated.GetMetadata().GetTenant() != "tenant-a" {
		t.Fatalf("updated tenant = %q, want tenant-a", updated.GetMetadata().GetTenant())
	}
	if got := updated.GetMetadata().GetAnnotations()["osac.openshift.io/owner-reference"]; got != "parent-123" {
		t.Fatalf("owner-reference = %q, want parent-123", got)
	}
	for key, value := range map[string]string{
		"admin.osac.openshift.io/existing": "private-label",
		"admin.osac.openshift.io/new":      "written-label",
		"example.com/existing":             "ordinary-label",
	} {
		if got := updated.GetMetadata().GetLabels()[key]; got != value {
			t.Errorf("label %q = %q, want %q", key, got, value)
		}
	}
	for key, value := range map[string]string{
		"admin.osac.openshift.io/existing":  "private-annotation",
		"admin.osac.openshift.io/new":       "written-annotation",
		"osac.openshift.io/owner-reference": "parent-123",
	} {
		if got := updated.GetMetadata().GetAnnotations()[key]; got != value {
			t.Errorf("annotation %q = %q, want %q", key, got, value)
		}
	}
	if len(labels) != 2 || labels["example.com/caller"] != "caller-value" {
		t.Fatalf("updateMetadata() modified the caller's labels map: %v", labels)
	}
}

func TestComputeInstanceMetadataFixtureUpdateMetadataReturnsClientErrors(t *testing.T) {
	getFailure := errors.New("private Get failed")
	updateFailure := errors.New("private Update failed")
	for _, test := range []struct {
		name           string
		client         *fakeComputeInstanceMetadataClient
		wantErr        error
		wantErrMessage string
	}{
		{name: "snapshot failure", client: &fakeComputeInstanceMetadataClient{getErr: getFailure}, wantErr: getFailure},
		{name: "update failure", client: &fakeComputeInstanceMetadataClient{object: privatev1.ComputeInstance_builder{Id: "instance-123", Metadata: &privatev1.Metadata{}}.Build(), updateErr: updateFailure}, wantErr: updateFailure},
		{name: "nil update response", client: &fakeComputeInstanceMetadataClient{object: privatev1.ComputeInstance_builder{Id: "instance-123", Metadata: &privatev1.Metadata{}}.Build(), updateNilResponse: true}, wantErrMessage: "private Update returned no ComputeInstance"},
		{name: "nil updated object", client: &fakeComputeInstanceMetadataClient{object: privatev1.ComputeInstance_builder{Id: "instance-123", Metadata: &privatev1.Metadata{}}.Build(), updateNilObject: true}, wantErrMessage: "private Update returned no ComputeInstance"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := &computeInstanceMetadataFixture{id: "instance-123", client: test.client}
			_, err := fixture.updateMetadata(context.Background(), nil, nil)
			if test.wantErrMessage != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErrMessage) {
					t.Fatalf("updateMetadata() error = %v, want error containing %q", err, test.wantErrMessage)
				}
				return
			}
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("updateMetadata() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
