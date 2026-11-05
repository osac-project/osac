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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/vault"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// These specs run the finalizer scenarios through the real typed servers, not only through the bare generic server:
// the private wrappers that rebuild the request before calling the generic update, and the public servers that map
// objects without finalizers into private ones.

// finalizerTierResolver resolves every storage tier to a block-protocol test provider.
func finalizerTierResolver(_ context.Context, _ string) (*TierResolution, error) {
	return &TierResolution{
		Provider: "test-provider",
		Protocol: privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
	}, nil
}

// newFinalizerPrivateVolumesServer returns the private Volumes server with the test tier resolver.
func newFinalizerPrivateVolumesServer() *PrivateVolumesServer {
	GinkgoHelper()
	server, err := NewPrivateVolumesServer().
		SetLogger(logger).
		SetAttributionLogic(attribution).
		SetTenancyLogic(tenancy).
		SetTierResolver(finalizerTierResolver).
		Build()
	Expect(err).ToNot(HaveOccurred())
	return server
}

// finalizerCaseFor returns the case of a resource by its fully qualified service name.
func finalizerCaseFor(service string) finalizerCase {
	GinkgoHelper()
	for _, c := range finalizerCases() {
		if c.service == service {
			return c
		}
	}
	Fail("there is no finalizer case for " + service)
	return finalizerCase{}
}

// publicFinalizerCaseFor returns the public server case of the given private service, failing the spec if there is
// none.
func publicFinalizerCaseFor(service string) publicFinalizerCase {
	GinkgoHelper()
	for _, c := range publicFinalizerCases() {
		if c.service == service {
			return c
		}
	}
	Fail("there is no public finalizer case for " + service)
	return publicFinalizerCase{}
}

// typedUpdater sends an update through a typed server. The object must be of the private type of the resource.
type typedUpdater func(ctx context.Context, object proto.Message, mask *fieldmaskpb.FieldMask) error

// publicFinalizerOps are the operations of a public server, on objects of the public type of the resource.
type publicFinalizerOps struct {
	get    func(ctx context.Context, id string) (proto.Message, error)
	update func(ctx context.Context, object proto.Message, mask *fieldmaskpb.FieldMask) error
}

// publicIDRequest is implemented by the generated public get request of every resource.
type publicIDRequest interface {
	proto.Message
	SetId(string)
}

// publicUpdateRequest is implemented by the generated public update request of a resource whose public type is P.
type publicUpdateRequest[P any] interface {
	proto.Message
	SetObject(P)
	SetUpdateMask(*fieldmaskpb.FieldMask)
}

// newRequestOf returns an empty request of the given generated type.
func newRequestOf[T proto.Message]() T {
	var zero T
	return zero.ProtoReflect().New().Interface().(T)
}

// newPublicFinalizerOps adapts the Get and Update methods of a public server. The object function extracts the public
// object from the response of Get, which is the GetObject method of the response type.
func newPublicFinalizerOps[P proto.Message, G publicIDRequest, R any, U publicUpdateRequest[P], V any](
	get func(ctx context.Context, request G) (R, error),
	update func(ctx context.Context, request U) (V, error),
	object func(R) P,
) publicFinalizerOps {
	return publicFinalizerOps{
		get: func(ctx context.Context, id string) (proto.Message, error) {
			request := newRequestOf[G]()
			request.SetId(id)
			response, err := get(ctx, request)
			return object(response), err
		},
		update: func(ctx context.Context, value proto.Message, mask *fieldmaskpb.FieldMask) error {
			request := newRequestOf[U]()
			request.SetObject(value.(P))
			request.SetUpdateMask(mask)
			_, err := update(ctx, request)
			return err
		},
	}
}

// publicFinalizerCase is a public server and the private resource that it serves.
type publicFinalizerCase struct {
	service string
	ops     func() publicFinalizerOps
}

// withPublicLabel returns a copy of a public object with an extra label, which is a change that every public update
// has to carry through to the stored object.
func withPublicLabel(object proto.Message) proto.Message {
	result := proto.Clone(object)
	accessor := result.(interface {
		GetMetadata() *publicv1.Metadata
		SetMetadata(*publicv1.Metadata)
	})
	if accessor.GetMetadata() == nil {
		accessor.SetMetadata(&publicv1.Metadata{})
	}
	labels := map[string]string{"x": "y"}
	for key, value := range accessor.GetMetadata().GetLabels() {
		labels[key] = value
	}
	accessor.GetMetadata().SetLabels(labels)
	return result
}

// withPublicDescriptionAndNoMetadata returns a copy of a public object with another description and without metadata.
func withPublicDescriptionAndNoMetadata(object proto.Message) proto.Message {
	result := proto.Clone(object)
	result.(interface{ SetDescription(string) }).SetDescription("changed by the specs")
	result.(interface{ SetMetadata(*publicv1.Metadata) }).SetMetadata(nil)
	return result
}

// withOnlyIDAndMetadata returns a copy of the object that keeps its identifier and its metadata and nothing else. The
// private servers that merge a request into the stored object append the repeated fields of the request, so a copy of
// the whole stored object would repeat its spec.
func withOnlyIDAndMetadata(object proto.Message) proto.Message {
	result := proto.Clone(object)
	message := result.ProtoReflect()
	message.Range(func(field protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		if name := field.Name(); name != "id" && name != "metadata" {
			message.Clear(field)
		}
		return true
	})
	return result
}

// newBareMetalInstanceTypesUpdater returns an updater that sends requests through the private BareMetalInstanceTypes
// server.
func newBareMetalInstanceTypesUpdater() typedUpdater {
	GinkgoHelper()
	server, err := NewPrivateBareMetalInstanceTypesServer().
		SetLogger(logger).
		SetAttributionLogic(attribution).
		SetTenancyLogic(tenancy).
		Build()
	Expect(err).ToNot(HaveOccurred())
	return func(ctx context.Context, object proto.Message, mask *fieldmaskpb.FieldMask) error {
		_, err := server.Update(ctx, privatev1.BareMetalInstanceTypesUpdateRequest_builder{
			Object:     object.(*privatev1.BareMetalInstanceType),
			UpdateMask: mask,
		}.Build())
		return err
	}
}

// newInstanceTypesUpdater returns an updater that sends requests through the private InstanceTypes server.
func newInstanceTypesUpdater() typedUpdater {
	GinkgoHelper()
	server, err := NewPrivateInstanceTypesServer().
		SetLogger(logger).
		SetAttributionLogic(attribution).
		SetTenancyLogic(tenancy).
		Build()
	Expect(err).ToNot(HaveOccurred())
	return func(ctx context.Context, object proto.Message, mask *fieldmaskpb.FieldMask) error {
		_, err := server.Update(ctx, privatev1.InstanceTypesUpdateRequest_builder{
			Object:     object.(*privatev1.InstanceType),
			UpdateMask: mask,
		}.Build())
		return err
	}
}

// newDiskImagesUpdater returns an updater that sends requests through the private DiskImages server.
func newDiskImagesUpdater() typedUpdater {
	GinkgoHelper()
	server, err := NewPrivateDiskImagesServer().
		SetLogger(logger).
		SetAttributionLogic(attribution).
		SetTenancyLogic(tenancy).
		Build()
	Expect(err).ToNot(HaveOccurred())
	return func(ctx context.Context, object proto.Message, mask *fieldmaskpb.FieldMask) error {
		_, err := server.Update(ctx, privatev1.DiskImagesUpdateRequest_builder{
			Object:     object.(*privatev1.DiskImage),
			UpdateMask: mask,
		}.Build())
		return err
	}
}

// finalizerRaceResult is the outcome of a write that raced against the release of a finalizer.
type finalizerRaceResult struct {
	writerErr       error
	releasedVersion int32
}

// raceAgainstRelease commits the fixture and then plays a release of a finalizer against a second writer with two
// real transactions: one writer releases the hold with the lock and doesn't commit, a second writer starts and blocks
// on the row lock, and then the first one commits. The second writer has read the object before the commit, so what
// it writes is based on a stale view. The table is the one of the resource, used to see when the second writer is
// blocked.
func (t finalizerTarget) raceAgainstRelease(table string, writer func(ctx context.Context) error) finalizerRaceResult {
	GinkgoHelper()
	var result finalizerRaceResult

	// Commit the fixture so that two independent database transactions can write the object:
	Expect(suiteTx.End(ctx)).To(Succeed())
	suiteTx = nil

	releaseTx, err := tm.Begin(context.Background())
	Expect(err).ToNot(HaveOccurred())
	releaseCtx := database.TxIntoContext(context.Background(), releaseTx)
	var writerTx database.Tx
	var writerDone chan error
	DeferCleanup(func() {
		if releaseTx != nil {
			_ = releaseTx.End(context.Background())
			releaseTx = nil
		}
		if writerDone != nil {
			select {
			case <-writerDone:
			case <-time.After(10 * time.Second):
			}
		}
		if writerTx != nil {
			_ = writerTx.End(context.Background())
			writerTx = nil
		}
	})

	// The first writer releases the hold, but the transaction stays open:
	t.release(releaseCtx, finalizerController)
	result.releasedVersion = finalizerMetadataOf(t.get(releaseCtx)).GetVersion()

	// The second writer starts, and has to wait for the row lock of the first one:
	writerTx, err = tm.Begin(context.Background())
	Expect(err).ToNot(HaveOccurred())
	writerCtx, cancel := context.WithTimeout(database.TxIntoContext(context.Background(), writerTx), 10*time.Second)
	defer cancel()
	writerDone = make(chan error, 1)
	go func() {
		defer GinkgoRecover()
		writerDone <- writer(writerCtx)
	}()

	// Wait until PostgreSQL reports that the second writer is blocked by the first one:
	Eventually(func() (bool, error) {
		var waiting bool
		err := testPool.QueryRow(context.Background(), `
			select exists (
				select 1
				from pg_stat_activity
				where datname = current_database()
				  and pid <> pg_backend_pid()
				  and state = 'active'
				  and wait_event_type = 'Lock'
				  and cardinality(pg_blocking_pids(pid)) > 0
				  and query ilike '%' || $1 || '%'
			)
		`, table).Scan(&waiting)
		return waiting, err
	}, 5*time.Second, 10*time.Millisecond).Should(BeTrue())

	// Commit the first writer, which lets the second one go on:
	Expect(releaseTx.End(context.Background())).To(Succeed())
	releaseTx = nil
	result.writerErr = <-writerDone
	writerDone = nil
	Expect(writerTx.End(context.Background())).To(Succeed())
	writerTx = nil
	return result
}

// committedState reads, from a new transaction, the finalizers and the version that are stored, and the number of
// update records that the database kept for the object.
func (t finalizerTarget) committedState(table string) (finalizers []string, version int32, updates int) {
	GinkgoHelper()
	tx, err := tm.Begin(context.Background())
	Expect(err).ToNot(HaveOccurred())
	defer func() {
		GinkgoHelper()
		Expect(tx.End(context.Background())).To(Succeed())
	}()
	freshCtx := database.TxIntoContext(context.Background(), tx)
	stored := t.get(freshCtx)
	err = tx.QueryRow(freshCtx, `
		select count(*)
		from changes
		where "table" = $1 and op = 'UPDATE' and data->>'id' = $2
	`, table, t.id).Scan(&updates)
	Expect(err).ToNot(HaveOccurred())
	return finalizersOf(stored), finalizerMetadataOf(stored).GetVersion(), updates
}

// expectStaleTypedWriteRejected runs a stale finalizer write through a typed server: the writer reads the
// object while the hold is present, another writer releases it, and then the first one writes back its stale view.
func expectStaleTypedWriteRejected(t finalizerTarget, update typedUpdater) {
	GinkgoHelper()
	stale := t.get(ctx)
	t.release(ctx, finalizerController)

	err := update(ctx, withFinalizers(stale, finalizerHold), finalizerMaskOf("metadata.finalizers"))

	t.expectFinalizerConflict(ctx, err, finalizerController)
}

var _ = Describe("typed finalizer updates", func() {
	Describe("private wrappers with their own update path", func() {
		// Volume reads the stored object, merges the request and checks immutable fields before calling the generic
		// update. It ignores the metadata paths when merging, so a metadata.finalizers mask is safe.
		It("rejects a stale finalizer write through the Volumes server", func() {
			server := newFinalizerPrivateVolumesServer()
			t := finalizerCaseFor(privatev1.Volumes_ServiceDesc.ServiceName).
				target([]string{finalizerController, finalizerHold})

			expectStaleTypedWriteRejected(t, func(ctx context.Context, object proto.Message,
				mask *fieldmaskpb.FieldMask) error {
				_, err := server.Update(ctx, privatev1.VolumesUpdateRequest_builder{
					Object:     object.(*privatev1.Volume),
					UpdateMask: mask,
				}.Build())
				return err
			})
		})

		// ServiceAccount skips its preparation callback when the mask is exactly metadata.finalizers.
		It("rejects a stale finalizer write through the ServiceAccounts server", func() {
			server, err := NewPrivateServiceAccountsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			t := finalizerCaseFor(privatev1.ServiceAccounts_ServiceDesc.ServiceName).
				target([]string{finalizerController, finalizerHold})

			expectStaleTypedWriteRejected(t, func(ctx context.Context, object proto.Message,
				mask *fieldmaskpb.FieldMask) error {
				_, err := server.Update(ctx, privatev1.ServiceAccountsUpdateRequest_builder{
					Object:     object.(*privatev1.ServiceAccount),
					UpdateMask: mask,
				}.Build())
				return err
			})
		})

		// Cluster validates and prepares the candidate on every update, so its fixture has to be a valid cluster.
		It("rejects a stale finalizer write through the Clusters server", func() {
			server, err := NewPrivateClustersServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			t := finalizerCaseFor(privatev1.Clusters_ServiceDesc.ServiceName).
				target([]string{finalizerController, finalizerHold})

			expectStaleTypedWriteRejected(t, func(ctx context.Context, object proto.Message,
				mask *fieldmaskpb.FieldMask) error {
				_, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object:     object.(*privatev1.Cluster),
					UpdateMask: mask,
				}.Build())
				return err
			})
		})
	})

	Describe("public servers that send a nil update mask", func() {
		// The public API has no finalizers. Most of these servers map the public object into an empty private one,
		// so what they forward has none. DiskImages and InstanceTypes map it over the stored object that they read
		// from the private server: the forwarded object already carries the stored finalizers, the masked paths of
		// their private servers ignore metadata.finalizers, and the nil-mask path merges the request over the stored
		// object, which unions the request finalizers with the stored ones.
		DescribeTable("keeps the finalizers of the object",
			func(c publicFinalizerCase) {
				t := finalizerCaseFor(c.service).target([]string{finalizerController, finalizerHold})
				public := c.ops()

				object, err := public.get(ctx, t.id)
				Expect(err).ToNot(HaveOccurred())
				Expect(public.update(ctx, withPublicLabel(object), nil)).To(Succeed())

				Expect(t.finalizers(ctx)).To(ConsistOf(finalizerController, finalizerHold),
					"a public update must not clear the finalizers")
			},
			publicFinalizerEntries(),
		)

		DescribeTable("keeps a deleting object",
			func(c publicFinalizerCase) {
				t := finalizerCaseFor(c.service).target([]string{finalizerController, finalizerHold})
				public := c.ops()
				Expect(t.ops.del(ctx, t.id)).To(Succeed())

				object, err := public.get(ctx, t.id)
				Expect(err).ToNot(HaveOccurred())
				Expect(public.update(ctx, withPublicLabel(object), nil)).To(Succeed())

				stored, err := t.ops.get(ctx, t.id)
				Expect(err).ToNot(HaveOccurred(), "a public update must not archive an object with finalizers")
				Expect(finalizersOf(stored)).To(ConsistOf(finalizerController, finalizerHold))
			},
			publicFinalizerEntries(),
		)
	})

	Describe("BareMetalInstanceType", func() {
		// This server applies the masks itself, with compiled paths, before calling the generic update.
		var server *PrivateBareMetalInstanceTypesServer
		var update typedUpdater

		BeforeEach(func() {
			var err error
			server, err = NewPrivateBareMetalInstanceTypesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			update = func(ctx context.Context, object proto.Message, mask *fieldmaskpb.FieldMask) error {
				_, err := server.Update(ctx, privatev1.BareMetalInstanceTypesUpdateRequest_builder{
					Object:     object.(*privatev1.BareMetalInstanceType),
					UpdateMask: mask,
				}.Build())
				return err
			}
		})

		It("persists a finalizer added from the current version", func() {
			t := finalizerCaseFor(privatev1.BareMetalInstanceTypes_ServiceDesc.ServiceName).
				target([]string{finalizerController})

			object := withFinalizers(t.get(ctx), finalizerController, finalizerHold)
			Expect(update(ctx, object, finalizerMaskOf("metadata.finalizers"))).To(Succeed())

			Expect(t.finalizers(ctx)).To(ConsistOf(finalizerController, finalizerHold))
		})

		It("rejects a stale finalizer write", func() {
			t := finalizerCaseFor(privatev1.BareMetalInstanceTypes_ServiceDesc.ServiceName).
				target([]string{finalizerController, finalizerHold})

			expectStaleTypedWriteRejected(t, update)
		})

		// With a nil mask this server merges the whole request over the stored object and keeps the version of the
		// request, so a stale write would bring back a finalizer that another writer released after the stale read. The
		// merge appends repeated fields, so the request carries only the identifier and the metadata: a copy of the
		// stored spec would double the hardware ports and fail the immutability check before the finalizer check.
		It("rejects a stale nil-mask write that would restore a released finalizer", func() {
			t := finalizerCaseFor(privatev1.BareMetalInstanceTypes_ServiceDesc.ServiceName).
				target([]string{finalizerController, finalizerHold})
			current := t.get(ctx).(*privatev1.BareMetalInstanceType)
			stale := privatev1.BareMetalInstanceType_builder{
				Id:       current.GetId(),
				Metadata: current.GetMetadata(),
			}.Build()
			t.release(ctx, finalizerController)

			err := update(ctx, stale, nil)

			t.expectFinalizerConflict(ctx, err, finalizerController)
		})

		// This server takes the version of the request from its metadata and puts it in the merged object that it
		// forwards. Without metadata the version is zero, and the mask application clears the finalizers because the
		// request has none.
		DescribeTable("rejects a finalizer change that has no metadata",
			func(bumpFirst bool) {
				t := finalizerCaseFor(privatev1.BareMetalInstanceTypes_ServiceDesc.ServiceName).
					target([]string{finalizerController, finalizerHold})
				if bumpFirst {
					t.bump(ctx)
				}

				err := update(ctx, withoutMetadata(t.get(ctx)), finalizerMaskOf("metadata.finalizers"))

				t.expectFinalizerConflict(ctx, err, finalizerController, finalizerHold)
			},
			Entry("at version 0", false),
			Entry("at a later version", true),
		)
	})

	Describe("private wrappers that merge the request into the stored object", func() {
		// InstanceType and DiskImage ignore metadata.finalizers when they merge the request, so the object that they
		// forward to the generic update carries the stored finalizers whatever the request holds. A request without
		// metadata therefore doesn't change them, and is accepted at any stored version.
		DescribeTable("keeps the finalizers when the request has no metadata",
			func(service string, newUpdate func() typedUpdater, bumpFirst bool) {
				update := newUpdate()
				t := finalizerCaseFor(service).target([]string{finalizerController, finalizerHold})
				if bumpFirst {
					t.bump(ctx)
				}

				err := update(ctx, withoutMetadata(t.get(ctx)), finalizerMaskOf("metadata.finalizers"))

				Expect(err).ToNot(HaveOccurred())
				Expect(t.finalizers(ctx)).To(ConsistOf(finalizerController, finalizerHold))
			},
			Entry("InstanceTypes at version 0", privatev1.InstanceTypes_ServiceDesc.ServiceName,
				newInstanceTypesUpdater, false),
			Entry("InstanceTypes at a later version", privatev1.InstanceTypes_ServiceDesc.ServiceName,
				newInstanceTypesUpdater, true),
			Entry("DiskImages at version 0", privatev1.DiskImages_ServiceDesc.ServiceName,
				newDiskImagesUpdater, false),
			Entry("DiskImages at a later version", privatev1.DiskImages_ServiceDesc.ServiceName,
				newDiskImagesUpdater, true),
		)

		// With a nil mask the merge forwards the finalizers of the request, so a stale whole-object write would bring
		// back a finalizer that another writer released after the stale read. It carries an old version, so it is
		// rejected as a conflict instead.
		DescribeTable("rejects a stale nil-mask write that would restore a released finalizer",
			func(service string, newUpdate func() typedUpdater) {
				update := newUpdate()
				t := finalizerCaseFor(service).target([]string{finalizerController, finalizerHold})
				stale := t.get(ctx)
				t.release(ctx, finalizerController)

				err := update(ctx, stale, nil)

				t.expectFinalizerConflict(ctx, err, finalizerController)
			},
			Entry("InstanceTypes", privatev1.InstanceTypes_ServiceDesc.ServiceName, newInstanceTypesUpdater),
			Entry("DiskImages", privatev1.DiskImages_ServiceDesc.ServiceName, newDiskImagesUpdater),
		)

		// From the current version the merge only adds: the finalizers of the request join the stored ones, and a
		// request with fewer finalizers leaves the stored ones in place.
		DescribeTable("adds the finalizers of the request to the stored ones and never removes one",
			func(service string, newUpdate func() typedUpdater) {
				update := newUpdate()
				t := finalizerCaseFor(service).target([]string{finalizerController})

				added := withFinalizers(withOnlyIDAndMetadata(t.get(ctx)), finalizerController, finalizerHold)
				Expect(update(ctx, added, nil)).To(Succeed())
				Expect(t.finalizers(ctx)).To(ConsistOf(finalizerController, finalizerHold))

				fewer := withFinalizers(withOnlyIDAndMetadata(t.get(ctx)), finalizerController)
				Expect(update(ctx, fewer, nil)).To(Succeed())
				Expect(t.finalizers(ctx)).To(ConsistOf(finalizerController, finalizerHold))
			},
			Entry("InstanceTypes", privatev1.InstanceTypes_ServiceDesc.ServiceName, newInstanceTypesUpdater),
			Entry("DiskImages", privatev1.DiskImages_ServiceDesc.ServiceName, newDiskImagesUpdater),
			Entry("BareMetalInstanceTypes", privatev1.BareMetalInstanceTypes_ServiceDesc.ServiceName,
				newBareMetalInstanceTypesUpdater),
		)

		// These two servers apply the paths of the mask one by one, and none of them is a finalizer path, so a mask
		// can neither add nor remove a finalizer. The request is accepted and the stored finalizers stay as they are.
		DescribeTable("ignores a finalizer path in the mask",
			func(service string, newUpdate func() typedUpdater) {
				update := newUpdate()
				t := finalizerCaseFor(service).target([]string{finalizerController, finalizerHold})

				removed := withFinalizers(t.get(ctx), finalizerController)
				Expect(update(ctx, removed, finalizerMaskOf("metadata.finalizers"))).To(Succeed())
				Expect(t.finalizers(ctx)).To(ConsistOf(finalizerController, finalizerHold))

				added := withFinalizers(t.get(ctx), finalizerController, finalizerHold, finalizerForeign)
				Expect(update(ctx, added, finalizerMaskOf("metadata.finalizers"))).To(Succeed())
				Expect(t.finalizers(ctx)).To(ConsistOf(finalizerController, finalizerHold))
			},
			Entry("InstanceTypes", privatev1.InstanceTypes_ServiceDesc.ServiceName, newInstanceTypesUpdater),
			Entry("DiskImages", privatev1.DiskImages_ServiceDesc.ServiceName, newDiskImagesUpdater),
		)
	})

	Describe("ServiceAccounts server", func() {
		// A missing mask is rewritten into an update of the labels, so the finalizers of the request are not looked
		// at, even when the request is stale and carries a finalizer that another writer has released.
		It("leaves the finalizers alone when the mask is missing", func() {
			server, err := NewPrivateServiceAccountsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			t := finalizerCaseFor(privatev1.ServiceAccounts_ServiceDesc.ServiceName).
				target([]string{finalizerController, finalizerHold})
			stale := t.get(ctx)
			t.release(ctx, finalizerController)

			changed := withMetadata(stale, func(metadata *privatev1.Metadata) {
				metadata.SetLabels(map[string]string{"x": "y"})
			})
			_, err = server.Update(ctx, privatev1.ServiceAccountsUpdateRequest_builder{
				Object: changed.(*privatev1.ServiceAccount),
			}.Build())

			Expect(err).ToNot(HaveOccurred())
			stored := t.get(ctx)
			Expect(finalizersOf(stored)).To(ConsistOf(finalizerController))
			Expect(finalizerMetadataOf(stored).GetLabels()).To(HaveKeyWithValue("x", "y"))
		})
	})

	Describe("public servers that read the object before the update", func() {
		// On a nil mask these servers do their own unlocked read of the private object and forward its finalizers.
		// The window is between that read and the locked read of the generic server, so a sequential spec can't
		// cover it: the other writer has to commit while the public update waits for the row lock.
		DescribeTable("does not restore a finalizer that was released while waiting for the row lock",
			func(service, table string, newOps func() publicFinalizerOps) {
				t := finalizerCaseFor(service).target([]string{finalizerController, finalizerHold})
				public := newOps()
				object, err := public.get(ctx, t.id)
				Expect(err).ToNot(HaveOccurred())
				changed := withPublicLabel(object)

				result := t.raceAgainstRelease(table, func(ctx context.Context) error {
					return public.update(ctx, changed, nil)
				})

				Expect(result.writerErr).ToNot(HaveOccurred())
				finalizers, _, _ := t.committedState(table)
				Expect(finalizers).To(ConsistOf(finalizerController), "the public update restored the released hold")
			},
			Entry("Volumes", privatev1.Volumes_ServiceDesc.ServiceName, "volumes", func() publicFinalizerOps {
				return newPublicVolumesOps()
			}),
			Entry("Clusters", privatev1.Clusters_ServiceDesc.ServiceName, "clusters", func() publicFinalizerOps {
				return newPublicClustersOps()
			}),
			Entry("DiskImages", privatev1.DiskImages_ServiceDesc.ServiceName, "disk_images", func() publicFinalizerOps {
				return publicFinalizerCaseFor(privatev1.DiskImages_ServiceDesc.ServiceName).ops()
			}),
			Entry("InstanceTypes", privatev1.InstanceTypes_ServiceDesc.ServiceName, "instance_types",
				func() publicFinalizerOps {
					return publicFinalizerCaseFor(privatev1.InstanceTypes_ServiceDesc.ServiceName).ops()
				}),
		)
	})

	Describe("two transactions", func() {
		It("rejects a stale finalizer write that waited for the row lock and leaves no trace of it", func() {
			server := newFinalizerPrivateVolumesServer()
			t := finalizerCaseFor(privatev1.Volumes_ServiceDesc.ServiceName).
				target([]string{finalizerController, finalizerHold})
			stale := withFinalizers(t.get(ctx), finalizerHold)

			result := t.raceAgainstRelease("volumes", func(ctx context.Context) error {
				_, err := server.Update(ctx, privatev1.VolumesUpdateRequest_builder{
					Object:     stale.(*privatev1.Volume),
					UpdateMask: finalizerMaskOf("metadata.finalizers"),
				}.Build())
				return err
			})

			finalizers, version, updates := t.committedState("volumes")
			Expect(status.Code(result.writerErr)).To(Equal(codes.Aborted),
				"the stale write returned %v and the stored finalizers are %v", result.writerErr, finalizers)
			expectConflictMessage(result.writerErr, t.id)
			Expect(finalizers).To(ConsistOf(finalizerController))
			Expect(version).To(Equal(result.releasedVersion), "the rejected write must not bump the version")
			Expect(updates).To(Equal(1), "only the release may have written a change record")
		})
	})

	Describe("catalog items, whose public servers copy the stored metadata when a nil-mask request has none", func() {
		// The three public catalog-items servers copy the stored metadata into the private object when they get a nil
		// mask and the request has no metadata. Otherwise GenericServer restores the stored finalizers, like it does
		// for every public update. These controls pin that the finalizers and the metadata are kept in all cases.
		DescribeTable("keeps the finalizers and the metadata",
			func(c publicFinalizerCase, variant string) {
				t := finalizerCaseFor(c.service).target([]string{finalizerController, finalizerHold})
				public := c.ops()
				before := t.get(ctx)
				if variant == "deleting" {
					Expect(t.ops.del(ctx, t.id)).To(Succeed())
				}

				object, err := public.get(ctx, t.id)
				Expect(err).ToNot(HaveOccurred())
				changed := withPublicLabel(object)
				if variant == "without metadata" {
					changed = withPublicDescriptionAndNoMetadata(object)
				}
				Expect(public.update(ctx, changed, nil)).To(Succeed())

				stored, err := t.ops.get(ctx, t.id)
				Expect(err).ToNot(HaveOccurred(), "a public update must not archive an object with finalizers")
				Expect(finalizersOf(stored)).To(ConsistOf(finalizerController, finalizerHold))
				Expect(finalizerMetadataOf(stored).GetName()).To(Equal(finalizerMetadataOf(before).GetName()))
				if variant == "without metadata" {
					Expect(finalizerMetadataOf(before).GetLabels()).ToNot(BeEmpty(), "the fixture must have labels")
					Expect(finalizerMetadataOf(stored).GetLabels()).To(Equal(finalizerMetadataOf(before).GetLabels()))
				}
			},
			catalogItemEntries(),
		)
	})
})

// publicFinalizerEntries returns an entry for each public server that sends a nil update mask.
func publicFinalizerEntries() []TableEntry {
	var entries []TableEntry
	for _, c := range publicFinalizerCases() {
		entries = append(entries, Entry(c.service, c))
	}
	return entries
}

// catalogItemEntries returns an entry for each public catalog-items server and each kind of nil-mask update.
func catalogItemEntries() []TableEntry {
	var entries []TableEntry
	for _, c := range catalogItemFinalizerCases() {
		for _, variant := range []string{"with metadata", "without metadata", "deleting"} {
			entries = append(entries, Entry(c.service+" "+variant, c, variant))
		}
	}
	return entries
}

// publicFinalizerCases returns the public servers that send a nil update mask with an object that has no finalizers
// of its own.
func publicFinalizerCases() []publicFinalizerCase {
	return []publicFinalizerCase{
		{privatev1.Projects_ServiceDesc.ServiceName, func() publicFinalizerOps {
			GinkgoHelper()
			server, err := NewProjectsServer().
				SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			return newPublicFinalizerOps(
				server.Get, server.Update, (*publicv1.ProjectsGetResponse).GetObject,
			)
		}},
		{privatev1.Roles_ServiceDesc.ServiceName, func() publicFinalizerOps {
			GinkgoHelper()
			server, err := NewRolesServer().
				SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			return newPublicFinalizerOps(
				server.Get, server.Update, (*publicv1.RolesGetResponse).GetObject,
			)
		}},
		{privatev1.RoleBindings_ServiceDesc.ServiceName, func() publicFinalizerOps {
			GinkgoHelper()
			server, err := NewRoleBindingsServer().
				SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			return newPublicFinalizerOps(
				server.Get, server.Update, (*publicv1.RoleBindingsGetResponse).GetObject,
			)
		}},
		{privatev1.IdentityProviders_ServiceDesc.ServiceName, func() publicFinalizerOps {
			GinkgoHelper()
			server, err := NewIdentityProvidersServer().
				SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			return newPublicFinalizerOps(
				server.Get, server.Update, (*publicv1.IdentityProvidersGetResponse).GetObject,
			)
		}},
		{privatev1.Users_ServiceDesc.ServiceName, func() publicFinalizerOps {
			GinkgoHelper()
			server, err := NewUsersServer().
				SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			return newPublicFinalizerOps(
				server.Get, server.Update, (*publicv1.UsersGetResponse).GetObject,
			)
		}},
		{privatev1.Secrets_ServiceDesc.ServiceName, func() publicFinalizerOps {
			GinkgoHelper()
			server, err := NewSecretsServer().
				SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).
				SetSecretStore(vault.NewMockSecretStore(ctrl)).Build()
			Expect(err).ToNot(HaveOccurred())
			return newPublicFinalizerOps(
				server.Get, server.Update, (*publicv1.SecretsGetResponse).GetObject,
			)
		}},
		{privatev1.DiskImages_ServiceDesc.ServiceName, func() publicFinalizerOps {
			GinkgoHelper()
			server, err := NewDiskImagesServer().
				SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			return newPublicFinalizerOps(
				server.Get, server.Update, (*publicv1.DiskImagesGetResponse).GetObject,
			)
		}},
		{privatev1.InstanceTypes_ServiceDesc.ServiceName, func() publicFinalizerOps {
			GinkgoHelper()
			server, err := NewInstanceTypesServer().
				SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			return newPublicFinalizerOps(
				server.Get, server.Update, (*publicv1.InstanceTypesGetResponse).GetObject,
			)
		}},
	}
}

// catalogItemFinalizerCases returns the three public catalog-items servers.
func catalogItemFinalizerCases() []publicFinalizerCase {
	return []publicFinalizerCase{
		{privatev1.ComputeInstanceCatalogItems_ServiceDesc.ServiceName, func() publicFinalizerOps {
			GinkgoHelper()
			server, err := NewComputeInstanceCatalogItemsServer().
				SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			return newPublicFinalizerOps(
				server.Get, server.Update, (*publicv1.ComputeInstanceCatalogItemsGetResponse).GetObject,
			)
		}},
		{privatev1.ClusterCatalogItems_ServiceDesc.ServiceName, func() publicFinalizerOps {
			GinkgoHelper()
			server, err := NewClusterCatalogItemsServer().
				SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			return newPublicFinalizerOps(
				server.Get, server.Update, (*publicv1.ClusterCatalogItemsGetResponse).GetObject,
			)
		}},
		{privatev1.BareMetalInstanceCatalogItems_ServiceDesc.ServiceName, func() publicFinalizerOps {
			GinkgoHelper()
			server, err := NewBareMetalInstanceCatalogItemsServer().
				SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			return newPublicFinalizerOps(
				server.Get, server.Update, (*publicv1.BareMetalInstanceCatalogItemsGetResponse).GetObject,
			)
		}},
	}
}

// newPublicVolumesOps returns the operations of the public Volumes server.
func newPublicVolumesOps() publicFinalizerOps {
	GinkgoHelper()
	server, err := NewVolumesServer().
		SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).
		SetTierResolver(finalizerTierResolver).Build()
	Expect(err).ToNot(HaveOccurred())
	return newPublicFinalizerOps(
		server.Get, server.Update, (*publicv1.VolumesGetResponse).GetObject,
	)
}

// newPublicClustersOps returns the operations of the public Clusters server.
func newPublicClustersOps() publicFinalizerOps {
	GinkgoHelper()
	server, err := NewClustersServer().
		SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
	Expect(err).ToNot(HaveOccurred())
	return newPublicFinalizerOps(
		server.Get, server.Update, (*publicv1.ClustersGetResponse).GetObject,
	)
}
