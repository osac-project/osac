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
	"maps"
	"net/http"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Metadata fixture helper contracts", func() {
	DescribeTable("discovers the metadata methods exposed by each service",
		func(private, public protoreflect.ServiceDescriptor, want metadataMethodCapabilities) {
			Expect(discoverMetadataMethodCapabilities(private, public)).To(Equal(want))
		},
		Entry("CRUD services",
			privatev1.File_osac_private_v1_compute_instances_service_proto.Services().ByName("ComputeInstances"),
			publicv1.File_osac_public_v1_compute_instances_service_proto.Services().ByName("ComputeInstances"),
			metadataMethodCapabilities{
				PrivateCreate: true, PrivateGet: true, PrivateList: true, PrivateUpdate: true, PrivateDelete: true,
				PublicCreate: true, PublicGet: true, PublicList: true, PublicUpdate: true, PublicDelete: true,
			},
		),
		Entry("create-only services",
			privatev1.File_osac_private_v1_self_subject_access_review_service_proto.Services().ByName("SelfSubjectAccessReviews"),
			publicv1.File_osac_public_v1_self_subject_access_review_service_proto.Services().ByName("SelfSubjectAccessReviews"),
			metadataMethodCapabilities{PrivateCreate: true, PublicCreate: true},
		),
		Entry("private-only update",
			privatev1.File_osac_private_v1_nat_gateways_service_proto.Services().ByName("NATGateways"),
			publicv1.File_osac_public_v1_nat_gateways_service_proto.Services().ByName("NATGateways"),
			metadataMethodCapabilities{
				PrivateCreate: true, PrivateGet: true, PrivateList: true, PrivateUpdate: true, PrivateDelete: true,
				PublicCreate: true, PublicGet: true, PublicList: true, PublicDelete: true,
			},
		),
		Entry("missing descriptors", nil, nil, metadataMethodCapabilities{}),
	)

	It("lists each persistent metadata service once", func() {
		Expect(metadataResourceServiceNames).To(HaveLen(26))
		seen := make(map[string]struct{}, len(metadataResourceServiceNames))
		for _, name := range metadataResourceServiceNames {
			Expect(seen).NotTo(HaveKey(name), "metadata service %q appears more than once", name)
			seen[name] = struct{}{}
		}
		Expect(seen).To(HaveLen(26))
	})

	resourceEntries := make([]any, 0, len(metadataResourceServiceNames))
	for _, name := range metadataResourceServiceNames {
		resourceEntries = append(resourceEntries, Entry(name, name))
	}
	tableArgs := append([]any{
		func(name string) {
			services, err := metadataServices(name)
			Expect(err).NotTo(HaveOccurred())
			Expect(services.private).NotTo(BeNil())
			Expect(serviceHasMetadataMethod(services.private, "Get")).To(BeTrue(), "private %s service must expose Get", name)
			Expect(serviceHasMetadataMethod(services.private, "Update")).To(BeTrue(), "private %s service must expose Update", name)

			privateGet := services.private.Methods().ByName("Get")
			privateObject := privateGet.Output().Fields().ByName("object")
			Expect(privateObject).NotTo(BeNil())
			Expect(privateObject.Message()).NotTo(BeNil())
			privateMetadata := privateObject.Message().Fields().ByName("metadata")
			Expect(privateMetadata).NotTo(BeNil())
			Expect(privateMetadata.Message()).NotTo(BeNil())
			for _, mapName := range []protoreflect.Name{"labels", "annotations"} {
				mapField := privateMetadata.Message().Fields().ByName(mapName)
				Expect(mapField).NotTo(BeNil(), "private %s metadata.%s is missing", name, mapName)
				Expect(mapField.IsMap()).To(BeTrue(), "private %s metadata.%s is not a map", name, mapName)
				Expect(mapField.MapKey().Kind()).To(Equal(protoreflect.StringKind))
				Expect(mapField.MapValue().Kind()).To(Equal(protoreflect.StringKind))
			}

			privateUpdate := services.private.Methods().ByName("Update")
			updateObject := privateUpdate.Input().Fields().ByName("object")
			Expect(updateObject).NotTo(BeNil())
			Expect(updateObject.Message()).NotTo(BeNil())
			Expect(updateObject.Message().FullName()).To(Equal(privateObject.Message().FullName()))

			if name == "Hubs" || name == "StorageBackends" {
				Expect(services.public).To(BeNil(), "%s must remain private-only", name)
				return
			}

			Expect(services.public).NotTo(BeNil(), "%s has no public service descriptor", name)
			publicGet := services.public.Methods().ByName("Get")
			Expect(publicGet).NotTo(BeNil(), "public %s service has no Get method", name)
			publicObject := publicGet.Output().Fields().ByName("object")
			Expect(publicObject).NotTo(BeNil())
			Expect(publicObject.Message()).NotTo(BeNil())
			publicMetadata := publicObject.Message().Fields().ByName("metadata")
			Expect(publicMetadata).NotTo(BeNil())
			Expect(publicMetadata.Message()).NotTo(BeNil())

			if publicCreate := services.public.Methods().ByName("Create"); publicCreate != nil &&
				serviceHasMetadataMethod(services.private, "List") && serviceHasMetadataMethod(services.private, "Delete") {
				createObject := publicCreate.Input().Fields().ByName("object")
				Expect(createObject).NotTo(BeNil(), "public %s Create request has no object", name)
				Expect(createObject.Message()).NotTo(BeNil())
				Expect(createObject.Message().FullName()).To(Equal(publicObject.Message().FullName()))
			}

			for _, mapName := range []protoreflect.Name{"labels", "annotations"} {
				mapField := publicMetadata.Message().Fields().ByName(mapName)
				Expect(mapField).NotTo(BeNil(), "public %s metadata.%s is missing", name, mapName)
				Expect(mapField.IsMap()).To(BeTrue(), "public %s metadata.%s is not a map", name, mapName)
				Expect(mapField.MapKey().Kind()).To(Equal(protoreflect.StringKind))
				Expect(mapField.MapValue().Kind()).To(Equal(protoreflect.StringKind))
			}

			if publicUpdate := services.public.Methods().ByName("Update"); publicUpdate != nil {
				updateObject := publicUpdate.Input().Fields().ByName("object")
				Expect(updateObject).NotTo(BeNil(), "public %s Update request has no object", name)
				Expect(updateObject.Message()).NotTo(BeNil())
				Expect(updateObject.Message().FullName()).To(Equal(publicObject.Message().FullName()))
				updateMetadata := updateObject.Message().Fields().ByName("metadata")
				Expect(updateMetadata).NotTo(BeNil())
				Expect(updateMetadata.Message()).NotTo(BeNil())
				mask := publicUpdate.Input().Fields().ByName("update_mask")
				Expect(mask).NotTo(BeNil())
				Expect(mask.Message()).NotTo(BeNil())
				paths := mask.Message().Fields().ByName("paths")
				Expect(paths).NotTo(BeNil())
				Expect(paths.IsList()).To(BeTrue())
				Expect(paths.Kind()).To(Equal(protoreflect.StringKind))
			}
		},
	}, resourceEntries...)
	DescribeTable("exposes matching private and public metadata descriptors", tableArgs...)

	It("replaces only the requested metadata map", func() {
		metadata := privatev1.Metadata_builder{
			Labels:      map[string]string{"old-label": "old"},
			Annotations: map[string]string{"old-annotation": "old"},
		}.Build()
		Expect(replaceMetadataMap(metadata.ProtoReflect(), "labels", map[string]string{"new-label": "new"})).To(Succeed())

		object := privatev1.ComputeInstance_builder{Metadata: metadata}.Build()
		gotLabels, err := metadataMapValues(object.ProtoReflect(), "labels")
		Expect(err).NotTo(HaveOccurred())
		gotAnnotations, err := metadataMapValues(object.ProtoReflect(), "annotations")
		Expect(err).NotTo(HaveOccurred())
		Expect(gotLabels).To(Equal(map[string]string{"new-label": "new"}))
		Expect(gotAnnotations).To(Equal(map[string]string{"old-annotation": "old"}))
		Expect(maps.Equal(map[string]string{"left": ""}, map[string]string{"right": ""})).To(BeFalse())
	})

	It("sanitizes output fields when building a public create object", func() {
		services, err := metadataServices("ComputeInstances")
		Expect(err).NotTo(HaveOccurred())
		getMethod := services.public.Methods().ByName("Get")
		Expect(getMethod).NotTo(BeNil())
		objectField := getMethod.Output().Fields().ByName("object")
		Expect(objectField).NotTo(BeNil())
		Expect(objectField.Message()).NotTo(BeNil())
		objectDescriptor := objectField.Message()
		objectFields := objectDescriptor.Fields()
		idField := objectFields.ByName("id")
		statusField := objectFields.ByName("status")
		metadataField := objectFields.ByName("metadata")
		Expect(idField).NotTo(BeNil())
		Expect(statusField).NotTo(BeNil())
		Expect(metadataField).NotTo(BeNil())
		Expect(metadataField.Message()).NotTo(BeNil())

		existing := dynamicpb.NewMessage(objectDescriptor)
		existing.Set(idField, protoreflect.ValueOfString("existing-id"))
		existing.Set(statusField, protoreflect.ValueOfMessage(dynamicpb.NewMessage(statusField.Message())))
		metadata := existing.Mutable(metadataField).Message()
		metadataFields := metadata.Descriptor().Fields()
		for _, fieldName := range []protoreflect.Name{"name", "tenant", "project", "creator"} {
			field := metadataFields.ByName(fieldName)
			Expect(field).NotTo(BeNil(), "metadata field %q is missing", fieldName)
			Expect(field.Kind()).To(Equal(protoreflect.StringKind), "metadata field %q is not a string", fieldName)
			metadata.Set(field, protoreflect.ValueOfString("original-"+string(fieldName)))
		}
		versionField := metadataFields.ByName("version")
		Expect(versionField).NotTo(BeNil())
		Expect(versionField.Kind()).To(Equal(protoreflect.Int32Kind))
		metadata.Set(versionField, protoreflect.ValueOfInt32(7))
		for _, fieldName := range []protoreflect.Name{"creation_timestamp", "deletion_timestamp"} {
			field := metadataFields.ByName(fieldName)
			Expect(field).NotTo(BeNil(), "metadata field %q is missing", fieldName)
			Expect(field.Kind()).To(Equal(protoreflect.MessageKind), "metadata field %q is not a message", fieldName)
			metadata.Set(field, protoreflect.ValueOfMessage(dynamicpb.NewMessage(field.Message())))
		}
		Expect(replaceMetadataMap(metadata, "labels", map[string]string{"old": "label"})).To(Succeed())
		Expect(replaceMetadataMap(metadata, "annotations", map[string]string{"old": "annotation"})).To(Succeed())

		created, err := publicMetadataCreateObject(existing, "new-name", map[string]string{"new": "label"}, map[string]string{"new": "annotation"})
		Expect(err).NotTo(HaveOccurred())
		Expect(created.Has(idField)).To(BeFalse())
		Expect(created.Has(statusField)).To(BeFalse())
		createdMetadata := created.Get(metadataField).Message()
		for _, fieldName := range []protoreflect.Name{"creation_timestamp", "deletion_timestamp", "creator", "version"} {
			field := createdMetadata.Descriptor().Fields().ByName(fieldName)
			Expect(field).NotTo(BeNil(), "metadata output field %q is missing", fieldName)
			Expect(createdMetadata.Has(field)).To(BeFalse(), "create object retained metadata output field %q", fieldName)
		}
		Expect(createdMetadata.Get(metadataFields.ByName("name")).String()).To(Equal("new-name"))
		for _, fieldName := range []protoreflect.Name{"tenant", "project"} {
			Expect(createdMetadata.Get(metadataFields.ByName(fieldName)).String()).To(Equal("original-" + string(fieldName)))
		}
		for fieldName, want := range map[protoreflect.Name]map[string]string{
			"labels":      {"new": "label"},
			"annotations": {"new": "annotation"},
		} {
			got, mapErr := metadataMapValues(created.ProtoReflect(), fieldName)
			Expect(mapErr).NotTo(HaveOccurred())
			Expect(got).To(Equal(want), "metadata.%s", fieldName)
		}
	})

	It("keeps the existing identity views when building metadata accounts", func() {
		adminConn := &grpc.ClientConn{}
		tool := &Tool{internalView: &ToolView{adminConn: adminConn}}
		identities := []metadataTestIdentity{
			{Conn: &grpc.ClientConn{}, HTTPClient: &http.Client{}, TenantID: "tenant-admin"},
			{Conn: &grpc.ClientConn{}, HTTPClient: &http.Client{}, TenantID: "tenant-user"},
			{Conn: &grpc.ClientConn{}, HTTPClient: &http.Client{}, TenantID: "second-tenant"},
		}

		accounts := metadataAccountsFromTool(tool, identities[0], identities[1], identities[2])
		Expect(accounts.adminPrivate).To(Equal(adminConn))
		Expect(accounts.tenantAdmin).To(Equal(identities[0]))
		Expect(accounts.tenantUser).To(Equal(identities[1]))
		Expect(accounts.secondTenantUser).To(Equal(identities[2]))
	})

	It("refreshes the snapshot after a version conflict", func(ctx context.Context) {
		stale := privatev1.ComputeInstance_builder{
			Id:       "instance-123",
			Metadata: privatev1.Metadata_builder{Version: 7, Labels: map[string]string{"example.com/existing": "kept"}}.Build(),
		}.Build()
		current := proto.Clone(stale).(*privatev1.ComputeInstance)
		current.GetMetadata().SetVersion(8)
		client := &fakeComputeInstanceMetadataClient{
			object:     current,
			getObjects: []*privatev1.ComputeInstance{stale, current},
			updateErrors: []error{
				grpcstatus.Error(codes.Aborted, "object has been modified"),
			},
		}
		fixture := &computeInstanceMetadataFixture{id: stale.GetId(), client: client}

		updated, err := fixture.updateMetadata(ctx, map[string]string{"example.com/new": "value"}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(client.updateCalls).To(Equal(2), "one version conflict should cause one retry")
		Expect(client.updateRequests[0].GetObject().GetMetadata().GetVersion()).To(Equal(int32(7)))
		Expect(client.updateRequests[1].GetObject().GetMetadata().GetVersion()).To(Equal(int32(8)))
		Expect(updated.GetMetadata().GetLabels()).To(HaveKeyWithValue("example.com/existing", "kept"))
		Expect(updated.GetMetadata().GetLabels()).To(HaveKeyWithValue("example.com/new", "value"))
	})

	It("returns a complete independent private snapshot", func(ctx context.Context) {
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

		got, err := fixture.privateSnapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(proto.Equal(got, want)).To(BeTrue())
		got.GetMetadata().GetLabels()["example.com/visible"] = "changed"
		Expect(client.object.GetMetadata().GetLabels()).To(HaveKeyWithValue("example.com/visible", "ordinary-label"))
	})

	DescribeTable("rejects incomplete private snapshot fixtures",
		func(ctx context.Context, fixture *computeInstanceMetadataFixture) {
			got, err := fixture.privateSnapshot(ctx)
			Expect(err).To(HaveOccurred())
			Expect(got).To(BeNil())
		},
		Entry("nil fixture", (*computeInstanceMetadataFixture)(nil)),
		Entry("nil client", &computeInstanceMetadataFixture{id: "instance-123"}),
		Entry("empty ID", &computeInstanceMetadataFixture{client: &fakeComputeInstanceMetadataClient{
			object: privatev1.ComputeInstance_builder{
				Id:       "instance-123",
				Metadata: privatev1.Metadata_builder{Tenant: "tenant-a"}.Build(),
			}.Build(),
		}}),
		Entry("nil response", &computeInstanceMetadataFixture{id: "instance-123", client: &fakeComputeInstanceMetadataClient{getNilResponse: true}}),
		Entry("nil object", &computeInstanceMetadataFixture{id: "instance-123", client: &fakeComputeInstanceMetadataClient{getNilObject: true}}),
		Entry("missing metadata", &computeInstanceMetadataFixture{id: "instance-123", client: &fakeComputeInstanceMetadataClient{
			object: privatev1.ComputeInstance_builder{Id: "instance-123"}.Build(),
		}}),
	)

	It("merges metadata maps and uses the exact update mask", func(ctx context.Context) {
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

		got, err := fixture.updateMetadata(ctx, labels, annotations)
		Expect(err).NotTo(HaveOccurred())
		Expect(proto.Equal(got, client.object)).To(BeTrue())
		Expect(client.updateRequest).NotTo(BeNil())
		Expect(client.updateRequest.GetUpdateMask().GetPaths()).To(Equal([]string{"metadata.labels", "metadata.annotations"}))
		Expect(client.updateRequest.GetLock()).To(BeTrue())
		updated := client.updateRequest.GetObject()
		Expect(updated.GetMetadata().GetTenant()).To(Equal("tenant-a"))
		Expect(updated.GetMetadata().GetAnnotations()).To(HaveKeyWithValue("osac.openshift.io/owner-reference", "parent-123"))
		for key, value := range map[string]string{
			"admin.osac.openshift.io/existing": "private-label",
			"admin.osac.openshift.io/new":      "written-label",
			"example.com/existing":             "ordinary-label",
		} {
			Expect(updated.GetMetadata().GetLabels()).To(HaveKeyWithValue(key, value))
		}
		for key, value := range map[string]string{
			"admin.osac.openshift.io/existing":  "private-annotation",
			"admin.osac.openshift.io/new":       "written-annotation",
			"osac.openshift.io/owner-reference": "parent-123",
		} {
			Expect(updated.GetMetadata().GetAnnotations()).To(HaveKeyWithValue(key, value))
		}
		Expect(labels).To(HaveLen(2))
		Expect(labels).To(HaveKeyWithValue("example.com/caller", "caller-value"))
	})

	getFailure := errors.New("private Get failed")
	updateFailure := errors.New("private Update failed")
	DescribeTable("returns private client errors",
		func(ctx context.Context, client *fakeComputeInstanceMetadataClient, wantErr error, wantErrMessage string) {
			fixture := &computeInstanceMetadataFixture{id: "instance-123", client: client}
			_, err := fixture.updateMetadata(ctx, nil, nil)
			Expect(err).To(HaveOccurred())
			if wantErrMessage != "" {
				Expect(err.Error()).To(ContainSubstring(wantErrMessage))
				return
			}
			Expect(errors.Is(err, wantErr)).To(BeTrue())
		},
		Entry("snapshot failure", &fakeComputeInstanceMetadataClient{getErr: getFailure}, getFailure, ""),
		Entry("update failure", &fakeComputeInstanceMetadataClient{
			object: privatev1.ComputeInstance_builder{
				Id:       "instance-123",
				Metadata: privatev1.Metadata_builder{}.Build(),
			}.Build(),
			updateErr: updateFailure,
		}, updateFailure, ""),
		Entry("nil update response", &fakeComputeInstanceMetadataClient{
			object: privatev1.ComputeInstance_builder{
				Id:       "instance-123",
				Metadata: privatev1.Metadata_builder{}.Build(),
			}.Build(),
			updateNilResponse: true,
		}, nil, "private Update returned no ComputeInstance"),
		Entry("nil updated object", &fakeComputeInstanceMetadataClient{
			object: privatev1.ComputeInstance_builder{
				Id:       "instance-123",
				Metadata: privatev1.Metadata_builder{}.Build(),
			}.Build(),
			updateNilObject: true,
		}, nil, "private Update returned no ComputeInstance"),
	)
})

type fakeComputeInstanceMetadataClient struct {
	object            *privatev1.ComputeInstance
	getObjects        []*privatev1.ComputeInstance
	getCalls          int
	getErr            error
	getNilResponse    bool
	getNilObject      bool
	updateErr         error
	updateErrors      []error
	updateCalls       int
	updateRequests    []*privatev1.ComputeInstancesUpdateRequest
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
	object := f.object
	if f.getCalls < len(f.getObjects) {
		object = f.getObjects[f.getCalls]
	}
	f.getCalls++
	return privatev1.ComputeInstancesGetResponse_builder{
		Object: proto.Clone(object).(*privatev1.ComputeInstance),
	}.Build(), nil
}

func (f *fakeComputeInstanceMetadataClient) Update(
	_ context.Context,
	request *privatev1.ComputeInstancesUpdateRequest,
	_ ...grpc.CallOption,
) (*privatev1.ComputeInstancesUpdateResponse, error) {
	f.updateRequest = proto.Clone(request).(*privatev1.ComputeInstancesUpdateRequest)
	f.updateRequests = append(f.updateRequests, f.updateRequest)
	if f.updateCalls < len(f.updateErrors) {
		err := f.updateErrors[f.updateCalls]
		f.updateCalls++
		return nil, err
	}
	f.updateCalls++
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
