/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	bmfv1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	"github.com/osac-project/osac/fulfillment-service/internal/servers"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("BareMetalInstance architecture admission", Ordered, ContinueOnFailure, func() {
	var (
		instances       privatev1.BareMetalInstancesClient
		publicInstances publicv1.BareMetalInstancesClient
		images          privatev1.DiskImagesClient
		types           privatev1.BareMetalInstanceTypesClient
		image           *privatev1.DiskImage
		instanceType    *privatev1.BareMetalInstanceType
		template        string
		toPublic        *servers.GenericMapper[*privatev1.BareMetalInstance, *publicv1.BareMetalInstance]
		toPrivate       *servers.GenericMapper[*publicv1.BareMetalInstance, *privatev1.BareMetalInstance]
	)
	architectures := map[string]privatev1.Architecture{
		"amd64": privatev1.Architecture_ARCHITECTURE_AMD64,
		"arm64": privatev1.Architecture_ARCHITECTURE_ARM64,
		"s390x": privatev1.Architecture_ARCHITECTURE_S390X,
	}
	BeforeAll(func(ctx context.Context) {
		ensureTenantDefaultNetworkingFixture(ctx, usersGroup, "")
	})
	BeforeEach(func(ctx context.Context) {
		instances = privatev1.NewBareMetalInstancesClient(tool.InternalView().AdminConn())
		publicInstances = publicv1.NewBareMetalInstancesClient(tool.ExternalView().UserConn())
		images = privatev1.NewDiskImagesClient(tool.InternalView().AdminConn())
		types = privatev1.NewBareMetalInstanceTypesClient(tool.InternalView().AdminConn())
		var err error
		toPublic, err = servers.NewGenericMapper[*privatev1.BareMetalInstance, *publicv1.BareMetalInstance]().SetLogger(logger).SetStrict(false).Build()
		Expect(err).NotTo(HaveOccurred())
		toPrivate, err = servers.NewGenericMapper[*publicv1.BareMetalInstance, *privatev1.BareMetalInstance]().SetLogger(logger).SetStrict(false).Build()
		Expect(err).NotTo(HaveOccurred())
		image = createCatalogItemDiskImageFixture(ctx, "shared", catalogItemFixtureName())
		typeID := createCatalogItemBareMetalInstanceTypeFixture(ctx, usersGroup)
		selected, err := types.Get(ctx, privatev1.BareMetalInstanceTypesGetRequest_builder{Id: typeID}.Build())
		Expect(err).NotTo(HaveOccurred())
		instanceType = selected.GetObject()
		template = createCatalogItemBareMetalInstanceTemplateFixture(ctx, nil, []*privatev1.BareMetalInstanceTemplateParameterDefinition{
			privatev1.BareMetalInstanceTemplateParameterDefinition_builder{
				Name: "layout", Type: "type.googleapis.com/google.protobuf.StringValue", Default: catalogItemParameterValue(wrapperspb.String("default-layout")),
			}.Build(),
		}, typeID)
	})
	object := func() *privatev1.BareMetalInstance {
		return privatev1.BareMetalInstance_builder{
			Metadata: catalogItemFixtureMetadata(usersGroup, ""),
			Spec: privatev1.BareMetalInstanceSpec_builder{
				Template:     privatev1.BareMetalInstanceTemplateReference_builder{Id: template, Shared: true}.Build(),
				DiskImage:    privatev1.DiskImageReference_builder{Id: image.GetId(), Shared: true}.Build(),
				SshPublicKey: new(catalogItemFixtureSSHPublicKey),
			}.Build(),
		}.Build()
	}
	get := func(ctx context.Context, id string) *privatev1.BareMetalInstance {
		result, err := instances.Get(ctx, privatev1.BareMetalInstancesGetRequest_builder{Id: id}.Build())
		Expect(err).NotTo(HaveOccurred())
		return result.GetObject()
	}
	setArchitectures := func(ctx context.Context, members ...privatev1.Architecture) {
		image.GetSpec().SetArchitecture(members)
		result, err := images.Update(ctx, privatev1.DiskImagesUpdateRequest_builder{Object: image, UpdateMask: catalogItemUpdateMask("spec.architecture")}.Build())
		Expect(err).NotTo(HaveOccurred())
		image = result.GetObject()
	}
	updateImage := func(ctx context.Context, proposed *privatev1.DiskImage, mask *fieldmaskpb.FieldMask, public bool) error {
		if !public {
			_, err := images.Update(ctx, privatev1.DiskImagesUpdateRequest_builder{Object: proposed, UpdateMask: mask}.Build())
			return err
		}
		mapper, err := servers.NewGenericMapper[*privatev1.DiskImage, *publicv1.DiskImage]().SetLogger(logger).SetStrict(false).Build()
		Expect(err).NotTo(HaveOccurred())
		mapped := &publicv1.DiskImage{}
		Expect(mapper.Copy(ctx, proposed, mapped)).To(Succeed())
		client := publicv1.NewDiskImagesClient(tool.ExternalView().AdminConn())
		_, err = client.Update(ctx, publicv1.DiskImagesUpdateRequest_builder{Object: mapped, UpdateMask: mask}.Build())
		return err
	}
	cleanupInstance := func(id string) {
		deferCatalogItemFixtureDeletion(func(ctx context.Context) error {
			_, err := instances.Delete(ctx, privatev1.BareMetalInstancesDeleteRequest_builder{Id: id}.Build())
			return err
		}, func(ctx context.Context) (bool, error) {
			_, err := instances.Get(ctx, privatev1.BareMetalInstancesGetRequest_builder{Id: id}.Build())
			if status.Code(err) == codes.NotFound {
				return true, nil
			}
			if err != nil {
				return false, err
			}
			_, err = instances.Signal(ctx, privatev1.BareMetalInstancesSignalRequest_builder{Id: id}.Build())
			return status.Code(err) == codes.NotFound, err
		})
	}
	create := func(ctx context.Context, obj *privatev1.BareMetalInstance, public, dryRun bool) (*privatev1.BareMetalInstance, []string, error) {
		if dryRun {
			ctx = metadata.AppendToOutgoingContext(ctx, "x-dry-run", "true")
		}
		var result *privatev1.BareMetalInstance
		var warnings []string
		var err error
		if public {
			mapped := &publicv1.BareMetalInstance{}
			Expect(toPublic.Copy(ctx, obj, mapped)).To(Succeed())
			var response *publicv1.BareMetalInstancesCreateResponse
			response, err = publicInstances.Create(ctx, publicv1.BareMetalInstancesCreateRequest_builder{Object: mapped}.Build())
			if err == nil {
				result = &privatev1.BareMetalInstance{}
				Expect(toPrivate.Copy(ctx, response.GetObject(), result)).To(Succeed())
				warnings = response.GetWarnings()
			}
		} else {
			var response *privatev1.BareMetalInstancesCreateResponse
			response, err = instances.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{Object: obj}.Build())
			result, warnings = response.GetObject(), response.GetWarnings()
		}
		if err == nil && !dryRun {
			cleanupInstance(result.GetId())
		}
		return result, warnings, err
	}
	expectAbsent := func(ctx context.Context, name string) {
		filter := new(fmt.Sprintf("this.metadata.name == '%s'", name))
		priv, err := instances.List(ctx, privatev1.BareMetalInstancesListRequest_builder{Filter: filter}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(priv.GetItems()).To(BeEmpty())
		pub, err := publicInstances.List(ctx, publicv1.BareMetalInstancesListRequest_builder{Filter: filter}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(pub.GetItems()).To(BeEmpty())
	}
	expectMismatch := func(err error) {
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(status.Convert(err).Message()).To(And(HavePrefix("INCOMPATIBLE_DISK_IMAGE_ARCHITECTURE: "),
			ContainSubstring("supports ["), ContainSubstring("requires "),
			ContainSubstring("Select a compatible DiskImage or BareMetalInstanceType"),
			Not(ContainSubstring("quay.io")), Not(ContainSubstring("osac.openshift.io/host-type"))))
	}
	DescribeTableSubtree("API", func(public bool) {
		DescribeTable("rejects image changes that invalidate an existing instance", func(ctx context.Context, masked bool) {
			// Register Secret cleanup first so the dependent instance is removed before it.
			secret := createCatalogItemUserDataSecretFixture(ctx, usersGroup)
			obj := object()
			obj.GetSpec().SetUserDataSource(privatev1.UserDataSource_builder{Inline: new("#cloud-config\n")}.Build())
			created, _, err := create(ctx, obj, public, false)
			Expect(err).NotTo(HaveOccurred())
			before, err := images.Get(ctx, privatev1.DiskImagesGetRequest_builder{Id: image.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			proposed := proto.Clone(before.GetObject()).(*privatev1.DiskImage)
			proposed.GetSpec().SetArchitecture([]privatev1.Architecture{architectures["arm64"]})
			var mask *fieldmaskpb.FieldMask
			if masked {
				mask = catalogItemUpdateMask("spec.architecture")
			}
			err = updateImage(ctx, proposed, mask, public)
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			Expect(status.Convert(err).Message()).To(HavePrefix("DISK_IMAGE_ARCHITECTURE_IN_USE:"))
			after, err := images.Get(ctx, privatev1.DiskImagesGetRequest_builder{Id: image.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(proto.Equal(after.GetObject(), before.GetObject())).To(BeTrue())
			Expect(get(ctx, created.GetId()).GetSpec().GetDiskImage().GetId()).To(Equal(image.GetId()))

			change := privatev1.BareMetalInstance_builder{Id: created.GetId(), Spec: privatev1.BareMetalInstanceSpec_builder{
				UserDataSource: privatev1.UserDataSource_builder{Secret: privatev1.SecretLocalReference_builder{Id: secret}.Build()}.Build(),
			}.Build()}.Build()
			secretMask := catalogItemUpdateMask("spec.user_data_source")
			if public {
				mapped := &publicv1.BareMetalInstance{}
				Expect(toPublic.Copy(ctx, change, mapped)).To(Succeed())
				_, err = publicInstances.Update(ctx, publicv1.BareMetalInstancesUpdateRequest_builder{Object: mapped, UpdateMask: secretMask}.Build())
			} else {
				_, err = instances.Update(ctx, privatev1.BareMetalInstancesUpdateRequest_builder{Object: change, UpdateMask: secretMask}.Build())
			}
			Expect(err).NotTo(HaveOccurred())
			stored := get(ctx, created.GetId())
			Expect(stored.GetSpec().GetUserDataSource().HasInline()).To(BeFalse())
			Expect(stored.GetSpec().GetUserDataSource().GetSecret().GetId()).To(Equal(secret))
			setArchitectures(ctx, architectures["amd64"], architectures["arm64"])
			setArchitectures(ctx, architectures["amd64"])
		}, Entry("masked update", true), Entry("full update", false))

		DescribeTable("preserves catalog image compatibility without an instance", func(ctx context.Context, locked bool) {
			ref := privatev1.DiskImageReference_builder{Id: image.GetId(), Shared: true}.Build()
			policy := &privatev1.DiskImageReferenceFieldPolicy{}
			if locked {
				policy.SetLocked(ref)
			} else {
				policy.SetEditable(privatev1.EditableDiskImageReferenceField_builder{DefaultValue: ref}.Build())
			}
			catalogs := privatev1.NewBareMetalInstanceCatalogItemsClient(tool.InternalView().AdminConn())
			catalog, err := catalogs.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{Object: privatev1.BareMetalInstanceCatalogItem_builder{
				Metadata: catalogItemFixtureMetadata(usersGroup, ""), Title: "Protected architecture", Published: true,
				Template: privatev1.BareMetalInstanceTemplateReference_builder{Id: template, Shared: true}.Build(),
				Fields:   privatev1.BareMetalInstanceCatalogItemFields_builder{DiskImage: policy}.Build(),
			}.Build()}.Build())
			Expect(err).NotTo(HaveOccurred())
			deferCatalogItemFixtureDeletion(func(ctx context.Context) error {
				_, err := catalogs.Delete(ctx, privatev1.BareMetalInstanceCatalogItemsDeleteRequest_builder{Id: catalog.GetObject().GetId()}.Build())
				return err
			}, nil)
			proposed := proto.Clone(image).(*privatev1.DiskImage)
			proposed.GetSpec().SetArchitecture([]privatev1.Architecture{architectures["arm64"]})
			err = updateImage(ctx, proposed, catalogItemUpdateMask("spec.architecture"), public)
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
		}, Entry("locked image and template type", true), Entry("default image and template type", false))

		DescribeTable("compares canonical pairs and references", func(ctx context.Context, cpu, imageCPU string, byName bool) {
			selected := instanceType
			if cpu != "amd64" {
				id := createCatalogItemBareMetalInstanceTypeFixture(ctx, usersGroup, cpu)
				result, err := types.Get(ctx, privatev1.BareMetalInstanceTypesGetRequest_builder{Id: id}.Build())
				Expect(err).NotTo(HaveOccurred())
				selected = result.GetObject()
			}
			setArchitectures(ctx, architectures[imageCPU])
			obj := object()
			obj.GetSpec().SetInstanceType(privatev1.BareMetalInstanceTypeReference_builder{Id: selected.GetId(), Shared: true}.Build())
			if byName {
				obj.GetSpec().GetInstanceType().SetId("")
				obj.GetSpec().GetInstanceType().SetName(selected.GetMetadata().GetName())
				obj.GetSpec().GetDiskImage().SetId("")
				obj.GetSpec().GetDiskImage().SetName(image.GetMetadata().GetName())
			}
			result, _, err := create(ctx, obj, public, false)
			if cpu != imageCPU {
				expectMismatch(err)
				Expect(err.Error()).To(And(ContainSubstring(selected.GetMetadata().GetName()), ContainSubstring(image.GetMetadata().GetName()),
					ContainSubstring("requires "+cpu), ContainSubstring("supports ["+imageCPU+"]")))
				expectAbsent(ctx, obj.GetMetadata().GetName())
			} else {
				Expect(err).NotTo(HaveOccurred())
				stored := get(ctx, result.GetId())
				Expect(stored.GetSpec().GetInstanceType().GetId()).To(Equal(selected.GetId()))
				Expect(stored.GetSpec().GetDiskImage().GetId()).To(Equal(image.GetId()))
				Expect(proto.Equal(stored.GetSpec().GetTemplateParameters()["layout"], catalogItemParameterValue(wrapperspb.String("default-layout")))).To(BeTrue())
			}
		}, Entry("amd64/amd64", "amd64", "amd64", false), Entry("amd64/arm64", "amd64", "arm64", false), Entry("amd64/s390x", "amd64", "s390x", false),
			Entry("arm64/amd64", "arm64", "amd64", false), Entry("arm64/arm64", "arm64", "arm64", false), Entry("arm64/s390x", "arm64", "s390x", false),
			Entry("s390x/amd64", "s390x", "amd64", false), Entry("s390x/arm64", "s390x", "arm64", false), Entry("s390x/s390x", "s390x", "s390x", false),
			Entry("matching names", "arm64", "arm64", true), Entry("mismatching names", "arm64", "s390x", true))

		DescribeTable("uses template defaults and multi-architecture membership", func(ctx context.Context, cpu string, reverse bool) {
			members := []privatev1.Architecture{architectures["amd64"], architectures["arm64"]}
			if reverse {
				members[0], members[1] = members[1], members[0]
			}
			setArchitectures(ctx, members...)
			obj := object()
			if cpu != "amd64" {
				id := createCatalogItemBareMetalInstanceTypeFixture(ctx, usersGroup, cpu)
				obj.GetSpec().SetInstanceType(privatev1.BareMetalInstanceTypeReference_builder{Id: id, Shared: true}.Build())
			}
			result, _, err := create(ctx, obj, public, false)
			if cpu == "s390x" {
				expectMismatch(err)
				expectAbsent(ctx, obj.GetMetadata().GetName())
			} else {
				Expect(err).NotTo(HaveOccurred())
				Expect(result.GetSpec().GetInstanceType().GetId()).NotTo(BeEmpty())
			}
		}, Entry("template default amd64", "amd64", false), Entry("template default amd64 reversed", "amd64", true),
			Entry("arm64", "arm64", false), Entry("arm64 reversed", "arm64", true),
			Entry("s390x nonmember", "s390x", false), Entry("s390x nonmember reversed", "s390x", true))

		DescribeTable("validates effective catalog policies", func(ctx context.Context, locked, override, compatible bool) {
			if !compatible {
				setArchitectures(ctx, architectures["arm64"])
			}
			typeRef := privatev1.BareMetalInstanceTypeReference_builder{Id: instanceType.GetId(), Shared: true}.Build()
			imageRef := privatev1.DiskImageReference_builder{Id: image.GetId(), Shared: true}.Build()
			typePolicy := &privatev1.BareMetalInstanceTypeReferenceFieldPolicy{}
			imagePolicy := &privatev1.DiskImageReferenceFieldPolicy{}
			if locked {
				typePolicy.SetLocked(typeRef)
				imagePolicy.SetLocked(imageRef)
			} else {
				typePolicy.SetEditable(privatev1.EditableBareMetalInstanceTypeReferenceField_builder{DefaultValue: typeRef}.Build())
				imagePolicy.SetEditable(privatev1.EditableDiskImageReferenceField_builder{DefaultValue: imageRef}.Build())
			}
			catalogs := privatev1.NewBareMetalInstanceCatalogItemsClient(tool.InternalView().AdminConn())
			item := privatev1.BareMetalInstanceCatalogItem_builder{
				Metadata: catalogItemFixtureMetadata(usersGroup, ""), Title: "Architecture admission", Published: true,
				Template: privatev1.BareMetalInstanceTemplateReference_builder{Id: template, Shared: true}.Build(),
				Fields:   privatev1.BareMetalInstanceCatalogItemFields_builder{InstanceType: typePolicy, DiskImage: imagePolicy}.Build(),
			}.Build()
			catalog, err := catalogs.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{Object: item}.Build())
			if !compatible {
				expectMismatch(err)
				stored, listErr := catalogs.List(ctx, privatev1.BareMetalInstanceCatalogItemsListRequest_builder{
					Filter: new(fmt.Sprintf("this.metadata.name == '%s'", item.GetMetadata().GetName())),
				}.Build())
				Expect(listErr).NotTo(HaveOccurred())
				Expect(stored.GetItems()).To(BeEmpty())
				return
			}
			Expect(err).NotTo(HaveOccurred())
			deferCatalogItemFixtureDeletion(func(ctx context.Context) error {
				_, err := catalogs.Delete(ctx, privatev1.BareMetalInstanceCatalogItemsDeleteRequest_builder{Id: catalog.GetObject().GetId()}.Build())
				return err
			}, nil)
			obj := object()
			obj.GetSpec().ClearTemplate()
			obj.GetSpec().ClearDiskImage()
			obj.GetSpec().SetCatalogItem(privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catalog.GetObject().GetId()}.Build())
			expectedType := instanceType.GetId()
			if override {
				expectedType = createCatalogItemBareMetalInstanceTypeFixture(ctx, usersGroup, "arm64")
				obj.GetSpec().SetInstanceType(privatev1.BareMetalInstanceTypeReference_builder{Id: expectedType, Shared: true}.Build())
				obj.GetSpec().SetDiskImage(imageRef)
				compatible = !compatible
			}
			result, _, err := create(ctx, obj, public, false)
			if compatible {
				Expect(err).NotTo(HaveOccurred())
				Expect(result.GetSpec().GetInstanceType().GetId()).To(Equal(expectedType))
				Expect(result.GetSpec().GetDiskImage().GetId()).To(Equal(image.GetId()))
			} else {
				expectMismatch(err)
				expectAbsent(ctx, obj.GetMetadata().GetName())
			}
		}, Entry("locked match", true, false, true), Entry("locked mismatch", true, false, false),
			Entry("editable match", false, false, true), Entry("editable mismatch", false, false, false),
			Entry("incompatible override", false, true, true))

		DescribeTable("rejects without an admitted identity or CR while reconciliation is active", func(ctx context.Context, byName, dryRun bool) {
			control, _, err := create(ctx, object(), public, false)
			Expect(err).NotTo(HaveOccurred())
			var controlCR bmfv1.BareMetalInstance
			Eventually(func(g Gomega) {
				list := &bmfv1.BareMetalInstanceList{}
				g.Expect(tool.KubeClient().List(ctx, list, crclient.InNamespace(hubNamespace), crclient.MatchingLabels{labels.BareMetalInstanceUuid: control.GetId()})).To(Succeed())
				g.Expect(list.Items).To(HaveLen(1))
				controlCR = list.Items[0]
			}, time.Minute, time.Second).Should(Succeed())
			Expect(controlCR.Spec.TemplateID).To(Equal(template))
			Expect(controlCR.Annotations).To(HaveKeyWithValue("osac.openshift.io/tenant", usersGroup))
			original := controlCR.DeepCopy()
			controlCR.Annotations["osac.openshift.io/owner-reference"] = "architecture-control"
			Expect(tool.KubeClient().Patch(ctx, &controlCR, crclient.MergeFrom(original))).To(Succeed())
			_, err = instances.Signal(ctx, privatev1.BareMetalInstancesSignalRequest_builder{Id: control.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			negativeTemplate := createCatalogItemBareMetalInstanceTemplateFixture(ctx, nil, nil, createCatalogItemBareMetalInstanceTypeFixture(ctx, usersGroup, "arm64"))
			obj := object()
			obj.GetSpec().SetTemplate(privatev1.BareMetalInstanceTemplateReference_builder{Id: negativeTemplate, Shared: true}.Build())
			if byName {
				obj.GetSpec().GetDiskImage().SetId("")
				obj.GetSpec().GetDiskImage().SetName(image.GetMetadata().GetName())
			}
			_, _, err = create(ctx, obj, public, dryRun)
			expectMismatch(err)
			checkAbsent := func(g Gomega) {
				expectAbsent(ctx, obj.GetMetadata().GetName())
				list := &bmfv1.BareMetalInstanceList{}
				g.Expect(tool.KubeClient().List(ctx, list, crclient.InNamespace(hubNamespace))).To(Succeed())
				for _, cr := range list.Items {
					g.Expect(cr.Spec.TemplateID).NotTo(Equal(negativeTemplate))
				}
				current := &bmfv1.BareMetalInstance{}
				g.Expect(tool.KubeClient().Get(ctx, crclient.ObjectKeyFromObject(&controlCR), current)).To(Succeed())
				g.Expect(current.Annotations).To(HaveKeyWithValue("osac.openshift.io/tenant", usersGroup))
				g.Expect(current.Annotations).To(HaveKeyWithValue("osac.openshift.io/owner-reference", "architecture-control"))
			}
			Consistently(checkAbsent, 5*time.Second, 100*time.Millisecond).Should(Succeed())
			checkAbsent(Default)
		}, Entry("ID", false, false), Entry("name", true, false), Entry("dry-run ID", false, true), Entry("dry-run name", true, true))

		DescribeTable("preserves lifecycle precedence and warnings", func(ctx context.Context, obsolete, compatible bool) {
			if compatible {
				setArchitectures(ctx, architectures["amd64"], architectures["arm64"])
			} else {
				setArchitectures(ctx, architectures["arm64"])
			}
			lifecycle := privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_DEPRECATED
			if obsolete {
				lifecycle = privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_OBSOLETE
			}
			image.GetSpec().SetLifecycle(lifecycle)
			_, err := images.Update(ctx, privatev1.DiskImagesUpdateRequest_builder{Object: image, UpdateMask: catalogItemUpdateMask("spec.lifecycle")}.Build())
			Expect(err).NotTo(HaveOccurred())
			obj := object()
			_, warnings, err := create(ctx, obj, public, false)
			if obsolete {
				Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
				Expect(err.Error()).NotTo(ContainSubstring("INCOMPATIBLE_DISK_IMAGE_ARCHITECTURE"))
			} else if compatible {
				Expect(err).NotTo(HaveOccurred())
				Expect(warnings).To(HaveLen(1))
				Expect(warnings[0]).To(ContainSubstring("deprecated"))
			} else {
				expectMismatch(err)
			}
			if err != nil {
				expectAbsent(ctx, obj.GetMetadata().GetName())
			}
		}, Entry("obsolete match", true, true), Entry("obsolete mismatch", true, false), Entry("deprecated match", false, true), Entry("deprecated mismatch", false, false))

	}, Entry("private", false), Entry("public", true))

	DescribeTable("hides cross-tenant image architecture", func(ctx context.Context, byName, compatible bool) {
		tenant, _ := createCatalogItemTenantAdminFixture(ctx)
		hidden := createCatalogItemDiskImageFixture(ctx, tenant, catalogItemFixtureName())
		if !compatible {
			hidden.GetSpec().SetArchitecture([]privatev1.Architecture{architectures["arm64"]})
			_, err := images.Update(ctx, privatev1.DiskImagesUpdateRequest_builder{Object: hidden, UpdateMask: catalogItemUpdateMask("spec.architecture")}.Build())
			Expect(err).NotTo(HaveOccurred())
		}
		obj := object()
		ref := privatev1.DiskImageReference_builder{Id: hidden.GetId()}.Build()
		if byName {
			ref.SetId("")
			ref.SetName(hidden.GetMetadata().GetName())
		}
		obj.GetSpec().SetDiskImage(ref)
		_, _, err := create(ctx, obj, true, false)
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(status.Convert(err).Message()).To(And(HavePrefix("reference validation failed:"), ContainSubstring("object.spec.disk_image"), ContainSubstring("not found")))
		Expect(err.Error()).NotTo(ContainSubstring("INCOMPATIBLE_DISK_IMAGE_ARCHITECTURE"))
		Expect(err.Error()).NotTo(ContainSubstring("quay.io"))
		expectAbsent(ctx, obj.GetMetadata().GetName())
	}, Entry("matching ID", false, true), Entry("mismatching ID", false, false), Entry("matching name", true, true), Entry("mismatching name", true, false))
})
