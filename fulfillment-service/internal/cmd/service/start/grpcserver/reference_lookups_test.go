/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package grpcserver

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/references"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	testsv1 "github.com/osac-project/osac/proto/gen/osac/tests/v1"
)

var _ = Describe("RegisterReferenceLookups", func() {
	var validator *references.ReferenceValidator

	BeforeEach(func() {
		validator = newTestReferenceValidator()
	})

	It("registers SecretLocalReference for private and public APIs", func() {
		for _, name := range []protoreflect.FullName{
			"osac.private.v1.SecretLocalReference",
			"osac.public.v1.SecretLocalReference",
		} {
			Expect(validator.HasLookup(name)).To(BeTrue(), "missing lookup for %s", name)
		}
	})

	It("registers lookups for reference uses not handled by their server", func() {
		var missing []string
		for _, use := range createOrUpdateReferenceUses() {
			if isHandlerOwnedReferenceUse(use) {
				continue
			}
			if !validator.HasLookup(use.typeName) {
				missing = append(missing, fmt.Sprintf("%s %s (%s)", use.method, use.path, use.typeName))
			}
		}
		slices.Sort(missing)
		Expect(missing).To(BeEmpty(),
			"no lookup registered for Create/Update reference uses:\n  %s",
			strings.Join(missing, "\n  "))
	})

	It("discovers reference types through direct and nested map values", func() {
		file := protodesc.ToFileDescriptorProto(testsv1.File_osac_tests_v1_reference_test_types_proto)
		for _, message := range file.GetMessageType() {
			if message.GetName() == "TestRefSpec" {
				message.Field = slices.DeleteFunc(message.Field, func(field *descriptorpb.FieldDescriptorProto) bool {
					return field.GetName() != "targets" && field.GetName() != "attachments"
				})
			}
		}
		descriptor, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
		Expect(err).ToNot(HaveOccurred())
		refs := map[protoreflect.FullName]struct{}{}
		var uses []referenceUse
		collectReferenceUses(descriptor.Messages().ByName("TestRefSpec"), "", "", map[protoreflect.FullName]struct{}{}, &uses)
		for _, use := range uses {
			refs[use.typeName] = struct{}{}
		}
		Expect(refs).To(HaveKey(protoreflect.FullName("osac.tests.v1.TestTargetReference")))
		Expect(refs).To(HaveKey(protoreflect.FullName("osac.tests.v1.TestTargetLocalReference")))
		Expect(refs).To(HaveKey(protoreflect.FullName("osac.tests.v1.TestOtherTargetLocalReference")))
	})

	It("classifies handler-owned SecretReference paths for lookup coverage", func() {
		Expect(isHandlerOwnedReferenceUse(referenceUse{
			method:   publicv1.ComputeInstances_Create_FullMethodName,
			path:     "object.spec.ssh_key",
			typeName: "osac.public.v1.SecretReference",
		})).To(BeTrue())
		Expect(isHandlerOwnedReferenceUse(referenceUse{
			method:   publicv1.ComputeInstances_Create_FullMethodName,
			path:     "object.spec.other",
			typeName: "osac.public.v1.SecretReference",
		})).To(BeFalse())
		Expect(isHandlerOwnedReferenceUse(referenceUse{
			method:   publicv1.ComputeInstanceCatalogItems_Create_FullMethodName,
			path:     "object.fields.ssh_key.editable.default_value",
			typeName: "osac.public.v1.SecretReference",
		})).To(BeTrue())
	})

	DescribeTableSubtree("CaaS NodeSet reference interception", func(layer, method string) {
		var request proto.Message
		var v *references.ReferenceValidator
		var info *grpc.UnaryServerInfo

		BeforeEach(func() {
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			tenancy, err := auth.NewGuestTenancyLogic().SetLogger(logger).Build()
			Expect(err).ToNot(HaveOccurred())
			v, err = newReferenceValidator(logger, tenancy, prometheus.NewRegistry())
			Expect(err).ToNot(HaveOccurred())
			prefix := "osac." + layer + ".v1."
			requestType, err := protoregistry.GlobalTypes.FindMessageByName(protoreflect.FullName(prefix + "Clusters" + method + "Request"))
			Expect(err).ToNot(HaveOccurred())
			request = requestType.New().Interface()
			body := `{"object":{"spec":{"nodeSets":{"workers":{"size":1,"baremetalInstanceType":{"name":"worker-type","shared":true}}}}}}`
			if method == "Update" {
				body = `{"object":{"id":"cluster-id","spec":{"nodeSets":{"workers":{"size":1,"baremetalInstanceType":{"name":"worker-type","shared":true}}}}},"updateMask":"spec.nodeSets"}`
			}
			Expect(protojson.Unmarshal([]byte(body), request)).To(Succeed())
			info = &grpc.UnaryServerInfo{FullMethod: "/" + prefix + "Clusters/" + method}
		})

		It("resolves hardware names in the common interceptor before the handler", func() {
			lookupCalls := 0
			v.Register(protoreflect.FullName("osac."+layer+".v1.BareMetalInstanceTypeReference"),
				func(_ context.Context, tenant, project, id, name string) (*references.ResolvedRef, error) {
					lookupCalls++
					Expect(tenant).To(Equal(auth.SharedTenant))
					Expect(project).To(BeEmpty())
					Expect(id).To(BeEmpty())
					Expect(name).To(Equal("worker-type"))
					return &references.ResolvedRef{ID: "worker-type-id", Name: name, Tenant: auth.SharedTenant}, nil
				})
			called := false
			_, err := v.UnaryServer(ctx, request, info, func(_ context.Context, actual any) (any, error) {
				called = true
				message := actual.(proto.Message).ProtoReflect()
				object := message.Get(message.Descriptor().Fields().ByName("object")).Message()
				spec := object.Get(object.Descriptor().Fields().ByName("spec")).Message()
				node := spec.Get(spec.Descriptor().Fields().ByName("node_sets")).Map().Get(protoreflect.ValueOfString("workers").MapKey()).Message()
				ref := node.Get(node.Descriptor().Fields().ByName("baremetal_instance_type")).Message()
				Expect(ref.Get(ref.Descriptor().Fields().ByName("id")).String()).To(Equal("worker-type-id"))
				return "ok", nil
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(called).To(BeTrue())
			Expect(lookupCalls).To(Equal(1))
		})

		It("rejects missing hardware through the registered DAO lookup before the handler", func() {
			body, err := protojson.Marshal(request)
			Expect(err).ToNot(HaveOccurred())
			_, err = invokeCatalogRPC(layer, "Clusters", method, string(body))
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(err).To(MatchError(ContainSubstring(`reference validation failed: object.spec.node_sets["workers"].baremetal_instance_type`)))
		})
	},
		Entry("public create", "public", "Create"),
		Entry("private create", "private", "Create"),
		Entry("public update", "public", "Update"),
		Entry("private update", "private", "Update"),
	)

	DescribeTable("leaves CaaS catalog NodeSet policy maps for post-merge handler resolution", func(layer, method, behavior string) {
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		tenancy, err := auth.NewGuestTenancyLogic().SetLogger(logger).Build()
		Expect(err).ToNot(HaveOccurred())
		v, err := newReferenceValidator(logger, tenancy, prometheus.NewRegistry())
		Expect(err).ToNot(HaveOccurred())
		prefix := "osac." + layer + ".v1."
		requestType, err := protoregistry.GlobalTypes.FindMessageByName(protoreflect.FullName(prefix + "ClusterCatalogItems" + method + "Request"))
		Expect(err).ToNot(HaveOccurred())
		request := requestType.New().Interface()
		nodeMap := `{"items":{"workers":{"size":1,"baremetalInstanceType":{"name":"unresolved-type","shared":true}}}}`
		if behavior == "editable" {
			nodeMap = `{"defaultValue":` + nodeMap + `}`
		}
		body := fmt.Sprintf(`{"object":{"fields":{"nodeSets":{%q:%s}}}}`, behavior, nodeMap)
		Expect(protojson.Unmarshal([]byte(body), request)).To(Succeed())
		original := proto.Clone(request)
		called := false
		_, err = v.UnaryServer(ctx, request, &grpc.UnaryServerInfo{FullMethod: "/" + prefix + "ClusterCatalogItems/" + method},
			func(_ context.Context, actual any) (any, error) {
				called = true
				Expect(proto.Equal(actual.(proto.Message), original)).To(BeTrue())
				return "ok", nil
			})
		Expect(err).ToNot(HaveOccurred())
		Expect(called).To(BeTrue())
	},
		Entry("public create locked", "public", "Create", "locked"),
		Entry("private create locked", "private", "Create", "locked"),
		Entry("public update locked", "public", "Update", "locked"),
		Entry("private update locked", "private", "Update", "locked"),
		Entry("public create default", "public", "Create", "editable"),
		Entry("private create default", "private", "Create", "editable"),
		Entry("public update default", "public", "Update", "editable"),
		Entry("private update default", "private", "Update", "editable"),
	)

	It("leaves BMI Create network references for the tenant-scoped handler to resolve", func() {
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		tenancy, err := auth.NewGuestTenancyLogic().SetLogger(logger).Build()
		Expect(err).NotTo(HaveOccurred())
		v, err := newReferenceValidator(logger, tenancy, prometheus.NewRegistry())
		Expect(err).NotTo(HaveOccurred())

		request := privatev1.BareMetalInstancesCreateRequest_builder{Object: privatev1.BareMetalInstance_builder{
			Metadata: privatev1.Metadata_builder{Tenant: "tenant-b"}.Build(),
			Spec: privatev1.BareMetalInstanceSpec_builder{NetworkAttachments: []*privatev1.BareMetalNetworkAttachment{
				privatev1.BareMetalNetworkAttachment_builder{
					Subnet: privatev1.SubnetLocalReference_builder{Name: "default-ipv4"}.Build(),
					SecurityGroups: []*privatev1.SecurityGroupLocalReference{
						privatev1.SecurityGroupLocalReference_builder{Name: "default"}.Build(),
					},
				}.Build(),
			}}.Build(),
		}.Build()}.Build()
		called := false
		_, err = v.UnaryServer(context.Background(), request,
			&grpc.UnaryServerInfo{FullMethod: privatev1.BareMetalInstances_Create_FullMethodName},
			func(_ context.Context, actual any) (any, error) {
				called = true
				attachment := actual.(*privatev1.BareMetalInstancesCreateRequest).GetObject().GetSpec().GetNetworkAttachments()[0]
				Expect(attachment.GetSubnet().GetId()).To(BeEmpty(), "interceptor must not resolve a same-named subnet in another tenant")
				Expect(attachment.GetSubnet().GetName()).To(Equal("default-ipv4"))
				Expect(attachment.GetSecurityGroups()[0].GetId()).To(BeEmpty(), "security group must also be resolved after tenant assignment")
				Expect(attachment.GetSecurityGroups()[0].GetName()).To(Equal("default"))
				return "ok", nil
			})
		Expect(err).NotTo(HaveOccurred())
		Expect(called).To(BeTrue(), "network references must reach handler for post-ownership scoped resolution")
	})

	It("leaves public BMI Create network references for the private handler to resolve", func() {
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		tenancy, err := auth.NewGuestTenancyLogic().SetLogger(logger).Build()
		Expect(err).NotTo(HaveOccurred())
		v, err := newReferenceValidator(logger, tenancy, prometheus.NewRegistry())
		Expect(err).NotTo(HaveOccurred())
		request := publicv1.BareMetalInstancesCreateRequest_builder{Object: publicv1.BareMetalInstance_builder{
			Spec: publicv1.BareMetalInstanceSpec_builder{NetworkAttachments: []*publicv1.BareMetalNetworkAttachment{
				publicv1.BareMetalNetworkAttachment_builder{
					Subnet: publicv1.SubnetLocalReference_builder{Name: "default-ipv4"}.Build(),
				}.Build(),
			}}.Build(),
		}.Build()}.Build()
		called := false
		_, err = v.UnaryServer(context.Background(), request,
			&grpc.UnaryServerInfo{FullMethod: publicv1.BareMetalInstances_Create_FullMethodName},
			func(_ context.Context, actual any) (any, error) {
				called = true
				Expect(actual.(*publicv1.BareMetalInstancesCreateRequest).GetObject().GetSpec().GetNetworkAttachments()[0].GetSubnet().GetId()).To(BeEmpty())
				return "ok", nil
			})
		Expect(err).NotTo(HaveOccurred())
		Expect(called).To(BeTrue())
	})

	It("does not reject identity provider client_secret_secret as unregistered", func() {
		request := privatev1.IdentityProvidersCreateRequest_builder{
			Object: privatev1.IdentityProvider_builder{
				Spec: privatev1.IdentityProviderSpec_builder{
					Oidc: privatev1.OidcConfig_builder{
						ClientSecretSecret: privatev1.SecretLocalReference_builder{
							Id: "secret-id",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build()

		_, err := validator.UnaryServer(
			context.Background(),
			request,
			&grpc.UnaryServerInfo{FullMethod: "/osac.private.v1.IdentityProviders/Create"},
			func(context.Context, any) (any, error) { return "ok", nil },
		)
		if err != nil {
			st, _ := grpcstatus.FromError(err)
			Expect(st.Message()).ToNot(ContainSubstring("no lookup registered"),
				"SecretLocalReference is not registered with the interceptor: %v", err)
		}
	})
})

type referenceUse struct {
	method   string
	path     string
	typeName protoreflect.FullName
}

// isHandlerOwnedReferenceUse reports whether lookup coverage should exclude a
// reference use because its handler resolves or intentionally preserves it.
func isHandlerOwnedReferenceUse(use referenceUse) bool {
	if isHandlerOwnedReferenceType(use.typeName) {
		return true
	}

	switch use.typeName {
	case "osac.private.v1.SecretReference", "osac.public.v1.SecretReference":
		if slices.Contains(computeInstanceSSHKeyMethods(), use.method) && use.path == "object.spec.ssh_key" {
			return true
		}
		return slices.Contains(catalogAuthoringMethods(), use.method) &&
			(use.path == "object.fields" || strings.HasPrefix(use.path, "object.fields."))
	default:
		return false
	}
}

func isHandlerOwnedReferenceType(name protoreflect.FullName) bool {
	return name == "osac.private.v1.AddOnOperatorReference" ||
		name == "osac.public.v1.AddOnOperatorReference"
}

func newTestReferenceValidator() *references.ReferenceValidator {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tenancy, err := auth.NewGuestTenancyLogic().SetLogger(logger).Build()
	Expect(err).ToNot(HaveOccurred())
	validator, err := references.NewReferenceValidator().SetLogger(logger).Build()
	Expect(err).ToNot(HaveOccurred())
	err = registerReferenceLookups(validator, logger, tenancy, prometheus.NewRegistry())
	Expect(err).ToNot(HaveOccurred())
	return validator
}

func createOrUpdateReferenceUses() []referenceUse {
	var refs []referenceUse
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		pkg := string(fd.Package())
		if pkg != "osac.private.v1" && pkg != "osac.public.v1" {
			return true
		}
		services := fd.Services()
		for i := 0; i < services.Len(); i++ {
			methods := services.Get(i).Methods()
			for j := 0; j < methods.Len(); j++ {
				method := methods.Get(j)
				methodName := string(method.Name())
				if methodName != "Create" && methodName != "Update" {
					continue
				}
				fullMethod := "/" + string(services.Get(i).FullName()) + "/" + methodName
				collectReferenceUses(method.Input(), fullMethod, "", map[protoreflect.FullName]struct{}{}, &refs)
			}
		}
		return true
	})
	return refs
}

func collectReferenceUses(
	md protoreflect.MessageDescriptor,
	method, path string,
	visiting map[protoreflect.FullName]struct{},
	refs *[]referenceUse,
) {
	if md == nil {
		return
	}
	name := md.FullName()
	if _, ok := visiting[name]; ok {
		return
	}
	visiting[name] = struct{}{}
	defer delete(visiting, name)

	fields := md.Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		if fd.Kind() != protoreflect.MessageKind {
			continue
		}
		fieldPath := string(fd.Name())
		if path != "" {
			fieldPath = path + "." + fieldPath
		}
		msg := fd.Message()
		if fd.IsMap() {
			msg = fd.MapValue().Message()
			if msg == nil {
				continue
			}
		}
		if strings.HasSuffix(string(msg.FullName()), "Reference") {
			*refs = append(*refs, referenceUse{
				method:   method,
				path:     fieldPath,
				typeName: msg.FullName(),
			})
			continue
		}
		collectReferenceUses(msg, method, fieldPath, visiting, refs)
	}
}
