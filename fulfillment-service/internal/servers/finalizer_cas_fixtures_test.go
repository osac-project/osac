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
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"slices"
	"sort"
	"strings"
	"sync"

	"buf.build/go/protovalidate"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/types"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpcmetadata "google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	osactesting "github.com/osac-project/osac/fulfillment-service/internal/testing"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// Finalizer CAS fixtures: one case per private resource with finalizers, run against its bare GenericServer.

const (
	// Kept for the object's whole life.
	finalizerController = "fulfillment-controller"

	// Added and later released by another writer.
	finalizerHold = "test.osac.io/hold"

	// Owned by a writer the test doesn't control.
	finalizerForeign = "test.osac.io/foreign"
)

// finalizerBearingServices returns the private services whose Update object has metadata.finalizers.
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

type finalizerObject interface {
	dao.Object
	GetMetadata() *privatev1.Metadata
	SetMetadata(*privatev1.Metadata)
}

// finalizerOps is a non-generic view of a GenericServer; objects must be the resource's concrete type.
type finalizerOps interface {
	get(ctx context.Context, id string) (proto.Message, error)
	update(ctx context.Context, object proto.Message, mask *fieldmaskpb.FieldMask, lock bool) error
	del(ctx context.Context, id string) error
	setLogger(logger *slog.Logger)
}

type finalizerUpdateRequest[O any] interface {
	proto.Message
	SetObject(O)
	SetUpdateMask(*fieldmaskpb.FieldMask)
	SetLock(bool)
}

type finalizerIDRequest interface {
	proto.Message
	SetId(string)
}

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

func (o *genericFinalizerOps[O]) setLogger(logger *slog.Logger) {
	o.server.logger = logger
}

type finalizerTarget struct {
	ops finalizerOps
	id  string
}

// target seeds an object with the given finalizers at version zero. It reads the suite ctx when called, not when the
// table is built.
type finalizerCase struct {
	service string
	target  func(finalizers []string) finalizerTarget
}

// build saves any parents and returns the object with metadata set; options mirror the production server's.
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

			// The server, not the DAO, assigns the tenant.
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

// afterCreate runs hook with the stored object's id, for database state the specs need to remove.
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

func testHub(name string) *privatev1.Hub {
	return privatev1.Hub_builder{
		Metadata: privatev1.Metadata_builder{
			Name:   name,
			Tenant: testTenant,
		}.Build(),
		Spec: privatev1.HubSpec_builder{
			Kubeconfig: []byte("my_config"),
			Namespace:  "my_ns",
		}.Build(),
	}.Build()
}

func finalizerName(prefix string) string {
	return fmt.Sprintf("%s-%s", prefix, uuid.NewString()[:8])
}

// finalizerCases lists the leaf resources, which need no parents, here and the rest by group.
func finalizerCases() []finalizerCase {
	leaves := []finalizerCase{
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
				return testHub(finalizerName("hub"))
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
	}
	return slices.Concat(leaves, finalizerPlatformCases(), finalizerTenantCases(), finalizerNetworkingCases())
}

type finalizerSelectingMask struct {
	name string
	mask *fieldmaskpb.FieldMask
}

// finalizerSelectingMasks includes the indexed path, which replaces one stored entry and so also changes the set.
func finalizerSelectingMasks() []finalizerSelectingMask {
	return []finalizerSelectingMask{
		{"metadata mask", finalizerMaskOf("metadata")},
		{"nil mask", nil},
		{"metadata.finalizers mask", finalizersMask()},
		{"metadata.finalizers.0 mask", finalizerMaskOf("metadata.finalizers.0")},
		{"metadata and metadata.finalizers mask", finalizerMaskOf("metadata", "metadata.finalizers")},
		{"metadata.finalizers and metadata.version mask", finalizerMaskOf("metadata.finalizers", "metadata.version")},
	}
}

func finalizerMaskEntries() []TableEntry {
	var entries []TableEntry
	for _, m := range finalizerSelectingMasks() {
		entries = append(entries, Entry("with "+m.name, m.mask))
	}
	return entries
}

func publicMaskEntries() []TableEntry {
	var entries []TableEntry
	for _, m := range finalizerSelectingMasks() {
		entries = append(entries,
			Entry("live with "+m.name, m.mask, false),
			Entry("deleting with "+m.name, m.mask, true),
		)
	}
	return entries
}

func finalizerMaskOf(paths ...string) *fieldmaskpb.FieldMask {
	return &fieldmaskpb.FieldMask{Paths: paths}
}

func finalizersMask() *fieldmaskpb.FieldMask {
	return finalizerMaskOf("metadata.finalizers")
}

func finalizerMetadataOf(object proto.Message) *privatev1.Metadata {
	return object.(interface{ GetMetadata() *privatev1.Metadata }).GetMetadata()
}

func finalizersOf(object proto.Message) []string {
	return finalizerMetadataOf(object).GetFinalizers()
}

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

func withFinalizers(object proto.Message, finalizers ...string) proto.Message {
	return withMetadata(object, func(metadata *privatev1.Metadata) {
		metadata.SetFinalizers(finalizers)
	})
}

// withLabel replaces all labels with the given one.
func withLabel(object proto.Message, key, value string) proto.Message {
	return withMetadata(object, func(metadata *privatev1.Metadata) {
		metadata.SetLabels(map[string]string{key: value})
	})
}

func withoutMetadata(object proto.Message) proto.Message {
	result := proto.Clone(object)
	result.(interface{ SetMetadata(*privatev1.Metadata) }).SetMetadata(nil)
	return result
}

func (t finalizerTarget) get(ctx context.Context) proto.Message {
	GinkgoHelper()
	object, err := t.ops.get(ctx, t.id)
	Expect(err).ToNot(HaveOccurred())
	return object
}

// markDeleting deletes the object, which its finalizers turn into a deletion mark instead of an archive.
func (t finalizerTarget) markDeleting(ctx context.Context) {
	GinkgoHelper()
	Expect(t.ops.del(ctx, t.id)).To(Succeed())
	Expect(finalizerMetadataOf(t.get(ctx)).HasDeletionTimestamp()).To(BeTrue(),
		"the object must be marked as being deleted")
}

func (t finalizerTarget) finalizers(ctx context.Context) []string {
	GinkgoHelper()
	return finalizersOf(t.get(ctx))
}

func (t finalizerTarget) update(ctx context.Context, object proto.Message, mask *fieldmaskpb.FieldMask,
	lock bool) error {
	return t.ops.update(ctx, object, mask, lock)
}

// publicUpdateInSavepoint uses a savepoint so a database rejection doesn't abort the spec transaction.
func (t finalizerTarget) publicUpdateInSavepoint(ctx context.Context, object proto.Message,
	mask *fieldmaskpb.FieldMask) error {
	GinkgoHelper()
	tx, err := database.TxFromContext(ctx)
	Expect(err).ToNot(HaveOccurred())
	savepointErr := tx.Savepoint(ctx, func(ctx context.Context) error {
		err = t.update(withPublicAPIView(ctx), object, mask, false)
		return err
	})
	if err == nil {
		Expect(savepointErr).ToNot(HaveOccurred())
	}
	return err
}

// release replaces the finalizers as a concurrent writer would: fresh read, locked, current version.
func (t finalizerTarget) release(ctx context.Context, finalizers ...string) {
	GinkgoHelper()
	err := t.ops.update(ctx, withFinalizers(t.get(ctx), finalizers...), finalizersMask(), true)
	Expect(err).ToNot(HaveOccurred(), "the concurrent finalizer write must succeed")
}

// bump raises the version through a label change that leaves the finalizers alone.
func (t finalizerTarget) bump(ctx context.Context) {
	GinkgoHelper()
	object := withMetadata(t.get(ctx), func(metadata *privatev1.Metadata) {
		metadata.SetLabels(map[string]string{"bump": uuid.NewString()[:8]})
	})
	err := t.ops.update(ctx, object, finalizerMaskOf("metadata.labels"), true)
	Expect(err).ToNot(HaveOccurred(), "the concurrent label write must succeed")
}

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

// expectConflictMessage checks the message names the object but not the finalizers, which belong to controllers.
func expectConflictMessage(err error, id string) {
	GinkgoHelper()
	message := status.Convert(err).Message()
	Expect(message).To(ContainSubstring(id))
	Expect(message).ToNot(ContainSubstring(finalizerHold))
	Expect(message).ToNot(ContainSubstring(finalizerController))
}

const finalizerChangeMessage = "Updated finalizers"

// methodStream lets grpc.Method read the given method from the context.
type methodStream string

func (s methodStream) Method() string                 { return string(s) }
func (methodStream) SetHeader(grpcmetadata.MD) error  { return nil }
func (methodStream) SendHeader(grpcmetadata.MD) error { return nil }
func (methodStream) SetTrailer(grpcmetadata.MD) error { return nil }

// commitSuiteTx commits so the finalizer log is written, failing if the transaction rolled back so a spec expecting no
// log can't pass just because nothing committed.
func commitSuiteTx() {
	GinkgoHelper()
	endTx(ctx, suiteTx, true)
	tx, err := tm.Begin(context.Background())
	Expect(err).ToNot(HaveOccurred())
	suiteTx = tx
	ctx = database.TxIntoContext(ctx, suiteTx)
}

// endTx commits or rolls back tx and checks which one happened.
func endTx(ctx context.Context, tx database.Tx, commit bool) {
	GinkgoHelper()
	if !commit {
		laterErr := errors.New("a later step of the request failed")
		tx.ReportError(&laterErr)
	}
	committed := false
	tx.OnCommit(func() {
		committed = true
	})
	Expect(tx.End(ctx)).To(Succeed())
	Expect(committed).To(Equal(commit), "the transaction must have been committed: %t", commit)
}

// finalizerRun replaces the suite transaction for one concurrent case. commit and rollBack start a new transaction, so
// reread r.ctx after calling them.
type finalizerRun struct {
	ctx context.Context
	tx  database.Tx
}

func (r *finalizerRun) begin() {
	GinkgoHelper()
	tx, err := tm.Begin(context.Background())
	Expect(err).ToNot(HaveOccurred())
	r.tx = tx
	r.ctx = database.TxIntoContext(context.Background(), tx)
}

func (r *finalizerRun) commit() {
	GinkgoHelper()
	endTx(r.ctx, r.tx, true)
	r.begin()
}

// rollBack simulates a later step of the request failing, then starts a new transaction.
func (r *finalizerRun) rollBack() {
	GinkgoHelper()
	endTx(r.ctx, r.tx, false)
	r.begin()
}

type finalizerScenario func(r *finalizerRun, c finalizerCase, t finalizerTarget)

// finalizerFailure is panicked by a failed assertion to stop only its own case.
type finalizerFailure struct {
	message  string
	location types.CodeLocation
}

func runFinalizerCases(finalizers []string, scenario finalizerScenario) {
	GinkgoHelper()
	runFinalizerCasesFor(finalizerCases(), finalizers, scenario)
}

// runFinalizerCasesFor seeds and commits one object per case, then runs each scenario concurrently in its own
// transaction. A failed assertion stops only its case; the spec fails at the end with every case's failure.
func runFinalizerCasesFor(cases []finalizerCase, finalizers []string, scenario finalizerScenario) {
	GinkgoHelper()
	targets := make([]finalizerTarget, len(cases))
	for i, c := range cases {
		targets[i] = c.target(finalizers)
	}

	// Each case has its own transaction; ending the suite one frees its connection.
	endTx(ctx, suiteTx, true)
	suiteTx = nil

	// Ginkgo keeps only the first failure and not its case, so failures panic and are recorded per case.
	RegisterFailHandler(func(message string, callerSkip ...int) {
		skip := 0
		if len(callerSkip) > 0 {
			skip = callerSkip[0]
		}
		panic(finalizerFailure{
			message:  message,
			location: types.NewCodeLocation(skip + 1),
		})
	})
	defer RegisterFailHandler(Fail)

	var (
		lock     sync.Mutex
		failures []string
		wg       sync.WaitGroup
	)
	for i, c := range cases {
		wg.Go(func() {
			defer func() {
				p := recover()
				if p == nil {
					return
				}
				var failure string
				if f, ok := p.(finalizerFailure); ok {
					failure = fmt.Sprintf("%s: %s\n%s", c.service, f.location, f.message)
				} else {
					failure = fmt.Sprintf("%s: panic: %v\n%s", c.service, p, debug.Stack())
				}
				lock.Lock()
				failures = append(failures, failure)
				lock.Unlock()
			}()
			r := &finalizerRun{}
			r.begin()
			ended := false
			defer func() {
				if !ended {
					_ = r.tx.End(context.Background())
				}
			}()
			scenario(r, c, targets[i])
			ended = true
			Expect(r.tx.End(context.Background())).To(Succeed())
		})
	}
	wg.Wait()

	if len(failures) > 0 {
		sort.Strings(failures)
		Fail(fmt.Sprintf("%d of %d resources failed:\n\n%s", len(failures), len(cases),
			strings.Join(failures, "\n\n")))
	}
}

// recordLogs swaps the server logger for a recorder. Call it after seeding so the seed isn't recorded.
func (t finalizerTarget) recordLogs() *osactesting.LogRecorder {
	recorder := &osactesting.LogRecorder{}
	t.ops.setLogger(slog.New(recorder))
	return recorder
}
