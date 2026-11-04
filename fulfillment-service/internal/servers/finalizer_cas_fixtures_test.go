/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"fmt"
	"sort"

	"buf.build/go/protovalidate"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// Fixtures for the finalizer compare-and-swap specs. Every private resource that can carry finalizers has
// one finalizerCase. The specs run each scenario against the bare GenericServer of the resource, which is the single
// choke point that all private Update RPCs go through.

const (
	// finalizerController stands for the finalizer that a controller keeps on an object for its whole life.
	finalizerController = "fulfillment-controller"

	// finalizerHold stands for a finalizer that is added and later released by another writer.
	finalizerHold = "test.osac.io/hold"

	// finalizerForeign stands for a finalizer owned by a writer that the test doesn't control.
	finalizerForeign = "test.osac.io/foreign"
)

// finalizerCaseSkips lists the resources that the specs can't cover, by fully qualified service name, with the reason
// and the location of the constraint that blocks seeding. It is empty when every resource is covered.
var finalizerCaseSkips = map[string]string{}

// finalizerBearingServices returns the fully qualified names of the private services that have an Update method whose
// object has metadata with finalizers. Services without Update are read-only and are excluded by construction.
func finalizerBearingServices() []string {
	var result []string
	packageName := (privatev1.EventType)(0).Descriptor().FullName().Parent()
	protoregistry.GlobalFiles.RangeFilesByPackage(packageName, func(file protoreflect.FileDescriptor) bool {
		services := file.Services()
		for i := range services.Len() {
			service := services.Get(i)
			update := service.Methods().ByName("Update")
			if update == nil {
				continue
			}
			object := update.Input().Fields().ByName("object")
			if object == nil || object.Message() == nil {
				continue
			}
			metadata := object.Message().Fields().ByName("metadata")
			if metadata == nil || metadata.Message() == nil {
				continue
			}
			if metadata.Message().Fields().ByName("finalizers") == nil {
				continue
			}
			result = append(result, string(service.FullName()))
		}
		return true
	})
	sort.Strings(result)
	return result
}

// finalizerObject is the constraint satisfied by every private object that has metadata.
type finalizerObject interface {
	dao.Object
	GetMetadata() *privatev1.Metadata
	SetMetadata(*privatev1.Metadata)
}

// finalizerOps is the non-generic view of a GenericServer used by the specs. Objects are passed as proto.Message and
// must be of the concrete type of the resource.
type finalizerOps interface {
	get(ctx context.Context, id string) (proto.Message, error)
	update(ctx context.Context, object proto.Message, mask *fieldmaskpb.FieldMask, lock bool) error
	del(ctx context.Context, id string) error
}

// finalizerUpdateRequest is implemented by the generated update request of every resource.
type finalizerUpdateRequest[O any] interface {
	proto.Message
	SetObject(O)
	SetUpdateMask(*fieldmaskpb.FieldMask)
	SetLock(bool)
}

// finalizerIDRequest is implemented by the generated get and delete requests of every resource.
type finalizerIDRequest interface {
	proto.Message
	SetId(string)
}

// genericFinalizerOps drives a GenericServer through typed requests cloned from its own request templates.
type genericFinalizerOps[O finalizerObject] struct {
	server *GenericServer[O]
}

func (o *genericFinalizerOps[O]) get(ctx context.Context, id string) (proto.Message, error) {
	request := proto.Clone(o.server.getRequest).(finalizerIDRequest)
	request.SetId(id)
	var response proto.Message
	err := o.server.Get(ctx, request, &response)
	if err != nil {
		return nil, err
	}
	return response.(interface{ GetObject() O }).GetObject(), nil
}

func (o *genericFinalizerOps[O]) update(ctx context.Context, object proto.Message, mask *fieldmaskpb.FieldMask,
	lock bool) error {
	request := proto.Clone(o.server.updateRequest).(finalizerUpdateRequest[O])
	request.SetObject(object.(O))
	request.SetUpdateMask(mask)
	request.SetLock(lock)
	var response proto.Message
	return o.server.Update(ctx, request, &response)
}

func (o *genericFinalizerOps[O]) del(ctx context.Context, id string) error {
	request := proto.Clone(o.server.deleteRequest).(finalizerIDRequest)
	request.SetId(id)
	var response proto.Message
	return o.server.Delete(ctx, request, &response)
}

// finalizerTarget is a seeded object together with the operations of its server.
type finalizerTarget struct {
	ops finalizerOps
	id  string
}

// finalizerCase describes how to seed and serve one resource. The target function builds the server and seeds an
// object that carries the given finalizers from its creation, so its version is zero. It reads the suite context when
// it is called, never when the table is built.
type finalizerCase struct {
	service string
	target  func(finalizers []string) finalizerTarget
}

// newFinalizerCase creates the case for a resource. The build function returns the object to store, after saving
// whatever parents it needs; it must set the metadata. The options mirror the ones that the production server of the
// resource sets on its generic server.
func newFinalizerCase[O finalizerObject](desc grpc.ServiceDesc, build func() O,
	options ...func(*GenericServerBuilder[O])) finalizerCase {
	return finalizerCase{
		service: desc.ServiceName,
		target: func(finalizers []string) finalizerTarget {
			GinkgoHelper()
			builder := NewGenericServer[O]().
				SetLogger(logger).
				SetService(desc.ServiceName).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy)
			for _, option := range options {
				option(builder)
			}
			server, err := builder.Build()
			Expect(err).ToNot(HaveOccurred())

			// The DAO doesn't assign a tenant, the server does, so default it as the server would:
			object := build()
			if object.GetMetadata().GetTenant() == "" {
				object.GetMetadata().SetTenant(testTenant)
			}
			if len(finalizers) > 0 {
				object.GetMetadata().SetFinalizers(finalizers)
			}
			created, err := fixtureDAO[O]().Create().SetObject(object).Do(ctx)
			Expect(err).ToNot(HaveOccurred(), "fixture for %s must be creatable", desc.ServiceName)
			Expect(protovalidate.Validate(created.GetObject())).To(Succeed(),
				"fixture for %s must pass protovalidate, because Update validates the merged object", desc.ServiceName)
			return finalizerTarget{
				ops: &genericFinalizerOps[O]{server: server},
				id:  created.GetObject().GetId(),
			}
		},
	}
}

// afterCreate returns a copy of the case that runs the given function with the identifier of the object after it has
// been stored. Use it for state that the database adds around the object and that the specs need to remove.
func (c finalizerCase) afterCreate(hook func(id string)) finalizerCase {
	inner := c.target
	c.target = func(finalizers []string) finalizerTarget {
		GinkgoHelper()
		result := inner(finalizers)
		hook(result.id)
		return result
	}
	return c
}

// finalizerName returns a unique name for a fixture.
func finalizerName(prefix string) string {
	return fmt.Sprintf("%s-%s", prefix, uuid.NewString()[:8])
}

// finalizerCases returns the cases for all the resources that the specs cover. The leaf resources come first: they
// need no parent objects. The rest are in the groups below.
func finalizerCases() []finalizerCase {
	return append(append(append([]finalizerCase{
		newFinalizerCase(privatev1.BareMetalInstanceTemplates_ServiceDesc,
			func() *privatev1.BareMetalInstanceTemplate {
				return privatev1.BareMetalInstanceTemplate_builder{
					Id: fmt.Sprintf("finalizer_cas_%s", uuid.NewString()[:8]),
					Metadata: privatev1.Metadata_builder{
						Name: finalizerName("template"),
					}.Build(),
					Description: "finalizer CAS fixture",
				}.Build()
			},
			func(b *GenericServerBuilder[*privatev1.BareMetalInstanceTemplate]) {
				b.AddAllowedTenants(auth.SharedTenant)
			},
		),
		newFinalizerCase(privatev1.Hubs_ServiceDesc,
			func() *privatev1.Hub {
				return privatev1.Hub_builder{
					Metadata: privatev1.Metadata_builder{
						Name: finalizerName("hub"),
					}.Build(),
					Spec: privatev1.HubSpec_builder{
						Kubeconfig: []byte("my_config"),
						Namespace:  "my_ns",
					}.Build(),
				}.Build()
			},
			func(b *GenericServerBuilder[*privatev1.Hub]) {
				b.AddAllowedTenants(auth.SharedTenant)
			},
		),
		newFinalizerCase(privatev1.Roles_ServiceDesc,
			func() *privatev1.Role {
				return privatev1.Role_builder{
					Metadata: privatev1.Metadata_builder{
						Name: finalizerName("role"),
					}.Build(),
				}.Build()
			},
			func(b *GenericServerBuilder[*privatev1.Role]) {
				b.AddAllowedTenants(auth.SharedTenant, auth.SystemTenant)
			},
		),
		newFinalizerCase(privatev1.Users_ServiceDesc,
			func() *privatev1.User {
				return privatev1.User_builder{
					Metadata: privatev1.Metadata_builder{
						Name: finalizerName("user"),
					}.Build(),
				}.Build()
			},
			func(b *GenericServerBuilder[*privatev1.User]) {
				b.AddAllowedTenants(auth.SharedTenant)
			},
		),
		newFinalizerCase(privatev1.ExternalIPPools_ServiceDesc,
			func() *privatev1.ExternalIPPool {
				return privatev1.ExternalIPPool_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   finalizerName("pool"),
						Tenant: auth.SharedTenant,
					}.Build(),
					Spec: privatev1.ExternalIPPoolSpec_builder{
						Cidrs:    []string{"203.0.113.0/24"},
						IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
					}.Build(),
					Status: privatev1.ExternalIPPoolStatus_builder{
						State:     privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY,
						Hub:       "network-hub-a",
						Total:     10,
						Available: 10,
					}.Build(),
				}.Build()
			},
			func(b *GenericServerBuilder[*privatev1.ExternalIPPool]) {
				b.AddAllowedTenants(auth.SharedTenant)
			},
		),
	}, finalizerPlatformCases()...), finalizerTenantCases()...), finalizerNetworkingCases()...)
}

// finalizerEntries returns one table entry per case, followed by the extra parameters.
func finalizerEntries(extra ...any) []TableEntry {
	cases := finalizerCases()
	entries := make([]TableEntry, 0, len(cases))
	for _, c := range cases {
		entries = append(entries, Entry(c.service, append([]any{c}, extra...)...))
	}
	return entries
}

// finalizerMaskOf returns a field mask with the given paths.
func finalizerMaskOf(paths ...string) *fieldmaskpb.FieldMask {
	return &fieldmaskpb.FieldMask{Paths: paths}
}

// finalizerMetadataOf returns the metadata of an object, which is nil when the object has none.
func finalizerMetadataOf(object proto.Message) *privatev1.Metadata {
	return object.(interface{ GetMetadata() *privatev1.Metadata }).GetMetadata()
}

// finalizersOf returns the finalizers of an object.
func finalizersOf(object proto.Message) []string {
	return finalizerMetadataOf(object).GetFinalizers()
}

// withMetadata returns a copy of the object whose metadata has been changed by the given function. The original
// object isn't modified.
func withMetadata(object proto.Message, change func(*privatev1.Metadata)) proto.Message {
	result := proto.Clone(object)
	setter := result.(interface {
		GetMetadata() *privatev1.Metadata
		SetMetadata(*privatev1.Metadata)
	})
	if setter.GetMetadata() == nil {
		setter.SetMetadata(&privatev1.Metadata{})
	}
	change(setter.GetMetadata())
	return result
}

// withFinalizers returns a copy of the object that has exactly the given finalizers.
func withFinalizers(object proto.Message, finalizers ...string) proto.Message {
	return withMetadata(object, func(metadata *privatev1.Metadata) {
		metadata.SetFinalizers(finalizers)
	})
}

// withoutMetadata returns a copy of the object that has no metadata at all.
func withoutMetadata(object proto.Message) proto.Message {
	result := proto.Clone(object)
	result.(interface{ SetMetadata(*privatev1.Metadata) }).SetMetadata(nil)
	return result
}

// get returns the stored object, failing the spec if it can't be read.
func (t finalizerTarget) get(ctx context.Context) proto.Message {
	GinkgoHelper()
	object, err := t.ops.get(ctx, t.id)
	Expect(err).ToNot(HaveOccurred())
	return object
}

// finalizers returns the stored finalizers.
func (t finalizerTarget) finalizers(ctx context.Context) []string {
	GinkgoHelper()
	return finalizersOf(t.get(ctx))
}

// update runs an update and returns its error.
func (t finalizerTarget) update(ctx context.Context, object proto.Message, mask *fieldmaskpb.FieldMask,
	lock bool) error {
	return t.ops.update(ctx, object, mask, lock)
}

// release replaces the finalizers as a concurrent writer would: from a fresh read, with the lock, and so with the
// current version. It fails the spec if the update doesn't succeed.
func (t finalizerTarget) release(ctx context.Context, finalizers ...string) {
	GinkgoHelper()
	err := t.ops.update(ctx, withFinalizers(t.get(ctx), finalizers...), finalizerMaskOf("metadata.finalizers"), true)
	Expect(err).ToNot(HaveOccurred(), "the concurrent finalizer write must succeed")
}

// bump changes a label as a concurrent writer that doesn't touch finalizers would, which increases the version.
func (t finalizerTarget) bump(ctx context.Context) {
	GinkgoHelper()
	object := withMetadata(t.get(ctx), func(metadata *privatev1.Metadata) {
		metadata.SetLabels(map[string]string{"bump": uuid.NewString()[:8]})
	})
	err := t.ops.update(ctx, object, finalizerMaskOf("metadata.labels"), true)
	Expect(err).ToNot(HaveOccurred(), "the concurrent label write must succeed")
}

// expectFinalizerConflict checks that the update was rejected as a conflict and that the stored finalizers are exactly
// the expected ones, which means that the rejected write wasn't applied.
func (t finalizerTarget) expectFinalizerConflict(ctx context.Context, err error, want ...string) {
	GinkgoHelper()
	stored, getErr := t.ops.get(ctx, t.id)
	var got []string
	state := fmt.Sprintf("unreadable (%v)", getErr)
	if getErr == nil {
		got = finalizersOf(stored)
		sort.Strings(got)
		state = fmt.Sprintf("%v", got)
	}
	Expect(status.Code(err)).To(Equal(codes.Aborted),
		"a stale finalizer write must be rejected as a conflict, but the update returned %v and the stored "+
			"finalizers are now %v", err, state)
	Expect(getErr).ToNot(HaveOccurred(), "the object must still exist")
	Expect(got).To(ConsistOf(want))
	expectConflictMessage(err, t.id)
}

// expectConflictMessage checks that the message of a conflict names the object and doesn't reveal finalizer names,
// which belong to the controllers. It doesn't check the exact wording.
func expectConflictMessage(err error, id string) {
	GinkgoHelper()
	message := status.Convert(err).Message()
	Expect(message).To(ContainSubstring(id))
	Expect(message).ToNot(ContainSubstring(finalizerHold))
	Expect(message).ToNot(ContainSubstring(finalizerController))
}
