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
	"log/slog"
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/authz/catalog"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	osactesting "github.com/osac-project/osac/fulfillment-service/internal/testing"
	"github.com/osac-project/osac/fulfillment-service/internal/vault"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// Finalizer scenarios run through the typed servers, which rebuild or map the request before the generic update.

func finalizerTierResolver(_ context.Context, _ string) (*TierResolution, error) {
	return &TierResolution{
		Provider: "test-provider",
		Protocol: privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
	}, nil
}

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

// finalizerCaseFor returns the private case of the service.
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

// publicFinalizerCaseFor returns the public case of the service.
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

// typedUpdater takes an object of the resource's private type.
type typedUpdater func(ctx context.Context, object proto.Message, mask *fieldmaskpb.FieldMask) error

// publicFinalizerOps take objects of the resource's public type.
type publicFinalizerOps struct {
	get    func(ctx context.Context, id string) (proto.Message, error)
	update func(ctx context.Context, object proto.Message, mask *fieldmaskpb.FieldMask) error
}

type typedUpdateRequest[O any] interface {
	proto.Message
	SetObject(O)
	SetUpdateMask(*fieldmaskpb.FieldMask)
}

func privateUpdater[O proto.Message, U typedUpdateRequest[O], V any](
	update func(ctx context.Context, request U) (V, error),
) typedUpdater {
	return func(ctx context.Context, object proto.Message, mask *fieldmaskpb.FieldMask) error {
		request := newRequestOf[U]()
		request.SetObject(object.(O))
		request.SetUpdateMask(mask)
		_, err := update(ctx, request)
		return err
	}
}

func newRequestOf[T proto.Message]() T {
	var zero T
	return zero.ProtoReflect().New().Interface().(T)
}

// newPublicFinalizerOps adapts a public server; object extracts the object from the Get response.
func newPublicFinalizerOps[P proto.Message, G finalizerIDRequest, R any, U typedUpdateRequest[P], V any](
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

// publicFinalizerCase pairs a public server with the private service of its resource.
type publicFinalizerCase struct {
	service string
	ops     func() publicFinalizerOps
}

// withPublicLabel returns a copy of the public object with a test label added.
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

func withPublicDescriptionAndNoMetadata(object proto.Message) proto.Message {
	result := proto.Clone(object)
	result.(interface{ SetDescription(string) }).SetDescription("changed by the specs")
	result.(interface{ SetMetadata(*publicv1.Metadata) }).SetMetadata(nil)
	return result
}

// storeFailingSecretStore simulates an unavailable Vault once fail is set.
type storeFailingSecretStore struct {
	*memorySecretStore
	fail     bool
	failures int
}

// Store fails while fail is set, and otherwise stores in memory.
func (s *storeFailingSecretStore) Store(ctx context.Context, tenant, project, name string,
	data map[string][]byte) error {
	if s.fail {
		s.failures++
		return errors.New("vault is unavailable")
	}
	return s.memorySecretStore.Store(ctx, tenant, project, name, data)
}

// withOnlyIDAndMetadata avoids duplicating the spec on servers whose merge appends repeated fields.
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

func newBareMetalInstanceTypesUpdater() typedUpdater {
	GinkgoHelper()
	server, err := NewPrivateBareMetalInstanceTypesServer().
		SetLogger(logger).
		SetAttributionLogic(attribution).
		SetTenancyLogic(tenancy).
		Build()
	Expect(err).ToNot(HaveOccurred())
	return privateUpdater[*privatev1.BareMetalInstanceType](server.Update)
}

func newInstanceTypesUpdater() typedUpdater {
	GinkgoHelper()
	server, err := NewPrivateInstanceTypesServer().
		SetLogger(logger).
		SetAttributionLogic(attribution).
		SetTenancyLogic(tenancy).
		Build()
	Expect(err).ToNot(HaveOccurred())
	return privateUpdater[*privatev1.InstanceType](server.Update)
}

func newDiskImagesUpdater() typedUpdater {
	GinkgoHelper()
	server, err := NewPrivateDiskImagesServer().
		SetLogger(logger).
		SetAttributionLogic(attribution).
		SetTenancyLogic(tenancy).
		Build()
	Expect(err).ToNot(HaveOccurred())
	return privateUpdater[*privatev1.DiskImage](server.Update)
}

func newServiceAccountsUpdater() typedUpdater {
	GinkgoHelper()
	server, err := NewPrivateServiceAccountsServer().
		SetLogger(logger).
		SetAttributionLogic(attribution).
		SetTenancyLogic(tenancy).
		Build()
	Expect(err).ToNot(HaveOccurred())
	return privateUpdater[*privatev1.ServiceAccount](server.Update)
}

type finalizerRaceResult struct {
	writerErr       error
	releasedVersion int32
}

// raceAgainstRelease releases the finalizer in an open transaction, runs writer in a second one until it blocks on the
// row lock (detected via table), then commits the release so writer proceeds from a stale read.
func (t finalizerTarget) raceAgainstRelease(table string, writer func(ctx context.Context) error) finalizerRaceResult {
	GinkgoHelper()
	var result finalizerRaceResult

	// Commit the fixture so other transactions can see it.
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

	// Release without committing, so the row lock stays held.
	t.release(releaseCtx, finalizerController)
	result.releasedVersion = finalizerMetadataOf(t.get(releaseCtx)).GetVersion()

	writerTx, err = tm.Begin(context.Background())
	Expect(err).ToNot(HaveOccurred())
	writerCtx, cancel := context.WithTimeout(database.TxIntoContext(context.Background(), writerTx), 10*time.Second)
	defer cancel()
	writerDone = make(chan error, 1)
	go func() {
		defer GinkgoRecover()
		// Send a result even if writer panics, so the spec doesn't hang.
		writerErr := errors.New("the writer panicked")
		defer func() {
			writerDone <- writerErr
		}()
		writerErr = writer(writerCtx)
	}()

	// Wait until PostgreSQL reports the writer blocked on the lock.
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

	// Commit the release to unblock the writer.
	Expect(releaseTx.End(context.Background())).To(Succeed())
	releaseTx = nil
	result.writerErr = <-writerDone
	writerDone = nil
	Expect(writerTx.End(context.Background())).To(Succeed())
	writerTx = nil
	return result
}

// committedState reads the stored finalizers, version and UPDATE change count from a new transaction.
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

// expectStaleTypedWriteRejected writes back a copy read before another writer released the controller finalizer.
func expectStaleTypedWriteRejected(t finalizerTarget, update typedUpdater) {
	GinkgoHelper()
	stale := t.get(ctx)
	t.release(ctx, finalizerController)

	err := update(ctx, withFinalizers(stale, finalizerHold), finalizersMask())

	t.expectFinalizerConflict(ctx, err, finalizerController)
}

func verifyAssignmentDeleteUnsupported() {
	GinkgoHelper()
	f := newFinalizerAssignmentFixture(false)
	for _, object := range []*privatev1.TenantBillingAssignment{f.current, f.history} {
		_, err := f.private.Delete(f.caller, privatev1.TenantBillingAssignmentsDeleteRequest_builder{Id: object.GetId()}.Build())
		Expect(status.Code(err)).To(Equal(codes.Unimplemented),
			"private assignment Delete is now supported: replace the descriptor-only case with full CAS/lifecycle coverage")
		_, err = f.public.Delete(f.caller, publicv1.TenantBillingAssignmentsDeleteRequest_builder{Id: object.GetId()}.Build())
		Expect(status.Code(err)).To(Equal(codes.Unimplemented),
			"public assignment Delete is now supported: replace the descriptor-only case with full CAS/lifecycle coverage")
	}
	f.expectUnchanged()
	Expect(f.fake.Calls()).To(BeEmpty())
}

var _ = Describe("typed finalizer updates", func() {
	Describe("descriptor-only assignment lifecycle", func() {
		It("keeps private and public Delete unsupported without mutating current or closed history", verifyAssignmentDeleteUnsupported)

		DescribeTable("rejects a private finalizer mask without changing any stored field or calling the provider",
			func(mask *fieldmaskpb.FieldMask) {
				f := newFinalizerAssignmentFixture(false)
				object := withFinalizers(f.current).(*privatev1.TenantBillingAssignment)
				var changes int
				Expect(testPool.QueryRow(f.caller, `select count(*) from changes`).Scan(&changes)).To(Succeed())
				_, err := billingRPC(f.caller, f.private.Update, privatev1.TenantBillingAssignmentsUpdateRequest_builder{
					Object: object, UpdateMask: mask,
				}.Build())
				Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
				Expect(status.Convert(err).Message()).To(ContainSubstring("only atomic spec.billing_account is writable"))
				f.expectUnchanged()
				var after int
				Expect(testPool.QueryRow(f.caller, `select count(*) from changes`).Scan(&after)).To(Succeed())
				Expect(after).To(Equal(changes), "a rejected mask must not write any resource")
				Expect(f.fake.Calls()).To(BeEmpty())
			},
			Entry("finalizers only", finalizersMask()),
			Entry("finalizers and the writable account", finalizerMaskOf("metadata.finalizers", "spec.billing_account")),
		)

		DescribeTable("preserves private finalizers and all history through supported public updates",
			func(mask *fieldmaskpb.FieldMask, metadata string, deleting bool) {
				f := newFinalizerAssignmentFixture(deleting)
				Expect(f.current.GetMetadata().GetVersion()).To(BeNumerically(">", 0))
				Expect(f.current.GetMetadata().HasDeletionTimestamp()).To(Equal(deleting))
				object, err := f.public.toPublic(f.caller, f.current)
				Expect(err).ToNot(HaveOccurred())
				switch metadata {
				case "stale":
					object.GetMetadata().SetVersion(f.current.GetMetadata().GetVersion() - 1)
				case "missing":
					object.SetMetadata(nil)
				}
				// Keep the same canonical account: nil masks revalidate, they must not reassign or clear it.
				request := publicv1.TenantBillingAssignmentsUpdateRequest_builder{
					Object: object, UpdateMask: mask,
				}.Build()
				response, err := billingRPC(f.caller, f.public.Update, request)
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetObject().GetId()).To(Equal(f.current.GetId()))
				Expect(response.GetObject().GetSpec().GetBillingAccount().GetId()).To(Equal(f.current.GetSpec().GetBillingAccount().GetId()))
				if mask == nil {
					Expect(f.fake.Succeeded("GetAccount")).To(Equal(1))
					Expect(f.fake.Succeeded("ListAccounts")).To(Equal(1), "revalidation must check the token's carrier")
					Expect(response.GetObject().GetStatus().GetConnectionState()).To(Equal(
						publicv1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED))

					// Only the current interval's observation and version may change; billing never emits outbox events.
					expected := proto.Clone(f.current).(*privatev1.TenantBillingAssignment)
					expected.GetStatus().SetConnectionState(privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED)
					expected.GetStatus().SetMessage("")
					expected.GetMetadata().SetVersion(f.current.GetMetadata().GetVersion() + 1)
					f.expectStored(expected)

					// The same evidence still gets read on a second nil mask but must not write the assignment again.
					_, err = billingRPC(f.caller, f.public.Update, request)
					Expect(err).ToNot(HaveOccurred())
					Expect(f.fake.Succeeded("GetAccount")).To(Equal(2))
					Expect(f.fake.Succeeded("ListAccounts")).To(Equal(2))
					f.expectStored(expected)
				} else {
					f.expectUnchanged()
					Expect(f.fake.Calls()).To(BeEmpty(), "an empty mask must be a true no-op")
				}
				Expect(f.fake.Count("SetCorrelationValue")).To(BeZero(), "the configured read-only provider already has the matching token")
			},
			Entry("nil mask, current version", (*fieldmaskpb.FieldMask)(nil), "current", false),
			Entry("nil mask, stale version", (*fieldmaskpb.FieldMask)(nil), "stale", false),
			Entry("nil mask, missing metadata", (*fieldmaskpb.FieldMask)(nil), "missing", false),
			Entry("empty mask, stale version", &fieldmaskpb.FieldMask{}, "stale", false),
			Entry("nil mask, deleting with stale version", (*fieldmaskpb.FieldMask)(nil), "stale", true),
			Entry("nil mask, deleting without metadata", (*fieldmaskpb.FieldMask)(nil), "missing", true),
			Entry("empty mask, deleting without metadata", &fieldmaskpb.FieldMask{}, "missing", true),
		)

		It("rejects private and public updates to closed history before provider I/O", func() {
			f := newFinalizerAssignmentFixture(false)
			_, err := billingRPC(f.caller, f.private.Update, privatev1.TenantBillingAssignmentsUpdateRequest_builder{
				Object: withFinalizers(f.history).(*privatev1.TenantBillingAssignment), UpdateMask: finalizersMask(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			object, err := f.public.toPublic(f.caller, f.history)
			Expect(err).ToNot(HaveOccurred())
			object.SetMetadata(nil)
			_, err = billingRPC(f.caller, f.public.Update, publicv1.TenantBillingAssignmentsUpdateRequest_builder{Object: object}.Build())
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			f.expectUnchanged()
			Expect(f.fake.Calls()).To(BeEmpty())
		})
	})

	Describe("private wrappers with their own update path", func() {
		// Volumes merges the request into the stored object, ignoring metadata paths, before the generic update.
		It("rejects a stale finalizer write through the Volumes server", func() {
			update := privateUpdater[*privatev1.Volume](newFinalizerPrivateVolumesServer().Update)
			t := finalizerCaseFor(privatev1.Volumes_ServiceDesc.ServiceName).
				target([]string{finalizerController, finalizerHold})

			expectStaleTypedWriteRejected(t, update)
		})

		// ServiceAccount skips its preparation callback when the mask is exactly metadata.finalizers.
		It("rejects a stale finalizer write through the ServiceAccounts server", func() {
			update := newServiceAccountsUpdater()
			t := finalizerCaseFor(privatev1.ServiceAccounts_ServiceDesc.ServiceName).
				target([]string{finalizerController, finalizerHold})

			expectStaleTypedWriteRejected(t, update)
		})

		// Clusters validates every update, so the fixture must be a valid cluster.
		It("rejects a stale finalizer write through the Clusters server", func() {
			server, err := NewPrivateClustersServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			t := finalizerCaseFor(privatev1.Clusters_ServiceDesc.ServiceName).
				target([]string{finalizerController, finalizerHold})

			expectStaleTypedWriteRejected(t, privateUpdater[*privatev1.Cluster](server.Update))
		})
	})

	Describe("public servers that send a nil update mask", func() {
		// Tenants and BareMetalInstanceTemplates must stay Unimplemented or gain a case.
		It("covers every public server that has an Update method", func() {
			covered := map[string]bool{}
			for _, c := range slices.Concat(publicFinalizerCases(), catalogItemFinalizerCases()) {
				covered[string(protoreflect.FullName(c.service).Name())] = true
			}
			for _, c := range descriptorOnlyFinalizerCases() {
				c.verify()
				covered[string(protoreflect.FullName(c.service).Name())] = true
			}
			// Finalizers only matter for a resource that can be deleted. A public service whose private counterpart has
			// no Delete method never needs a finalizer case; every other public Update is still checked below.
			noPrivateDelete := map[string]bool{}
			privatePackage := (privatev1.EventType)(0).Descriptor().FullName().Parent()
			protoregistry.GlobalFiles.RangeFilesByPackage(privatePackage, func(file protoreflect.FileDescriptor) bool {
				services := file.Services()
				for i := range services.Len() {
					service := services.Get(i)
					noPrivateDelete[string(service.Name())] = service.Methods().ByName("Delete") == nil
				}
				return true
			})
			unimplemented := map[string]func() error{
				"Tenants": func() error {
					server, err := NewTenantsServer().
						SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
					Expect(err).ToNot(HaveOccurred())
					_, err = server.Update(ctx, &publicv1.TenantsUpdateRequest{})
					return err
				},
				"BareMetalInstanceTemplates": func() error {
					server, err := NewBareMetalInstanceTemplatesServer().
						SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
					Expect(err).ToNot(HaveOccurred())
					_, err = server.Update(ctx, &publicv1.BareMetalInstanceTemplatesUpdateRequest{})
					return err
				},
			}

			var missing, checked []string
			publicPackage := (&publicv1.Metadata{}).ProtoReflect().Descriptor().ParentFile().Package()
			protoregistry.GlobalFiles.RangeFilesByPackage(publicPackage, func(file protoreflect.FileDescriptor) bool {
				services := file.Services()
				for i := range services.Len() {
					service := services.Get(i)
					name := string(service.Name())
					switch {
					case service.Methods().ByName("Update") == nil || covered[name]:
					case noPrivateDelete[name]:
					case unimplemented[name] != nil:
						Expect(status.Code(unimplemented[name]())).To(Equal(codes.Unimplemented),
							"the public %s server implements Update now, so it needs a case", name)
						checked = append(checked, name)
					default:
						missing = append(missing, name)
					}
				}
				return true
			})
			Expect(missing).To(BeEmpty(), "public servers with an Update method that have no finalizer case")
			Expect(checked).To(ConsistOf("Tenants", "BareMetalInstanceTemplates"))
		})

		// Most servers map into an empty private object. Volumes, Clusters, BareMetalInstances, ProjectMemberships,
		// DiskImages and InstanceTypes map over the stored object, so they forward its finalizers.
		It("keeps the finalizers of the object", func() {
			runPublicFinalizerCases(publicFinalizerCases(), []string{finalizerController, finalizerHold},
				func(r *finalizerRun, c publicFinalizerCase, t finalizerTarget) {
					public := c.ops()

					object, err := public.get(r.ctx, t.id)
					Expect(err).ToNot(HaveOccurred())
					Expect(public.update(r.ctx, withPublicLabel(object), nil)).To(Succeed())

					Expect(t.finalizers(r.ctx)).To(ConsistOf(finalizerController, finalizerHold),
						"a public update must not clear the finalizers")
				})
		})

		It("keeps a deleting object", func() {
			runPublicFinalizerCases(publicFinalizerCases(), []string{finalizerController, finalizerHold},
				func(r *finalizerRun, c publicFinalizerCase, t finalizerTarget) {
					public := c.ops()
					t.markDeleting(r.ctx)

					object, err := public.get(r.ctx, t.id)
					Expect(err).ToNot(HaveOccurred())
					err = public.update(r.ctx, withPublicLabel(object), nil)
					if c.service == privatev1.SecurityGroups_ServiceDesc.ServiceName {
						// SecurityGroups refuses updates while deleting.
						Expect(status.Code(err)).To(Equal(codes.FailedPrecondition), "the update returned %v", err)
					} else {
						Expect(err).ToNot(HaveOccurred())
					}

					stored, err := t.ops.get(r.ctx, t.id)
					Expect(err).ToNot(HaveOccurred(), "a public update must not archive an object with finalizers")
					Expect(finalizersOf(stored)).To(ConsistOf(finalizerController, finalizerHold))
				})
		})
	})

	Describe("BareMetalInstanceType", func() {
		// This server applies masks itself before calling the generic update.
		var update typedUpdater

		BeforeEach(func() {
			update = newBareMetalInstanceTypesUpdater()
		})

		It("persists a finalizer added from the current version", func() {
			t := finalizerCaseFor(privatev1.BareMetalInstanceTypes_ServiceDesc.ServiceName).
				target([]string{finalizerController})

			object := withFinalizers(t.get(ctx), finalizerController, finalizerHold)
			Expect(update(ctx, object, finalizersMask())).To(Succeed())

			Expect(t.finalizers(ctx)).To(ConsistOf(finalizerController, finalizerHold))
		})

		It("rejects a stale finalizer write", func() {
			t := finalizerCaseFor(privatev1.BareMetalInstanceTypes_ServiceDesc.ServiceName).
				target([]string{finalizerController, finalizerHold})

			expectStaleTypedWriteRejected(t, update)
		})

		// Only id and metadata are sent: the merge appends repeated fields, so a full copy would double the hardware
		// ports and fail the immutability check before the finalizer check.
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

		// Without metadata the mask clears the finalizers. The wrapper replaces the request metadata with the merged
		// one, which would carry version zero, so it applies the generic no-metadata check first: invalid, not a
		// conflict.
		DescribeTable("rejects a finalizer change that has no metadata as invalid",
			func(bumpFirst bool) {
				t := finalizerCaseFor(privatev1.BareMetalInstanceTypes_ServiceDesc.ServiceName).
					target([]string{finalizerController, finalizerHold})
				if bumpFirst {
					t.bump(ctx)
				}

				err := update(ctx, withoutMetadata(t.get(ctx)), finalizersMask())

				t.expectFinalizerInvalid(ctx, err, finalizerController, finalizerHold)
			},
			Entry("at version 0", false),
			Entry("at a later version", true),
		)

		// Without a mask the merge keeps the stored metadata, so this is accepted at any version.
		It("accepts a request without metadata that leaves the finalizers as they are", func() {
			t := finalizerCaseFor(privatev1.BareMetalInstanceTypes_ServiceDesc.ServiceName).
				target([]string{finalizerController, finalizerHold})
			t.bump(ctx)

			Expect(update(ctx, withoutMetadata(withOnlyIDAndMetadata(t.get(ctx))), nil)).To(Succeed())

			Expect(t.finalizers(ctx)).To(ConsistOf(finalizerController, finalizerHold))
		})
	})

	Describe("private wrappers that merge the request into the stored object", func() {
		// These servers ignore metadata.finalizers when merging, so the stored finalizers are always forwarded.
		DescribeTable("keeps the finalizers when the request has no metadata",
			func(service string, newUpdate func() typedUpdater, bumpFirst bool) {
				update := newUpdate()
				t := finalizerCaseFor(service).target([]string{finalizerController, finalizerHold})
				if bumpFirst {
					t.bump(ctx)
				}

				err := update(ctx, withoutMetadata(t.get(ctx)), finalizersMask())

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

		// A nil mask forwards the request finalizers, so only the stale version stops the write.
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

		// These servers apply mask paths one by one and have no finalizer path.
		DescribeTable("ignores a finalizer path in the mask",
			func(service string, newUpdate func() typedUpdater) {
				update := newUpdate()
				t := finalizerCaseFor(service).target([]string{finalizerController, finalizerHold})

				removed := withFinalizers(t.get(ctx), finalizerController)
				Expect(update(ctx, removed, finalizersMask())).To(Succeed())
				Expect(t.finalizers(ctx)).To(ConsistOf(finalizerController, finalizerHold))

				added := withFinalizers(t.get(ctx), finalizerController, finalizerHold, finalizerForeign)
				Expect(update(ctx, added, finalizersMask())).To(Succeed())
				Expect(t.finalizers(ctx)).To(ConsistOf(finalizerController, finalizerHold))
			},
			Entry("InstanceTypes", privatev1.InstanceTypes_ServiceDesc.ServiceName, newInstanceTypesUpdater),
			Entry("DiskImages", privatev1.DiskImages_ServiceDesc.ServiceName, newDiskImagesUpdater),
		)
	})

	Describe("ServiceAccounts server", func() {
		// A missing mask is rewritten into a labels update, so stale request finalizers are ignored.
		It("leaves the finalizers alone when the mask is missing", func() {
			update := newServiceAccountsUpdater()
			t := finalizerCaseFor(privatev1.ServiceAccounts_ServiceDesc.ServiceName).
				target([]string{finalizerController, finalizerHold})
			stale := t.get(ctx)
			t.release(ctx, finalizerController)

			Expect(update(ctx, withLabel(stale, "x", "y"), nil)).To(Succeed())
			stored := t.get(ctx)
			Expect(finalizersOf(stored)).To(ConsistOf(finalizerController))
			Expect(finalizerMetadataOf(stored).GetLabels()).To(HaveKeyWithValue("x", "y"))
		})
	})

	// Secrets updates inside a savepoint, then stores data in Vault; a store failure rolls back the savepoint.
	Describe("Secrets server and its Vault store", func() {
		var (
			store  *storeFailingSecretStore
			server *PrivateSecretsServer
			logs   *osactesting.LogRecorder
			id     string
		)

		BeforeEach(func() {
			var err error
			store = &storeFailingSecretStore{memorySecretStore: newMemorySecretStore()}
			server, err = NewPrivateSecretsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				SetSecretStore(store).
				Build()
			Expect(err).ToNot(HaveOccurred())
			created, err := server.Create(ctx, privatev1.SecretsCreateRequest_builder{
				Object: privatev1.Secret_builder{
					Metadata: privatev1.Metadata_builder{Name: finalizerName("secret")}.Build(),
					Data:     map[string][]byte{"key": []byte("value")},
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id = created.GetObject().GetId()
			commitSuiteTx()
			logs = &osactesting.LogRecorder{}
			server.generic.logger = slog.New(logs)
		})

		// update also changes data so the request reaches the Vault store.
		update := func() error {
			current, err := server.Get(ctx, privatev1.SecretsGetRequest_builder{Id: id}.Build())
			Expect(err).ToNot(HaveOccurred())
			_, err = server.Update(ctx, privatev1.SecretsUpdateRequest_builder{
				Object: privatev1.Secret_builder{
					Id: id,
					Metadata: privatev1.Metadata_builder{
						Finalizers: []string{finalizerHold},
						Version:    current.GetObject().GetMetadata().GetVersion(),
					}.Build(),
					Data: map[string][]byte{"key": []byte("changed")},
				}.Build(),
				UpdateMask: finalizerMaskOf("data", "metadata.finalizers"),
			}.Build())
			return err
		}

		storedFinalizers := func() []string {
			GinkgoHelper()
			response, err := server.Get(ctx, privatev1.SecretsGetRequest_builder{Id: id}.Build())
			Expect(err).ToNot(HaveOccurred())
			return response.GetObject().GetMetadata().GetFinalizers()
		}

		It("logs nothing for a finalizer change that a failed Vault store rolls back", func() {
			store.fail = true

			Expect(update()).ToNot(Succeed())
			Expect(store.failures).To(Equal(1), "the request must have failed in the Vault store, after the update")
			commitSuiteTx()

			Expect(storedFinalizers()).To(BeEmpty(), "the finalizer change must have been rolled back")
			Expect(logs.WithMessage(finalizerChangeMessage)).To(BeEmpty())
		})

		It("logs the finalizer change once the Vault store succeeds and the transaction commits", func() {
			Expect(update()).To(Succeed())
			Expect(logs.WithMessage(finalizerChangeMessage)).To(BeEmpty(),
				"nothing must be logged before the transaction commits")
			commitSuiteTx()

			Expect(storedFinalizers()).To(ConsistOf(finalizerHold))
			Expect(logs.WithMessage(finalizerChangeMessage)).To(HaveLen(1))
		})
	})

	Describe("public servers that read the object before the update", func() {
		// These servers forward finalizers from an unlocked read, so the release must commit while the update waits
		// for the row lock.
		DescribeTable("does not restore a finalizer that was released while waiting for the row lock",
			func(service, table string) {
				t := finalizerCaseFor(service).target([]string{finalizerController, finalizerHold})
				public := publicFinalizerCaseFor(service).ops()
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
			Entry("Volumes", privatev1.Volumes_ServiceDesc.ServiceName, "volumes"),
			Entry("Clusters", privatev1.Clusters_ServiceDesc.ServiceName, "clusters"),
			Entry("DiskImages", privatev1.DiskImages_ServiceDesc.ServiceName, "disk_images"),
			Entry("InstanceTypes", privatev1.InstanceTypes_ServiceDesc.ServiceName, "instance_types"),
			Entry("BareMetalInstances", privatev1.BareMetalInstances_ServiceDesc.ServiceName, "bare_metal_instances"),
			Entry("ProjectMemberships", privatev1.ProjectMemberships_ServiceDesc.ServiceName, "project_memberships"),
		)
	})

	Describe("two transactions", func() {
		It("rejects a stale finalizer write that waited for the row lock and leaves no trace of it", func() {
			update := privateUpdater[*privatev1.Volume](newFinalizerPrivateVolumesServer().Update)
			t := finalizerCaseFor(privatev1.Volumes_ServiceDesc.ServiceName).
				target([]string{finalizerController, finalizerHold})
			stale := withFinalizers(t.get(ctx), finalizerHold)

			result := t.raceAgainstRelease("volumes", func(ctx context.Context) error {
				return update(ctx, stale, finalizersMask())
			})

			finalizers, version, updates := t.committedState("volumes")
			Expect(status.Code(result.writerErr)).To(Equal(codes.Aborted),
				"the stale write returned %v and the stored finalizers are %v", result.writerErr, finalizers)
			expectRejectionMessage(result.writerErr, t.id)
			Expect(finalizers).To(ConsistOf(finalizerController))
			Expect(version).To(Equal(result.releasedVersion), "the rejected write must not bump the version")
			Expect(updates).To(Equal(1), "only the release may have written a change record")
		})
	})

	Describe("catalog items, whose public servers copy the stored metadata when a nil-mask request has none", func() {
		// In the other cases the generic server restores the stored finalizers, as for every public update.
		DescribeTable("keeps the finalizers and the metadata",
			func(variant string) {
				runPublicFinalizerCases(catalogItemFinalizerCases(), []string{finalizerController, finalizerHold},
					func(r *finalizerRun, c publicFinalizerCase, t finalizerTarget) {
						public := c.ops()
						before := t.get(r.ctx)
						if variant == "deleting" {
							t.markDeleting(r.ctx)
						}

						object, err := public.get(r.ctx, t.id)
						Expect(err).ToNot(HaveOccurred())
						changed := withPublicLabel(object)
						if variant == "without metadata" {
							changed = withPublicDescriptionAndNoMetadata(object)
						}
						Expect(public.update(r.ctx, changed, nil)).To(Succeed())

						stored, err := t.ops.get(r.ctx, t.id)
						Expect(err).ToNot(HaveOccurred(), "a public update must not archive an object with finalizers")
						Expect(finalizersOf(stored)).To(ConsistOf(finalizerController, finalizerHold))
						Expect(finalizerMetadataOf(stored).GetName()).To(Equal(finalizerMetadataOf(before).GetName()))
						if variant == "without metadata" {
							Expect(finalizerMetadataOf(before).GetLabels()).ToNot(BeEmpty(),
								"the fixture must have labels")
							Expect(finalizerMetadataOf(stored).GetLabels()).To(
								Equal(finalizerMetadataOf(before).GetLabels()))
						}
					})
			},
			Entry("with metadata", "with metadata"),
			Entry("without metadata", "without metadata"),
			Entry("deleting", "deleting"),
		)
	})
})

// runPublicFinalizerCases is runFinalizerCases for public servers, seeding objects through their private cases.
func runPublicFinalizerCases(publicCases []publicFinalizerCase, finalizers []string,
	scenario func(r *finalizerRun, c publicFinalizerCase, t finalizerTarget)) {
	GinkgoHelper()
	byService := map[string]publicFinalizerCase{}
	cases := make([]finalizerCase, 0, len(publicCases))
	for _, c := range publicCases {
		byService[c.service] = c
		cases = append(cases, finalizerCaseFor(c.service))
	}
	runFinalizerCasesFor(cases, finalizers, func(r *finalizerRun, c finalizerCase, t finalizerTarget) {
		scenario(r, byService[c.service], t)
	})
}

// publicFinalizerCases excludes the catalog-items servers, which have their own cases.
func publicFinalizerCases() []publicFinalizerCase {
	return []publicFinalizerCase{
		{privatev1.Volumes_ServiceDesc.ServiceName, newPublicVolumesOps},
		{privatev1.Clusters_ServiceDesc.ServiceName, newPublicClustersOps},
		{privatev1.BareMetalInstances_ServiceDesc.ServiceName, func() publicFinalizerOps {
			GinkgoHelper()
			server, err := NewBareMetalInstancesServer().
				SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			return newPublicFinalizerOps(
				server.Get, server.Update, (*publicv1.BareMetalInstancesGetResponse).GetObject,
			)
		}},
		{privatev1.ComputeInstances_ServiceDesc.ServiceName, func() publicFinalizerOps {
			GinkgoHelper()
			server, err := NewComputeInstancesServer().
				SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			return newPublicFinalizerOps(
				server.Get, server.Update, (*publicv1.ComputeInstancesGetResponse).GetObject,
			)
		}},
		{privatev1.ClusterTemplates_ServiceDesc.ServiceName, func() publicFinalizerOps {
			GinkgoHelper()
			server, err := NewClusterTemplatesServer().
				SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			return newPublicFinalizerOps(
				server.Get, server.Update, (*publicv1.ClusterTemplatesGetResponse).GetObject,
			)
		}},
		{privatev1.ClusterVersions_ServiceDesc.ServiceName, func() publicFinalizerOps {
			GinkgoHelper()
			server, err := NewClusterVersionsServer().
				SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			return newPublicFinalizerOps(
				server.Get, server.Update, (*publicv1.ClusterVersionsGetResponse).GetObject,
			)
		}},
		{privatev1.ComputeInstanceTemplates_ServiceDesc.ServiceName, func() publicFinalizerOps {
			GinkgoHelper()
			server, err := NewComputeInstanceTemplatesServer().
				SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			return newPublicFinalizerOps(
				server.Get, server.Update, (*publicv1.ComputeInstanceTemplatesGetResponse).GetObject,
			)
		}},
		{privatev1.ProjectMemberships_ServiceDesc.ServiceName, func() publicFinalizerOps {
			GinkgoHelper()
			server, err := NewProjectMembershipsServer().
				SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			return newPublicFinalizerOps(
				server.Get, server.Update, (*publicv1.ProjectMembershipsGetResponse).GetObject,
			)
		}},
		{privatev1.SecurityGroups_ServiceDesc.ServiceName, func() publicFinalizerOps {
			GinkgoHelper()
			server, err := NewSecurityGroupsServer().
				SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			// This update request has no mask; it only updates the rule lists.
			return publicFinalizerOps{
				get: func(ctx context.Context, id string) (proto.Message, error) {
					response, err := server.Get(ctx, publicv1.SecurityGroupsGetRequest_builder{Id: id}.Build())
					return response.GetObject(), err
				},
				update: func(ctx context.Context, object proto.Message, mask *fieldmaskpb.FieldMask) error {
					Expect(mask).To(BeNil(), "the public SecurityGroups update has no mask")
					_, err := server.Update(ctx, publicv1.SecurityGroupsUpdateRequest_builder{
						Object: object.(*publicv1.SecurityGroup),
					}.Build())
					return err
				},
			}
		}},
		{privatev1.ServiceAccounts_ServiceDesc.ServiceName, func() publicFinalizerOps {
			GinkgoHelper()
			server, err := NewServiceAccountsServer().
				SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			return newPublicFinalizerOps(
				server.Get, server.Update, (*publicv1.ServiceAccountsGetResponse).GetObject,
			)
		}},
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
			permissionCatalog, err := catalog.Build(protoregistry.GlobalFiles)
			Expect(err).ToNot(HaveOccurred())
			server, err := NewRolesServer().
				SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).
				SetPermissionCatalog(permissionCatalog).Build()
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

func newPublicClustersOps() publicFinalizerOps {
	GinkgoHelper()
	server, err := NewClustersServer().
		SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
	Expect(err).ToNot(HaveOccurred())
	return newPublicFinalizerOps(
		server.Get, server.Update, (*publicv1.ClustersGetResponse).GetObject,
	)
}
