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
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/vault"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// These specs run the OSAC-7221 scenarios through the real typed servers, not only through the bare generic server:
// the private wrappers that rebuild the request before calling the generic update, and the public servers that map
// objects without finalizers into private ones.

// finalizerCaseFor returns the case of a resource by its fully qualified service name.
func finalizerCaseFor(service string) finalizerCase {
	for _, c := range finalizerCases() {
		if c.service == service {
			return c
		}
	}
	Fail("there is no finalizer case for " + service)
	return finalizerCase{}
}

// typedUpdater sends an update through a typed server. The object must be of the private type of the resource.
type typedUpdater func(ctx context.Context, object proto.Message, mask *fieldmaskpb.FieldMask) error

// publicFinalizerOps are the operations of a public server, on objects of the public type of the resource.
type publicFinalizerOps struct {
	get    func(ctx context.Context, id string) (proto.Message, error)
	update func(ctx context.Context, object proto.Message, mask *fieldmaskpb.FieldMask) error
}

// newPublicFinalizerOps adapts the typed calls of a public server.
func newPublicFinalizerOps[P proto.Message](
	get func(ctx context.Context, id string) (P, error),
	update func(ctx context.Context, object P, mask *fieldmaskpb.FieldMask) error,
) publicFinalizerOps {
	return publicFinalizerOps{
		get: func(ctx context.Context, id string) (proto.Message, error) {
			return get(ctx, id)
		},
		update: func(ctx context.Context, object proto.Message, mask *fieldmaskpb.FieldMask) error {
			return update(ctx, object.(P), mask)
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

// finalizerRaceResult is the outcome of a write that raced against the release of a finalizer.
type finalizerRaceResult struct {
	writerErr       error
	releasedVersion int32
}

// raceAgainstRelease commits the fixture and then plays the interleaving behind OSAC-7221 with two real transactions:
// one writer releases the hold with the lock and doesn't commit, a second writer starts and blocks on the row lock,
// and then the first one commits. The second writer has read the object before the commit, so what it writes is based
// on a stale view. The table is the one of the resource, used to see when the second writer is blocked.
func (t finalizerTarget) raceAgainstRelease(table string, writer func(ctx context.Context) error) finalizerRaceResult {
	var result finalizerRaceResult

	// Commit the fixture so that two independent database transactions can write the object:
	ExpectWithOffset(1, suiteTx.End(ctx)).To(Succeed())
	suiteTx = nil

	releaseTx, err := tm.Begin(context.Background())
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
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
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	writerCtx, cancel := context.WithTimeout(database.TxIntoContext(context.Background(), writerTx), 10*time.Second)
	defer cancel()
	writerDone = make(chan error, 1)
	go func() {
		defer GinkgoRecover()
		writerDone <- writer(writerCtx)
	}()

	// Wait until PostgreSQL reports that the second writer is blocked by the first one:
	EventuallyWithOffset(1, func() (bool, error) {
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
	ExpectWithOffset(1, releaseTx.End(context.Background())).To(Succeed())
	releaseTx = nil
	result.writerErr = <-writerDone
	writerDone = nil
	ExpectWithOffset(1, writerTx.End(context.Background())).To(Succeed())
	writerTx = nil
	return result
}

// committedState reads, from a new transaction, the finalizers and the version that are stored, and the number of
// update records that the database kept for the object.
func (t finalizerTarget) committedState(table string) (finalizers []string, version int32, updates int) {
	tx, err := tm.Begin(context.Background())
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	defer func() {
		ExpectWithOffset(1, tx.End(context.Background())).To(Succeed())
	}()
	freshCtx := database.TxIntoContext(context.Background(), tx)
	stored := t.get(freshCtx)
	err = tx.QueryRow(freshCtx, `
		select count(*)
		from changes
		where "table" = $1 and op = 'UPDATE' and data->>'id' = $2
	`, table, t.id).Scan(&updates)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return finalizersOf(stored), finalizerMetadataOf(stored).GetVersion(), updates
}

// expectStaleTypedWriteRejected runs the sequence behind OSAC-7221 through a typed server: the writer reads the
// object while the hold is present, another writer releases it, and then the first one writes back its stale view.
func expectStaleTypedWriteRejected(t finalizerTarget, update typedUpdater) {
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
			server, err := NewPrivateVolumesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				SetTierResolver(func(_ context.Context, _ string) (*TierResolution, error) {
					return &TierResolution{
						Provider: "test-provider",
						Protocol: privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
					}, nil
				}).
				Build()
			Expect(err).ToNot(HaveOccurred())
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

	Describe("public servers that read the object before the update", func() {
		// On a nil mask these servers do their own unlocked read of the private object and forward its finalizers.
		// The window is between that read and the locked read of the generic server, so a sequential spec can't
		// reproduce it: the other writer has to commit while the public update waits for the row lock.
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
		)
	})

	Describe("two transactions", func() {
		It("rejects a stale finalizer write that waited for the row lock and leaves no trace of it", func() {
			server, err := NewPrivateVolumesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				SetTierResolver(func(_ context.Context, _ string) (*TierResolution, error) {
					return &TierResolution{
						Provider: "test-provider",
						Protocol: privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
					}, nil
				}).
				Build()
			Expect(err).ToNot(HaveOccurred())
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

	Describe("catalog items, whose public servers keep their own copy of the finalizers", func() {
		// The three public catalog-items servers copy the stored finalizers into the private object themselves when
		// they get a nil mask, and the stored metadata as a whole when the request has none. These controls pin that
		// behaviour.
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
			server, err := NewProjectsServer().
				SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			return newPublicFinalizerOps(
				func(ctx context.Context, id string) (*publicv1.Project, error) {
					response, err := server.Get(ctx, publicv1.ProjectsGetRequest_builder{Id: id}.Build())
					return response.GetObject(), err
				},
				func(ctx context.Context, object *publicv1.Project, mask *fieldmaskpb.FieldMask) error {
					_, err := server.Update(ctx, publicv1.ProjectsUpdateRequest_builder{
						Object: object, UpdateMask: mask,
					}.Build())
					return err
				},
			)
		}},
		{privatev1.Roles_ServiceDesc.ServiceName, func() publicFinalizerOps {
			server, err := NewRolesServer().
				SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			return newPublicFinalizerOps(
				func(ctx context.Context, id string) (*publicv1.Role, error) {
					response, err := server.Get(ctx, publicv1.RolesGetRequest_builder{Id: id}.Build())
					return response.GetObject(), err
				},
				func(ctx context.Context, object *publicv1.Role, mask *fieldmaskpb.FieldMask) error {
					_, err := server.Update(ctx, publicv1.RolesUpdateRequest_builder{
						Object: object, UpdateMask: mask,
					}.Build())
					return err
				},
			)
		}},
		{privatev1.RoleBindings_ServiceDesc.ServiceName, func() publicFinalizerOps {
			server, err := NewRoleBindingsServer().
				SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			return newPublicFinalizerOps(
				func(ctx context.Context, id string) (*publicv1.RoleBinding, error) {
					response, err := server.Get(ctx, publicv1.RoleBindingsGetRequest_builder{Id: id}.Build())
					return response.GetObject(), err
				},
				func(ctx context.Context, object *publicv1.RoleBinding, mask *fieldmaskpb.FieldMask) error {
					_, err := server.Update(ctx, publicv1.RoleBindingsUpdateRequest_builder{
						Object: object, UpdateMask: mask,
					}.Build())
					return err
				},
			)
		}},
		{privatev1.IdentityProviders_ServiceDesc.ServiceName, func() publicFinalizerOps {
			server, err := NewIdentityProvidersServer().
				SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			return newPublicFinalizerOps(
				func(ctx context.Context, id string) (*publicv1.IdentityProvider, error) {
					response, err := server.Get(ctx, publicv1.IdentityProvidersGetRequest_builder{Id: id}.Build())
					return response.GetObject(), err
				},
				func(ctx context.Context, object *publicv1.IdentityProvider, mask *fieldmaskpb.FieldMask) error {
					_, err := server.Update(ctx, publicv1.IdentityProvidersUpdateRequest_builder{
						Object: object, UpdateMask: mask,
					}.Build())
					return err
				},
			)
		}},
		{privatev1.Users_ServiceDesc.ServiceName, func() publicFinalizerOps {
			server, err := NewUsersServer().
				SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			return newPublicFinalizerOps(
				func(ctx context.Context, id string) (*publicv1.User, error) {
					response, err := server.Get(ctx, publicv1.UsersGetRequest_builder{Id: id}.Build())
					return response.GetObject(), err
				},
				func(ctx context.Context, object *publicv1.User, mask *fieldmaskpb.FieldMask) error {
					_, err := server.Update(ctx, publicv1.UsersUpdateRequest_builder{
						Object: object, UpdateMask: mask,
					}.Build())
					return err
				},
			)
		}},
		{privatev1.Secrets_ServiceDesc.ServiceName, func() publicFinalizerOps {
			server, err := NewSecretsServer().
				SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).
				SetSecretStore(vault.NewMockSecretStore(ctrl)).Build()
			Expect(err).ToNot(HaveOccurred())
			return newPublicFinalizerOps(
				func(ctx context.Context, id string) (*publicv1.Secret, error) {
					response, err := server.Get(ctx, publicv1.SecretsGetRequest_builder{Id: id}.Build())
					return response.GetObject(), err
				},
				func(ctx context.Context, object *publicv1.Secret, mask *fieldmaskpb.FieldMask) error {
					_, err := server.Update(ctx, publicv1.SecretsUpdateRequest_builder{
						Object: object, UpdateMask: mask,
					}.Build())
					return err
				},
			)
		}},
		{privatev1.DiskImages_ServiceDesc.ServiceName, func() publicFinalizerOps {
			server, err := NewDiskImagesServer().
				SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			return newPublicFinalizerOps(
				func(ctx context.Context, id string) (*publicv1.DiskImage, error) {
					response, err := server.Get(ctx, publicv1.DiskImagesGetRequest_builder{Id: id}.Build())
					return response.GetObject(), err
				},
				func(ctx context.Context, object *publicv1.DiskImage, mask *fieldmaskpb.FieldMask) error {
					_, err := server.Update(ctx, publicv1.DiskImagesUpdateRequest_builder{
						Object: object, UpdateMask: mask,
					}.Build())
					return err
				},
			)
		}},
		{privatev1.InstanceTypes_ServiceDesc.ServiceName, func() publicFinalizerOps {
			server, err := NewInstanceTypesServer().
				SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			return newPublicFinalizerOps(
				func(ctx context.Context, id string) (*publicv1.InstanceType, error) {
					response, err := server.Get(ctx, publicv1.InstanceTypesGetRequest_builder{Id: id}.Build())
					return response.GetObject(), err
				},
				func(ctx context.Context, object *publicv1.InstanceType, mask *fieldmaskpb.FieldMask) error {
					_, err := server.Update(ctx, publicv1.InstanceTypesUpdateRequest_builder{
						Object: object, UpdateMask: mask,
					}.Build())
					return err
				},
			)
		}},
	}
}

// catalogItemFinalizerCases returns the three public catalog-items servers.
func catalogItemFinalizerCases() []publicFinalizerCase {
	return []publicFinalizerCase{
		{privatev1.ComputeInstanceCatalogItems_ServiceDesc.ServiceName, func() publicFinalizerOps {
			server, err := NewComputeInstanceCatalogItemsServer().
				SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			return newPublicFinalizerOps(
				func(ctx context.Context, id string) (*publicv1.ComputeInstanceCatalogItem, error) {
					response, err := server.Get(ctx, publicv1.ComputeInstanceCatalogItemsGetRequest_builder{
						Id: id,
					}.Build())
					return response.GetObject(), err
				},
				func(ctx context.Context, object *publicv1.ComputeInstanceCatalogItem,
					mask *fieldmaskpb.FieldMask) error {
					_, err := server.Update(ctx, publicv1.ComputeInstanceCatalogItemsUpdateRequest_builder{
						Object: object, UpdateMask: mask,
					}.Build())
					return err
				},
			)
		}},
		{privatev1.ClusterCatalogItems_ServiceDesc.ServiceName, func() publicFinalizerOps {
			server, err := NewClusterCatalogItemsServer().
				SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			return newPublicFinalizerOps(
				func(ctx context.Context, id string) (*publicv1.ClusterCatalogItem, error) {
					response, err := server.Get(ctx, publicv1.ClusterCatalogItemsGetRequest_builder{Id: id}.Build())
					return response.GetObject(), err
				},
				func(ctx context.Context, object *publicv1.ClusterCatalogItem, mask *fieldmaskpb.FieldMask) error {
					_, err := server.Update(ctx, publicv1.ClusterCatalogItemsUpdateRequest_builder{
						Object: object, UpdateMask: mask,
					}.Build())
					return err
				},
			)
		}},
		{privatev1.BareMetalInstanceCatalogItems_ServiceDesc.ServiceName, func() publicFinalizerOps {
			server, err := NewBareMetalInstanceCatalogItemsServer().
				SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			return newPublicFinalizerOps(
				func(ctx context.Context, id string) (*publicv1.BareMetalInstanceCatalogItem, error) {
					response, err := server.Get(ctx, publicv1.BareMetalInstanceCatalogItemsGetRequest_builder{
						Id: id,
					}.Build())
					return response.GetObject(), err
				},
				func(ctx context.Context, object *publicv1.BareMetalInstanceCatalogItem,
					mask *fieldmaskpb.FieldMask) error {
					_, err := server.Update(ctx, publicv1.BareMetalInstanceCatalogItemsUpdateRequest_builder{
						Object: object, UpdateMask: mask,
					}.Build())
					return err
				},
			)
		}},
	}
}

// newPublicVolumesOps returns the operations of the public Volumes server.
func newPublicVolumesOps() publicFinalizerOps {
	server, err := NewVolumesServer().
		SetLogger(logger).
		SetAttributionLogic(attribution).
		SetTenancyLogic(tenancy).
		SetTierResolver(func(_ context.Context, _ string) (*TierResolution, error) {
			return &TierResolution{
				Provider: "test-provider",
				Protocol: privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
			}, nil
		}).
		Build()
	Expect(err).ToNot(HaveOccurred())
	return newPublicFinalizerOps(
		func(ctx context.Context, id string) (*publicv1.Volume, error) {
			response, err := server.Get(ctx, publicv1.VolumesGetRequest_builder{Id: id}.Build())
			return response.GetObject(), err
		},
		func(ctx context.Context, object *publicv1.Volume, mask *fieldmaskpb.FieldMask) error {
			_, err := server.Update(ctx, publicv1.VolumesUpdateRequest_builder{
				Object: object, UpdateMask: mask,
			}.Build())
			return err
		},
	)
}

// newPublicClustersOps returns the operations of the public Clusters server.
func newPublicClustersOps() publicFinalizerOps {
	server, err := NewClustersServer().
		SetLogger(logger).
		SetAttributionLogic(attribution).
		SetTenancyLogic(tenancy).
		Build()
	Expect(err).ToNot(HaveOccurred())
	return newPublicFinalizerOps(
		func(ctx context.Context, id string) (*publicv1.Cluster, error) {
			response, err := server.Get(ctx, publicv1.ClustersGetRequest_builder{Id: id}.Build())
			return response.GetObject(), err
		},
		func(ctx context.Context, object *publicv1.Cluster, mask *fieldmaskpb.FieldMask) error {
			_, err := server.Update(ctx, publicv1.ClustersUpdateRequest_builder{
				Object: object, UpdateMask: mask,
			}.Build())
			return err
		},
	)
}
