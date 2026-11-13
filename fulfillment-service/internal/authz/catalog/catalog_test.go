/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package catalog

import (
	"maps"
	"slices"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	// These imports ensure that the descriptors of both API layers are registered in the global
	// registry used by the tests of this suite.
	_ "github.com/osac-project/osac/proto/gen/osac/private/v1"
	_ "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// stubFile creates a self-contained file descriptor for the given package, containing one service
// per entry of the given map, with one request and response message per method.
func stubFile(name string, packageName string, services map[string][]string) *descriptorpb.FileDescriptorProto {
	file := &descriptorpb.FileDescriptorProto{
		Name:    proto.String(name),
		Package: proto.String(packageName),
		Syntax:  proto.String("proto3"),
	}
	for _, serviceName := range slices.Sorted(maps.Keys(services)) {
		service := &descriptorpb.ServiceDescriptorProto{
			Name: proto.String(serviceName),
		}
		for _, methodName := range services[serviceName] {
			requestName := serviceName + methodName + "Request"
			responseName := serviceName + methodName + "Response"
			file.MessageType = append(
				file.MessageType,
				&descriptorpb.DescriptorProto{Name: proto.String(requestName)},
				&descriptorpb.DescriptorProto{Name: proto.String(responseName)},
			)
			service.Method = append(
				service.Method,
				&descriptorpb.MethodDescriptorProto{
					Name:       proto.String(methodName),
					InputType:  proto.String("." + packageName + "." + requestName),
					OutputType: proto.String("." + packageName + "." + responseName),
				},
			)
		}
		file.Service = append(file.Service, service)
	}
	return file
}

// newStubRegistry creates an isolated protobuf registry containing the given files.
func newStubRegistry(files ...*descriptorpb.FileDescriptorProto) *protoregistry.Files {
	result, err := protodesc.NewFiles(&descriptorpb.FileDescriptorSet{File: files})
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return result
}

var _ = Describe("Permission catalog", func() {
	Describe("Building from the global registry", func() {
		var permissionCatalog *Catalog

		BeforeEach(func() {
			var err error
			permissionCatalog, err = Build(protoregistry.GlobalFiles)
			Expect(err).ToNot(HaveOccurred())
		})

		It("Contains every grantable service and method of both API layers", func() {
			expectedServices := map[string]bool{}
			expectedMethods := map[string]bool{}
			protoregistry.GlobalFiles.RangeFiles(func(fileDesc protoreflect.FileDescriptor) bool {
				packageDesc := fileDesc.Package()
				if packageDesc != publicPackage && packageDesc != privatePackage {
					return true
				}
				serviceDescs := fileDesc.Services()
				for i := range serviceDescs.Len() {
					serviceDesc := serviceDescs.Get(i)
					if _, ok := excludedServices[serviceDesc.Name()]; ok {
						continue
					}
					expectedServices[serviceRuleName(packageDesc, serviceDesc.Name())] = true
					methodDescs := serviceDesc.Methods()
					for j := range methodDescs.Len() {
						expectedMethods[fullMethod(serviceDesc, methodDescs.Get(j))] = true
					}
				}
				return true
			})
			Expect(expectedServices).ToNot(BeEmpty())

			Expect(permissionCatalog.Services()).To(ConsistOf(slices.Collect(maps.Keys(expectedServices))))

			granted, err := permissionCatalog.Expand(AllServicesWildcard, []string{AllMethodsWildcard})
			Expect(err).ToNot(HaveOccurred())
			Expect(granted).To(ConsistOf(slices.Collect(maps.Keys(expectedMethods))))
		})

		// The rule name drops the version segment and everything is lower case,
		// including acronyms.
		DescribeTable("Derives the rule names",
			func(service string, fullName string) {
				serviceDesc := permissionCatalog.LookupService(service)
				Expect(serviceDesc).ToNot(BeNil(), "service %q should be cataloged", service)
				Expect(serviceDesc.FullName()).To(Equal(fullName))
			},
			Entry("Role bindings", "osac.public.rolebindings", "osac.public.v1.RoleBindings"),
			Entry("NAT gateways", "osac.private.natgateways", "osac.private.v1.NATGateways"),
			Entry("Compute instances", "osac.public.computeinstances", "osac.public.v1.ComputeInstances"),
			Entry("Installation settings, private only", "osac.private.installationsettings",
				"osac.private.v1.InstallationSettings"),
		)

		It("Maps methods to full gRPC methods", func() {
			serviceDesc := permissionCatalog.LookupService("osac.public.rolebindings")
			Expect(serviceDesc).ToNot(BeNil())
			fullMethod, ok := serviceDesc.Method("get")
			Expect(ok).To(BeTrue())
			Expect(fullMethod).To(Equal("osac.public.v1.RoleBindings/Get"))
			fullMethod, ok = serviceDesc.Method("nope")
			Expect(ok).To(BeFalse())
			Expect(fullMethod).To(BeEmpty())
		})

		It("Catalogs both API layers of services that exist in both", func() {
			Expect(permissionCatalog.LookupService("osac.public.volumes")).ToNot(BeNil())
			Expect(permissionCatalog.LookupService("osac.private.volumes")).ToNot(BeNil())
		})

		It("Catalogs methods that aren't part of the object shape", func() {
			events := permissionCatalog.LookupService("osac.public.events")
			Expect(events).ToNot(BeNil())
			_, ok := events.Method("watch")
			Expect(ok).To(BeTrue(), "Events.Watch should be grantable")

			serviceAccounts := permissionCatalog.LookupService("osac.public.serviceaccounts")
			Expect(serviceAccounts).ToNot(BeNil())
			_, ok = serviceAccounts.Method("regeneratesecret")
			Expect(ok).To(BeTrue(), "ServiceAccounts.RegenerateSecret should be grantable")

			reviews := permissionCatalog.LookupService("osac.public.selfsubjectaccessreviews")
			Expect(reviews).ToNot(BeNil())
			_, ok = reviews.Method("create")
			Expect(ok).To(BeTrue(), "SelfSubjectAccessReviews.Create should be grantable")
		})

		It("Excludes the capabilities services", func() {
			Expect(permissionCatalog.LookupService("osac.public.capabilities")).To(BeNil())
			Expect(permissionCatalog.LookupService("osac.private.capabilities")).To(BeNil())
		})

		It("Sorts the services and the methods", func() {
			services := permissionCatalog.Services()
			Expect(services).ToNot(BeEmpty())
			Expect(slices.IsSorted(services)).To(BeTrue())

			serviceDesc := permissionCatalog.LookupService("osac.public.rolebindings")
			methods := serviceDesc.Methods()
			Expect(methods).ToNot(BeEmpty())
			Expect(slices.IsSorted(methods)).To(BeTrue())
		})
	})

	Describe("Building from a test registry", func() {
		It("Includes the services of the scanned registry", func() {
			registry := newStubRegistry(stubFile("stubs.proto", "osac.public.v1", map[string][]string{
				"StubWidgets": {"Get", "List", "Create"},
			}))

			permissionCatalog, err := Build(registry)
			Expect(err).ToNot(HaveOccurred())

			serviceDesc := permissionCatalog.LookupService("osac.public.stubwidgets")
			Expect(serviceDesc).ToNot(BeNil())
			Expect(serviceDesc.FullName()).To(Equal("osac.public.v1.StubWidgets"))
			Expect(serviceDesc.Methods()).To(ConsistOf("create", "get", "list"))
			fullMethod, ok := serviceDesc.Method("get")
			Expect(ok).To(BeTrue())
			Expect(fullMethod).To(Equal("osac.public.v1.StubWidgets/Get"))
		})

		It("Removes the services when their descriptors are removed on rebuild", func() {
			withStubs := newStubRegistry(
				stubFile("widgets.proto", "osac.public.v1", map[string][]string{
					"StubWidgets": {"Get", "List"},
				}),
				stubFile("gadgets.proto", "osac.private.v1", map[string][]string{
					"StubGadgets": {"Get"},
				}),
			)
			permissionCatalog, err := Build(withStubs)
			Expect(err).ToNot(HaveOccurred())
			Expect(permissionCatalog.LookupService("osac.public.stubwidgets")).ToNot(BeNil())
			Expect(permissionCatalog.LookupService("osac.private.stubgadgets")).ToNot(BeNil())

			withoutWidgets := newStubRegistry(
				stubFile("gadgets.proto", "osac.private.v1", map[string][]string{
					"StubGadgets": {"Get"},
				}),
			)
			permissionCatalog, err = Build(withoutWidgets)
			Expect(err).ToNot(HaveOccurred())
			Expect(permissionCatalog.LookupService("osac.public.stubwidgets")).To(BeNil())
			Expect(permissionCatalog.LookupService("osac.private.stubgadgets")).ToNot(BeNil())
		})

		It("Ignores files of other packages", func() {
			registry := newStubRegistry(
				stubFile("widgets.proto", "osac.public.v1", map[string][]string{
					"StubWidgets": {"Get"},
				}),
				stubFile("other.proto", "osac.tests.v1", map[string][]string{
					"OtherThings": {"Get"},
				}),
			)

			permissionCatalog, err := Build(registry)
			Expect(err).ToNot(HaveOccurred())
			Expect(permissionCatalog.Services()).To(ConsistOf("osac.public.stubwidgets"))
		})

		It("Rejects services that merge under lowercasing", func() {
			registry := newStubRegistry(stubFile("clash.proto", "osac.public.v1", map[string][]string{
				"StubWidgets": {"Get"},
				"STUBWIDGETS": {"Get"},
			}))

			_, err := Build(registry)
			Expect(err).To(MatchError(
				`services "osac.public.v1.STUBWIDGETS" and "osac.public.v1.StubWidgets" both map to rule name "osac.public.stubwidgets"`,
			))
		})

		It("Rejects methods that merge under lowercasing", func() {
			registry := newStubRegistry(stubFile("clash.proto", "osac.public.v1", map[string][]string{
				"StubWidgets": {"Get", "GET"},
			}))

			_, err := Build(registry)
			Expect(err).To(MatchError(
				`methods "Get" and "GET" of service "osac.public.v1.StubWidgets" both map to rule name "get"`,
			))
		})

		It("Rejects a nil registry", func() {
			_, err := Build(nil)
			Expect(err).To(MatchError("registry is mandatory"))
		})
	})
})
