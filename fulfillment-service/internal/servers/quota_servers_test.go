/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/collections"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Quota servers", func() {
	var (
		limitsServer     *QuotaLimitsServer
		thresholdsServer *QuotaThresholdsServer
		requestsServer   *QuotaLimitRequestsServer
		usageServer      *QuotaUsageServer
		limitsDao        *dao.GenericDAO[*privatev1.QuotaLimit]
		defaultsDao      *dao.GenericDAO[*privatev1.QuotaDefaultLimit]
	)

	BeforeEach(func() {
		var err error
		limitsServer, err = NewQuotaLimitsServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		thresholdsServer, err = NewQuotaThresholdsServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		requestsServer, err = NewQuotaLimitRequestsServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		usageServer, err = NewQuotaUsageServer().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		limitsDao, err = dao.NewGenericDAO[*privatev1.QuotaLimit]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		defaultsDao, err = dao.NewGenericDAO[*privatev1.QuotaDefaultLimit]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		projectsDao, err := dao.NewGenericDAO[*privatev1.Project]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		_, err = projectsDao.Create().
			SetObject(privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "team",
					Tenant: testTenant,
				}.Build(),
			}.Build()).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
	})

	createLimit := func(project string, dimension string, value int64) *publicv1.QuotaLimit {
		response, err := limitsServer.Create(ctx, publicv1.QuotaLimitsCreateRequest_builder{
			Object: publicv1.QuotaLimit_builder{
				Metadata: publicv1.Metadata_builder{
					Tenant:  testTenant,
					Project: project,
				}.Build(),
				Spec: publicv1.QuotaLimitSpec_builder{
					Dimension: dimension,
					Value:     proto.Int64(value),
				}.Build(),
			}.Build(),
		}.Build())
		ExpectWithOffset(1, err).ToNot(HaveOccurred())
		return response.GetObject()
	}

	Describe("Quota limits", func() {
		It("creates project limits and tenant overrides, deriving the name from the dimension", func() {
			override := createLimit("", "compute.vcpus", 10)
			Expect(override.GetMetadata().GetName()).To(Equal("compute-vcpus-" +
				quotaNameFromDimension("compute.vcpus")[len("compute-vcpus-"):]))
			Expect(override.GetMetadata().GetTenant()).To(Equal(testTenant))
			Expect(override.GetSpec().GetValue()).To(Equal(int64(10)))

			project := createLimit("team", "compute.vcpus", 4)
			Expect(project.GetMetadata().GetProject()).To(Equal("team"))
		})

		It("rejects a second limit of the same dimension in the same scope", func() {
			createLimit("", "compute.vcpus", 10)
			_, err := limitsServer.Create(ctx, publicv1.QuotaLimitsCreateRequest_builder{
				Object: publicv1.QuotaLimit_builder{
					Metadata: publicv1.Metadata_builder{Tenant: testTenant}.Build(),
					Spec:     publicv1.QuotaLimitSpec_builder{Dimension: "compute.vcpus", Value: proto.Int64(20)}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.AlreadyExists))
		})

		It("rejects a limit without a dimension", func() {
			_, err := limitsServer.Create(ctx, publicv1.QuotaLimitsCreateRequest_builder{
				Object: publicv1.QuotaLimit_builder{
					Metadata: publicv1.Metadata_builder{Tenant: testTenant}.Build(),
					Spec:     publicv1.QuotaLimitSpec_builder{Value: proto.Int64(10)}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		})

		It("derives a name within the metadata.name bound from a long dimension", func() {
			limit := createLimit("", "bare_metal.instances."+strings.Repeat("h", 63), 1)
			Expect(limit.GetMetadata().GetName()).
				To(MatchRegexp(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`))
		})

		It("falls back to the quota base for a dimension of only punctuation", func() {
			// "!!!" sanitizes to nothing, so the name keeps the quota base and its
			// hash suffix instead:
			limit := createLimit("", "!!!", 1)
			Expect(limit.GetMetadata().GetName()).To(MatchRegexp(`^quota-[0-9a-f]{8}$`))
		})

		It("updates only the value", func() {
			limit := createLimit("", "compute.vcpus", 10)
			_, err := limitsServer.Update(ctx, publicv1.QuotaLimitsUpdateRequest_builder{
				Object: publicv1.QuotaLimit_builder{
					Id:       limit.GetId(),
					Metadata: limit.GetMetadata(),
					Spec:     publicv1.QuotaLimitSpec_builder{Dimension: "compute.memory_gib", Value: proto.Int64(20)}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))

			updated, err := limitsServer.Update(ctx, publicv1.QuotaLimitsUpdateRequest_builder{
				Object: publicv1.QuotaLimit_builder{
					Id:       limit.GetId(),
					Metadata: limit.GetMetadata(),
					Spec:     publicv1.QuotaLimitSpec_builder{Dimension: "compute.vcpus", Value: proto.Int64(20)}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updated.GetObject().GetSpec().GetValue()).To(Equal(int64(20)))
		})

		It("deletes a limit", func() {
			limit := createLimit("", "compute.vcpus", 10)
			_, err := limitsServer.Delete(ctx, publicv1.QuotaLimitsDeleteRequest_builder{
				Id: limit.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			_, err = limitsServer.Get(ctx, publicv1.QuotaLimitsGetRequest_builder{Id: limit.GetId()}.Build())
			Expect(status.Code(err)).To(Equal(codes.NotFound))
		})

		It("lists the limits of a dimension", func() {
			createLimit("", "compute.vcpus", 10)
			createLimit("team", "compute.vcpus", 4)
			createLimit("team", "compute.memory_gib", 8)
			response, err := limitsServer.List(ctx, publicv1.QuotaLimitsListRequest_builder{
				Filter: new(`this.spec.dimension == "compute.vcpus"`),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetTotal()).To(Equal(int32(2)))
		})

		It("refuses a write to a tenant override without total visibility", func() {
			visibility, err := auth.NewVisibility().
				AddVisibleTenants(testTenant).
				SetTotalProjectVisibility(testTenant).
				Build()
			Expect(err).ToNot(HaveOccurred())
			restricted := auth.NewMockTenancyLogic(ctrl)
			restricted.EXPECT().DetermineAssignableTenants(gomock.Any()).
				Return(collections.NewSet(testTenant), nil).
				AnyTimes()
			restricted.EXPECT().DetermineDefaultTenant(gomock.Any()).
				Return(testTenant, nil).
				AnyTimes()
			restricted.EXPECT().DetermineVisibility(gomock.Any()).
				Return(visibility, nil).
				AnyTimes()
			server, err := NewQuotaLimitsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(restricted).
				Build()
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Create(ctx, publicv1.QuotaLimitsCreateRequest_builder{
				Object: publicv1.QuotaLimit_builder{
					Metadata: publicv1.Metadata_builder{Tenant: testTenant}.Build(),
					Spec:     publicv1.QuotaLimitSpec_builder{Dimension: "compute.vcpus", Value: proto.Int64(100)}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.PermissionDenied))

			// A project limit is the tenant admin's to set:
			_, err = server.Create(ctx, publicv1.QuotaLimitsCreateRequest_builder{
				Object: publicv1.QuotaLimit_builder{
					Metadata: publicv1.Metadata_builder{Tenant: testTenant, Project: "team"}.Build(),
					Spec:     publicv1.QuotaLimitSpec_builder{Dimension: "compute.vcpus", Value: proto.Int64(4)}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// An update cannot smuggle a project past the gate either: the mask drops
			// every field it does not name, so a body that names a visible project
			// next to a mask of spec.value leaves the stored override's empty project
			// in place. The stored scope decides, the way Delete decides it:
			override := createLimit("", "compute.vcpus", 4)
			_, err = server.Update(ctx, publicv1.QuotaLimitsUpdateRequest_builder{
				Object: publicv1.QuotaLimit_builder{
					Id:       override.GetId(),
					Metadata: publicv1.Metadata_builder{Tenant: testTenant, Project: "team"}.Build(),
					Spec:     publicv1.QuotaLimitSpec_builder{Dimension: "compute.vcpus", Value: proto.Int64(100)}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.value"}},
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.PermissionDenied))

			// Another tenant's limits stay invisible:
			_, err = limitsDao.Create().
				SetObject(privatev1.QuotaLimit_builder{
					Metadata: privatev1.Metadata_builder{Name: "other", Tenant: "shared"}.Build(),
					Spec:     privatev1.QuotaLimitSpec_builder{Dimension: "images.count", Value: proto.Int64(9)}.Build(),
				}.Build()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			response, err := server.List(ctx, publicv1.QuotaLimitsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			for _, item := range response.GetItems() {
				Expect(item.GetMetadata().GetTenant()).To(Equal(testTenant))
			}

			// A cloud provider admin, who sees every tenant, updates the override:
			updated, err := limitsServer.Update(ctx, publicv1.QuotaLimitsUpdateRequest_builder{
				Object: publicv1.QuotaLimit_builder{
					Id:       override.GetId(),
					Metadata: override.GetMetadata(),
					Spec:     publicv1.QuotaLimitSpec_builder{Dimension: "compute.vcpus", Value: proto.Int64(100)}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.value"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updated.GetObject().GetSpec().GetValue()).To(Equal(int64(100)))
		})
	})

	Describe("Default limits", func() {
		setDefault := func(dimension string, value int64) *publicv1.QuotaDefaultLimit {
			response, err := limitsServer.SetDefault(ctx, publicv1.QuotaLimitsSetDefaultRequest_builder{
				Object: publicv1.QuotaDefaultLimit_builder{
					Spec: publicv1.QuotaDefaultLimitSpec_builder{
						Dimension: dimension,
						Value:     proto.Int64(value),
					}.Build(),
				}.Build(),
			}.Build())
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			return response.GetObject()
		}

		It("recovers a lost default insert race by updating the winner", func() {
			// The race this pins: two transactions insert a default of one fresh
			// dimension at the same time. The winner's insert runs first and stays
			// uncommitted; the loser probes, sees nothing, and inserts, which waits on
			// the winner's unique index entry. The winner commits, the loser's insert
			// fails, and the recovery re-reads the winner's row and updates it.
			dimension := "race.winner.recovery"
			winner, err := tm.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = winner.Exec(ctx,
				`insert into quota_default_limits (id, name, creator, tenant, data)
				values ('race-winner', $1, 'system', 'shared', $2::jsonb)`,
				quotaNameFromDimension(dimension),
				`{"spec":{"dimension":"`+dimension+`","value":"10"}}`,
			)
			Expect(err).ToNot(HaveOccurred())

			loser, err := tm.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())
			// A bug that never unblocks the loser makes its insert fail fast instead
			// of holding the connection until the suite times out.
			_, err = loser.Exec(ctx, `set local lock_timeout = '5s'`)
			Expect(err).ToNot(HaveOccurred())
			var loserPID int
			Expect(loser.QueryRow(ctx, `select pg_backend_pid()`).Scan(&loserPID)).To(Succeed())

			loserCtx, cancel := context.WithTimeout(database.TxIntoContext(ctx, loser), 20*time.Second)
			DeferCleanup(cancel)
			results := make(chan error, 1)
			done := make(chan struct{})
			go func() {
				defer close(done)
				defer GinkgoRecover()
				_, upsertErr := upsertQuotaLimit(loserCtx, defaultsDao,
					fmt.Sprintf("this.spec.dimension == %q", dimension),
					privatev1.QuotaDefaultLimit_builder{
						Metadata: privatev1.Metadata_builder{
							Name:    quotaNameFromDimension(dimension),
							Tenant:  "shared",
							Creator: "system",
						}.Build(),
						Spec: privatev1.QuotaDefaultLimitSpec_builder{
							Dimension: dimension,
							Value:     proto.Int64(20),
						}.Build(),
					}.Build(),
					func(existing, fresh *privatev1.QuotaDefaultLimit) *privatev1.QuotaDefaultLimit {
						existing.SetSpec(fresh.GetSpec())
						return existing
					})
				results <- upsertErr
			}()
			// Whatever fails, commit the winner so the loser unblocks, wait for it,
			// and close its transaction. No path leaves a connection stuck.
			DeferCleanup(func() {
				if winner != nil {
					Expect(winner.End(context.Background())).To(Succeed())
					winner = nil
				}
				if loser != nil {
					cancel()
					Eventually(done, 5*time.Second).Should(BeClosed())
					Expect(loser.End(context.Background())).To(Succeed())
					loser = nil
				}
			})

			// Wait until the loser's insert asks for a lock it cannot have. Poll on a
			// separate connection: the suite's transaction keeps one view for its
			// whole life and would never see the loser move.
			Eventually(func() (bool, error) {
				var blocked bool
				err := testPool.QueryRow(context.Background(), `
					select exists (
						select 1 from pg_locks where pid = $1 and not granted
					)`, loserPID).Scan(&blocked)
				return blocked, err
			}, 10*time.Second, 10*time.Millisecond).Should(BeTrue())

			// The winner commits, so the loser's insert fails and the recovery runs:
			Expect(winner.End(ctx)).To(Succeed())
			winner = nil
			Eventually(results, 10*time.Second).Should(Receive(BeNil()))
			Expect(loser.End(ctx)).To(Succeed())
			loser = nil

			// The winner's row survived and now carries the loser's value. Read on a
			// separate connection: the suite's transaction cannot see committed rows.
			var value string
			Expect(testPool.QueryRow(context.Background(),
				`select data -> 'spec' ->> 'value' from quota_default_limits where id = 'race-winner'`).
				Scan(&value)).To(Succeed())
			Expect(value).To(Equal("20"))
		})

		It("recovers a lost default insert race through the savepoint", func() {
			// The race this pins: two concurrent SetDefaults of one fresh dimension.
			// Both probes miss, both inserts run, the winner commits first, and the loser's
			// insert hits the unique index. The DAO translates the violation to its own
			// error and records it in the managed transaction, which without the savepoint
			// would roll the whole request back; the savepoint isolates the insert, the
			// loser re-probes, finds the winner's row, and updates it.
			//
			// The race itself needs two transactions probing before either commits, which
			// the suite's shared transaction cannot arrange. This test pins the recovery
			// through the real path instead: the probe misses because it filters for
			// another dimension, the insert collides with a row the probe never saw, and
			// the recovery re-probes and fails to find the winner under that filter.
			_, err := limitsServer.SetDefault(ctx, publicv1.QuotaLimitsSetDefaultRequest_builder{
				Object: publicv1.QuotaDefaultLimit_builder{
					Spec: publicv1.QuotaDefaultLimitSpec_builder{
						Dimension: "race.savepoint.recovery",
						Value:     proto.Int64(10),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// The loser: a fresh object of the same dimension, probed with a filter that
			// matches no row, so the upsert takes the insert path and collides.
			_, err = upsertQuotaLimit(ctx, defaultsDao,
				`this.spec.dimension == "race.savepoint.absent"`,
				privatev1.QuotaDefaultLimit_builder{
					Metadata: privatev1.Metadata_builder{
						Name:    "race-loser",
						Tenant:  "shared",
						Creator: "system",
					}.Build(),
					Spec: privatev1.QuotaDefaultLimitSpec_builder{
						Dimension: "race.savepoint.recovery",
						Value:     proto.Int64(20),
					}.Build(),
				}.Build(),
				func(existing, fresh *privatev1.QuotaDefaultLimit) *privatev1.QuotaDefaultLimit {
					existing.SetSpec(fresh.GetSpec())
					return existing
				})
			// The insert lost the race, the savepoint kept the transaction alive, and the
			// recovery found no row under its own filter:
			Expect(err).To(MatchError(ContainSubstring("vanished from its unique scope")))

			// The transaction stayed usable, and the winner's row is untouched:
			second := setDefault("race.savepoint.recovery", 30)
			Expect(second.GetSpec().GetValue()).To(Equal(int64(30)))
		})

		It("refuses a default whose name another dimension already holds", func() {
			// Clear the second dimension's default, in case the suite seeds
			// one, so the spec takes the insert path:
			tx, err := database.TxFromContext(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = tx.Exec(ctx,
				`delete from quota_default_limits where data -> 'spec' ->> 'dimension' = 'images.count'`)
			Expect(err).ToNot(HaveOccurred())
			first := setDefault("compute.vcpus", 10)
			// The second default reuses the first's name for another dimension:
			_, err = limitsServer.SetDefault(ctx, publicv1.QuotaLimitsSetDefaultRequest_builder{
				Object: publicv1.QuotaDefaultLimit_builder{
					Metadata: publicv1.Metadata_builder{
						Name: first.GetMetadata().GetName(),
					}.Build(),
					Spec: publicv1.QuotaDefaultLimitSpec_builder{
						Dimension: "images.count",
						Value:     proto.Int64(10),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.AlreadyExists))
		})

		It("refuses a default that changes the name of an existing dimension's row", func() {
			setDefault("compute.vcpus", 10)
			_, err := limitsServer.SetDefault(ctx, publicv1.QuotaLimitsSetDefaultRequest_builder{
				Object: publicv1.QuotaDefaultLimit_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "another-name",
					}.Build(),
					Spec: publicv1.QuotaDefaultLimitSpec_builder{
						Dimension: "compute.vcpus",
						Value:     proto.Int64(20),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		})

		It("sets, lists, gets and clears defaults, replacing the value", func() {
			first := setDefault("compute.vcpus", 10)
			Expect(first.GetMetadata().GetName()).ToNot(BeEmpty())
			Expect(first.GetSpec().GetValue()).To(Equal(int64(10)))

			second := setDefault("compute.vcpus", 20)
			Expect(second.GetId()).To(Equal(first.GetId()))
			Expect(second.GetSpec().GetValue()).To(Equal(int64(20)))
			Expect(second.GetMetadata().GetVersion()).To(Equal(first.GetMetadata().GetVersion() + 1))

			setDefault("network.virtual_networks", 2)
			response, err := limitsServer.ListDefaults(ctx, publicv1.QuotaLimitsListDefaultsRequest_builder{
				Filter: new(`this.spec.dimension == "compute.vcpus"`),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetTotal()).To(Equal(int32(1)))

			getResponse, err := limitsServer.GetDefault(ctx, publicv1.QuotaLimitsGetDefaultRequest_builder{
				Id: first.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetSpec().GetValue()).To(Equal(int64(20)))

			_, err = limitsServer.ClearDefault(ctx, publicv1.QuotaLimitsClearDefaultRequest_builder{
				Id: first.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			_, err = limitsServer.GetDefault(ctx, publicv1.QuotaLimitsGetDefaultRequest_builder{
				Id: first.GetId(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.NotFound))
		})

		It("rejects a default without a dimension", func() {
			_, err := limitsServer.SetDefault(ctx, publicv1.QuotaLimitsSetDefaultRequest_builder{
				Object: publicv1.QuotaDefaultLimit_builder{
					Spec: publicv1.QuotaDefaultLimitSpec_builder{Value: proto.Int64(10)}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		})
	})

	Describe("Quota thresholds", func() {
		It("refuses a threshold on a scope with no limit of the dimension", func() {
			_, err := thresholdsServer.Create(ctx, publicv1.QuotaThresholdsCreateRequest_builder{
				Object: publicv1.QuotaThreshold_builder{
					Metadata: publicv1.Metadata_builder{Tenant: testTenant, Project: "team"}.Build(),
					Spec:     publicv1.QuotaThresholdSpec_builder{Dimension: "compute.vcpus", Percent: int32Ptr(80)}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
		})

		It("refuses a threshold without a percent, or with one out of bounds", func() {
			createLimit("team", "compute.vcpus", 4)
			for _, percent := range []int32{0, 101} {
				_, err := thresholdsServer.Create(ctx, publicv1.QuotaThresholdsCreateRequest_builder{
					Object: publicv1.QuotaThreshold_builder{
						Metadata: publicv1.Metadata_builder{Tenant: testTenant, Project: "team"}.Build(),
						Spec:     publicv1.QuotaThresholdSpec_builder{Dimension: "compute.vcpus", Percent: int32Ptr(percent)}.Build(),
					}.Build(),
				}.Build())
				Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			}
			_, err := thresholdsServer.Create(ctx, publicv1.QuotaThresholdsCreateRequest_builder{
				Object: publicv1.QuotaThreshold_builder{
					Metadata: publicv1.Metadata_builder{Tenant: testTenant, Project: "team"}.Build(),
					Spec:     publicv1.QuotaThresholdSpec_builder{Dimension: "compute.vcpus"}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		})

		It("creates a threshold for a dimension the project limits", func() {
			createLimit("team", "compute.vcpus", 4)
			response, err := thresholdsServer.Create(ctx, publicv1.QuotaThresholdsCreateRequest_builder{
				Object: publicv1.QuotaThreshold_builder{
					Metadata: publicv1.Metadata_builder{Tenant: testTenant, Project: "team"}.Build(),
					Spec:     publicv1.QuotaThresholdSpec_builder{Dimension: "compute.vcpus", Percent: int32Ptr(80)}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetSpec().GetPercent()).To(Equal(int32(80)))

			// One threshold per dimension per scope:
			_, err = thresholdsServer.Create(ctx, publicv1.QuotaThresholdsCreateRequest_builder{
				Object: publicv1.QuotaThreshold_builder{
					Metadata: publicv1.Metadata_builder{Tenant: testTenant, Project: "team"}.Build(),
					Spec:     publicv1.QuotaThresholdSpec_builder{Dimension: "compute.vcpus", Percent: int32Ptr(60)}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.AlreadyExists))
		})

		It("creates a threshold of the tenant's own scope, next to a project's", func() {
			createLimit("", "compute.vcpus", 10)
			_, err := thresholdsServer.Create(ctx, publicv1.QuotaThresholdsCreateRequest_builder{
				Object: publicv1.QuotaThreshold_builder{
					Metadata: publicv1.Metadata_builder{Tenant: testTenant}.Build(),
					Spec:     publicv1.QuotaThresholdSpec_builder{Dimension: "compute.vcpus", Percent: int32Ptr(90)}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			// The tenant's own threshold and a project's watch the same dimension without
			// colliding, because they sit at different scopes:
			createLimit("team", "compute.vcpus", 4)
			_, err = thresholdsServer.Create(ctx, publicv1.QuotaThresholdsCreateRequest_builder{
				Object: publicv1.QuotaThreshold_builder{
					Metadata: publicv1.Metadata_builder{Tenant: testTenant, Project: "team"}.Build(),
					Spec:     publicv1.QuotaThresholdSpec_builder{Dimension: "compute.vcpus", Percent: int32Ptr(80)}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("accepts a threshold of a project whose ancestor limits the dimension", func() {
			// The threshold check also accepts a default of the dimension, so clear any
			// before the threshold: only the ancestor's limit may satisfy it:
			tx, err := database.TxFromContext(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = tx.Exec(ctx,
				`delete from quota_default_limits where data -> 'spec' ->> 'dimension' = 'compute.vcpus'`)
			Expect(err).ToNot(HaveOccurred())

			nestedDao, err := dao.NewGenericDAO[*privatev1.Project]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = nestedDao.Create().
				SetObject(privatev1.Project_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "team.web",
						Tenant: testTenant,
					}.Build(),
				}.Build()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			createLimit("team", "compute.vcpus", 4)
			_, err = thresholdsServer.Create(ctx, publicv1.QuotaThresholdsCreateRequest_builder{
				Object: publicv1.QuotaThreshold_builder{
					Metadata: publicv1.Metadata_builder{Tenant: testTenant, Project: "team.web"}.Build(),
					Spec:     publicv1.QuotaThresholdSpec_builder{Dimension: "compute.vcpus", Percent: int32Ptr(80)}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Describe("Quota limit requests", func() {
		createRequest := func(dimension string, value int64) *publicv1.QuotaLimitRequest {
			response, err := requestsServer.Create(ctx, publicv1.QuotaLimitRequestsCreateRequest_builder{
				Object: publicv1.QuotaLimitRequest_builder{
					Metadata: publicv1.Metadata_builder{Tenant: testTenant}.Build(),
					Spec: publicv1.QuotaLimitRequestSpec_builder{
						Dimension:      dimension,
						RequestedValue: proto.Int64(value),
					}.Build(),
				}.Build(),
			}.Build())
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			return response.GetObject()
		}

		listLimits := func(dimension string) []*privatev1.QuotaLimit {
			response, err := limitsDao.List().
				SetFilter(`this.spec.dimension == "` + dimension + `" && this.metadata.tenant == "` + testTenant + `"`).
				Do(ctx)
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			return response.GetItems()
		}

		It("holds a request as pending and rejects a client-supplied status", func() {
			request := createRequest("compute.vcpus", 20)
			Expect(request.GetStatus().GetState()).To(Equal(publicv1.QuotaLimitRequestState_QUOTA_LIMIT_REQUEST_STATE_PENDING))

			_, err := requestsServer.Create(ctx, publicv1.QuotaLimitRequestsCreateRequest_builder{
				Object: publicv1.QuotaLimitRequest_builder{
					Metadata: publicv1.Metadata_builder{Tenant: testTenant}.Build(),
					Spec: publicv1.QuotaLimitRequestSpec_builder{
						Dimension:      "compute.memory_gib",
						RequestedValue: proto.Int64(64),
					}.Build(),
					Status: publicv1.QuotaLimitRequestStatus_builder{
						State: publicv1.QuotaLimitRequestState_QUOTA_LIMIT_REQUEST_STATE_APPROVED,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		})

		It("updates a pending request that omits the status, and rejects an explicit state", func() {
			privateServer, err := NewPrivateQuotaLimitRequestsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			created, err := privateServer.Create(ctx, privatev1.QuotaLimitRequestsCreateRequest_builder{
				Object: privatev1.QuotaLimitRequest_builder{
					Metadata: privatev1.Metadata_builder{Tenant: testTenant}.Build(),
					Spec: privatev1.QuotaLimitRequestSpec_builder{
						Dimension:      "compute.vcpus",
						RequestedValue: proto.Int64(20),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			request := created.GetObject()

			updated, err := privateServer.Update(ctx, privatev1.QuotaLimitRequestsUpdateRequest_builder{
				Object: privatev1.QuotaLimitRequest_builder{
					Id:       request.GetId(),
					Metadata: request.GetMetadata(),
					Spec: privatev1.QuotaLimitRequestSpec_builder{
						Dimension:      request.GetSpec().GetDimension(),
						RequestedValue: proto.Int64(30),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updated.GetObject().GetStatus().GetState()).
				To(Equal(privatev1.QuotaLimitRequestState_QUOTA_LIMIT_REQUEST_STATE_PENDING))
			Expect(updated.GetObject().GetSpec().GetRequestedValue()).To(Equal(int64(30)))

			_, err = privateServer.Update(ctx, privatev1.QuotaLimitRequestsUpdateRequest_builder{
				Object: privatev1.QuotaLimitRequest_builder{
					Id:       request.GetId(),
					Metadata: request.GetMetadata(),
					Spec:     request.GetSpec(),
					Status: privatev1.QuotaLimitRequestStatus_builder{
						State: privatev1.QuotaLimitRequestState_QUOTA_LIMIT_REQUEST_STATE_APPROVED,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))

			// The omitted status left the request pending, so a review can still act on it:
			_, err = requestsServer.Approve(ctx, publicv1.QuotaLimitRequestsApproveRequest_builder{
				Id: request.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("rejects a second pending request of the same dimension", func() {
			createRequest("compute.vcpus", 20)
			_, err := requestsServer.Create(ctx, publicv1.QuotaLimitRequestsCreateRequest_builder{
				Object: publicv1.QuotaLimitRequest_builder{
					Metadata: publicv1.Metadata_builder{Tenant: testTenant}.Build(),
					Spec: publicv1.QuotaLimitRequestSpec_builder{
						Dimension:      "compute.vcpus",
						RequestedValue: proto.Int64(30),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.AlreadyExists))
		})

		It("approves a request and sets the limit", func() {
			request := createRequest("compute.vcpus", 20)
			_, err := requestsServer.Approve(ctx, publicv1.QuotaLimitRequestsApproveRequest_builder{
				Id: request.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			limits := listLimits("compute.vcpus")
			Expect(limits).To(HaveLen(1))
			Expect(limits[0].GetSpec().GetValue()).To(Equal(int64(20)))
			Expect(limits[0].GetMetadata().GetProject()).To(BeEmpty())

			// The approved request is terminal:
			_, err = requestsServer.Approve(ctx, publicv1.QuotaLimitRequestsApproveRequest_builder{
				Id: request.GetId(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			_, err = requestsServer.Update(ctx, publicv1.QuotaLimitRequestsUpdateRequest_builder{
				Object: publicv1.QuotaLimitRequest_builder{
					Id:       request.GetId(),
					Metadata: request.GetMetadata(),
					Spec:     request.GetSpec(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
		})

		It("approving a request replaces an existing override", func() {
			createLimit("", "compute.vcpus", 10)
			request := createRequest("compute.vcpus", 20)
			_, err := requestsServer.Approve(ctx, publicv1.QuotaLimitRequestsApproveRequest_builder{
				Id: request.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			limits := listLimits("compute.vcpus")
			Expect(limits).To(HaveLen(1))
			Expect(limits[0].GetSpec().GetValue()).To(Equal(int64(20)))
		})

		It("approves a request onto an override with a caller-chosen name", func() {
			// The override's name is the caller's, not the one derived from the
			// dimension, and the approval must still land on it:
			override, err := limitsServer.Create(ctx, publicv1.QuotaLimitsCreateRequest_builder{
				Object: publicv1.QuotaLimit_builder{
					Metadata: publicv1.Metadata_builder{
						Name:   "chosen-override-name",
						Tenant: testTenant,
					}.Build(),
					Spec: publicv1.QuotaLimitSpec_builder{
						Dimension: "compute.vcpus",
						Value:     proto.Int64(10),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(override.GetObject().GetMetadata().GetName()).To(Equal("chosen-override-name"))

			request := createRequest("compute.vcpus", 20)
			_, err = requestsServer.Approve(ctx, publicv1.QuotaLimitRequestsApproveRequest_builder{
				Id: request.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			limits := listLimits("compute.vcpus")
			Expect(limits).To(HaveLen(1))
			Expect(limits[0].GetSpec().GetValue()).To(Equal(int64(20)))
			// The approval changed the value and left the name alone:
			Expect(limits[0].GetMetadata().GetName()).To(Equal("chosen-override-name"))
		})

		It("denies a request and changes no limit", func() {
			request := createRequest("compute.vcpus", 20)
			_, err := requestsServer.Deny(ctx, publicv1.QuotaLimitRequestsDenyRequest_builder{
				Id: request.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(listLimits("compute.vcpus")).To(BeEmpty())

			// A denied request doesn't block a new one:
			createRequest("compute.vcpus", 30)
		})
	})

	Describe("Quota usage", func() {
		BeforeEach(func() {
			_, err := limitsServer.SetDefault(ctx, publicv1.QuotaLimitsSetDefaultRequest_builder{
				Object: publicv1.QuotaDefaultLimit_builder{
					Spec: publicv1.QuotaDefaultLimitSpec_builder{
						Dimension: "compute.vcpus",
						Value:     proto.Int64(10),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			_, err = limitsServer.SetDefault(ctx, publicv1.QuotaLimitsSetDefaultRequest_builder{
				Object: publicv1.QuotaDefaultLimit_builder{
					Spec: publicv1.QuotaDefaultLimitSpec_builder{
						Dimension: "network.virtual_networks",
						Value:     proto.Int64(2),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			createLimit("", "compute.vcpus", 5)
			createLimit("team", "network.virtual_networks", 3)

			// One VM of a type with 6 vCPUs, and one NAT gateway in the team project:
			instanceTypesDao, err := dao.NewGenericDAO[*privatev1.InstanceType]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = instanceTypesDao.Create().
				SetObject(privatev1.InstanceType_builder{
					Metadata: privatev1.Metadata_builder{Name: "large", Tenant: "shared"}.Build(),
					Spec:     privatev1.InstanceTypeSpec_builder{Vcpus: 6, MemoryGib: 24}.Build(),
				}.Build()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			computeInstancesDao, err := dao.NewGenericDAO[*privatev1.ComputeInstance]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = computeInstancesDao.Create().
				SetObject(privatev1.ComputeInstance_builder{
					Metadata: privatev1.Metadata_builder{Name: "vm-1", Tenant: testTenant}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						InstanceType: privatev1.InstanceTypeReference_builder{Name: "large"}.Build(),
					}.Build(),
				}.Build()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			virtualNetworksDao, err := dao.NewGenericDAO[*privatev1.VirtualNetwork]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = virtualNetworksDao.Create().
				SetObject(privatev1.VirtualNetwork_builder{
					Metadata: privatev1.Metadata_builder{Name: "vn-1", Tenant: testTenant, Project: "team"}.Build(),
				}.Build()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
		})

		findDimension := func(
			dimensions []*publicv1.QuotaUsageDimension, name string,
		) *publicv1.QuotaUsageDimension {
			for _, dimension := range dimensions {
				if dimension.GetDimension() == name {
					return dimension
				}
			}
			return nil
		}

		It("resolves the limits, counts the usage, and marks the over-limit dimensions", func() {
			response, err := usageServer.Get(ctx, publicv1.QuotaUsageGetRequest_builder{
				Tenant: testTenant,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			report := response.GetReport()
			Expect(report.GetTenant()).To(Equal(testTenant))

			vcpus := findDimension(report.GetDimensions(), "compute.vcpus")
			Expect(vcpus).ToNot(BeNil())
			// The override replaces the default of 10:
			Expect(vcpus.GetLimit()).To(Equal(int64(5)))
			Expect(vcpus.GetUsage()).To(Equal(int64(6)))
			Expect(vcpus.GetRoom()).To(Equal(int64(-1)))
			Expect(vcpus.GetOverLimit()).To(BeTrue())

			gateways := findDimension(report.GetDimensions(), "network.virtual_networks")
			Expect(gateways).ToNot(BeNil())
			Expect(gateways.GetLimit()).To(Equal(int64(2)))
			Expect(gateways.GetUsage()).To(Equal(int64(1)))
			Expect(gateways.GetOverLimit()).To(BeFalse())

			Expect(report.GetProjects()).To(HaveLen(2))
			var team *publicv1.QuotaUsageProject
			for _, project := range report.GetProjects() {
				if project.GetProject() == "team" {
					team = project
				}
			}
			Expect(team).ToNot(BeNil())
			teamGateways := findDimension(team.GetDimensions(), "network.virtual_networks")
			Expect(teamGateways).ToNot(BeNil())
			// The project limit replaces the inherited tenant limit:
			Expect(teamGateways.GetLimit()).To(Equal(int64(3)))
			Expect(teamGateways.GetUsage()).To(Equal(int64(1)))
			Expect(teamGateways.GetRoom()).To(Equal(int64(2)))
		})

		It("lists a dimension that has a limit and no usage", func() {
			// The report covers every dimension with a default, an override or usage,
			// so a dimension with a limit and nothing counted still appears:
			_, err := limitsServer.SetDefault(ctx, publicv1.QuotaLimitsSetDefaultRequest_builder{
				Object: publicv1.QuotaDefaultLimit_builder{
					Spec: publicv1.QuotaDefaultLimitSpec_builder{
						Dimension: "images.count",
						Value:     proto.Int64(1000000),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			createLimit("team", "storage.fast.gib", 7)
			response, err := usageServer.Get(ctx, publicv1.QuotaUsageGetRequest_builder{
				Tenant: testTenant,
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// The tenant sees the default for a dimension nothing uses:
			images := findDimension(response.GetReport().GetDimensions(), "images.count")
			Expect(images).ToNot(BeNil())
			Expect(images.GetLimit()).To(Equal(int64(1000000)))
			Expect(images.GetUsage()).To(Equal(int64(0)))

			// The project sees its own limit for a dimension it does not use:
			var team *publicv1.QuotaUsageProject
			for _, project := range response.GetReport().GetProjects() {
				if project.GetProject() == "team" {
					team = project
				}
			}
			Expect(team).ToNot(BeNil())
			storage := findDimension(team.GetDimensions(), "storage.fast.gib")
			Expect(storage).ToNot(BeNil())
			Expect(storage.GetLimit()).To(Equal(int64(7)))
			Expect(storage.GetUsage()).To(Equal(int64(0)))
		})

		It("reads every limit of a tenant past the DAO's page", func() {
			// The DAO's default page is 100 rows, so a tenant with more limits must
			// still see them all: a dropped row would resolve its dimension against a
			// limit the report never read.
			tx, err := database.TxFromContext(ctx)
			Expect(err).ToNot(HaveOccurred())
			for i := 0; i < 105; i++ {
				dimension := fmt.Sprintf("test.paging.%03d", i)
				_, err = tx.Exec(ctx, `insert into quota_limits (id, name, tenant, project, data)
					values ($1, $1, $2, ''::ltree, jsonb_build_object(
						'spec', jsonb_build_object('dimension', $3::text, 'value', '7')))`,
					"paging-"+dimension, testTenant, dimension)
				Expect(err).ToNot(HaveOccurred())
			}

			response, err := usageServer.Get(ctx, publicv1.QuotaUsageGetRequest_builder{
				Tenant: testTenant,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			for _, dimension := range []string{"test.paging.000", "test.paging.100", "test.paging.104"} {
				entry := findDimension(response.GetReport().GetDimensions(), dimension)
				Expect(entry).ToNot(BeNil(), "the report dropped %s", dimension)
				Expect(entry.GetLimit()).To(Equal(int64(7)))
			}
		})

		It("resolves a default of zero, which the stored document omits", func() {
			// protojson leaves a zero value out of the stored document, so the report
			// must read the missing value as the zero it is instead of failing. The
			// dimension needs usage for the report to list it:
			diskImagesDao, err := dao.NewGenericDAO[*privatev1.DiskImage]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = diskImagesDao.Create().
				SetObject(privatev1.DiskImage_builder{
					Metadata: privatev1.Metadata_builder{Name: "img-1", Tenant: testTenant}.Build(),
				}.Build()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = limitsServer.SetDefault(ctx, publicv1.QuotaLimitsSetDefaultRequest_builder{
				Object: publicv1.QuotaDefaultLimit_builder{
					Spec: publicv1.QuotaDefaultLimitSpec_builder{
						Dimension: "images.count",
						Value:     proto.Int64(0),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			response, err := usageServer.Get(ctx, publicv1.QuotaUsageGetRequest_builder{
				Tenant: testTenant,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			images := findDimension(response.GetReport().GetDimensions(), "images.count")
			Expect(images).ToNot(BeNil())
			Expect(images.GetLimit()).To(Equal(int64(0)))
			Expect(images.GetUsage()).To(Equal(int64(1)))
			Expect(images.GetOverLimit()).To(BeTrue())
		})

		It("refuses a default without a value", func() {
			_, err := limitsServer.SetDefault(ctx, publicv1.QuotaLimitsSetDefaultRequest_builder{
				Object: publicv1.QuotaDefaultLimit_builder{
					Spec: publicv1.QuotaDefaultLimitSpec_builder{
						Dimension: "images.count",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		})

		It("refuses a limit without a value", func() {
			_, err := limitsServer.Create(ctx, publicv1.QuotaLimitsCreateRequest_builder{
				Object: publicv1.QuotaLimit_builder{
					Metadata: publicv1.Metadata_builder{Tenant: testTenant}.Build(),
					Spec:     publicv1.QuotaLimitSpec_builder{Dimension: "images.count"}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		})

		It("refuses a negative limit", func() {
			_, err := limitsServer.Create(ctx, publicv1.QuotaLimitsCreateRequest_builder{
				Object: publicv1.QuotaLimit_builder{
					Metadata: publicv1.Metadata_builder{Tenant: testTenant}.Build(),
					Spec:     publicv1.QuotaLimitSpec_builder{Dimension: "images.count", Value: proto.Int64(-1)}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		})

		It("refuses a negative requested value", func() {
			_, err := requestsServer.Create(ctx, publicv1.QuotaLimitRequestsCreateRequest_builder{
				Object: publicv1.QuotaLimitRequest_builder{
					Metadata: publicv1.Metadata_builder{Tenant: testTenant}.Build(),
					Spec: publicv1.QuotaLimitRequestSpec_builder{
						Dimension:      "compute.vcpus",
						RequestedValue: proto.Int64(-1),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		})

		It("refuses a negative default", func() {
			_, err := limitsServer.SetDefault(ctx, publicv1.QuotaLimitsSetDefaultRequest_builder{
				Object: publicv1.QuotaDefaultLimit_builder{
					Spec: publicv1.QuotaDefaultLimitSpec_builder{
						Dimension: "images.count",
						Value:     proto.Int64(-1),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		})

		It("refuses the built-in tenants and invisible tenants", func() {
			_, err := usageServer.Get(ctx, publicv1.QuotaUsageGetRequest_builder{
				Tenant: "shared",
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.NotFound))

			visibility, err := auth.NewVisibility().
				AddVisibleTenants(testTenant).
				SetTotalProjectVisibility(testTenant).
				Build()
			Expect(err).ToNot(HaveOccurred())
			restricted := auth.NewMockTenancyLogic(ctrl)
			restricted.EXPECT().DetermineVisibility(gomock.Any()).
				Return(visibility, nil).
				AnyTimes()
			server, err := NewQuotaUsageServer().
				SetLogger(logger).
				SetTenancyLogic(restricted).
				Build()
			Expect(err).ToNot(HaveOccurred())
			// An invisible tenant is reported as missing, the way a Get of another
			// tenant's row is:
			_, err = server.Get(ctx, publicv1.QuotaUsageGetRequest_builder{
				Tenant: "another",
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.NotFound))

			// A tenant that exists but holds no projects is missing the same way:
			_, err = usageServer.Get(ctx, publicv1.QuotaUsageGetRequest_builder{
				Tenant: "another",
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.NotFound))
		})

		It("shows a project-scoped caller only the projects they can see", func() {
			nestedDao, err := dao.NewGenericDAO[*privatev1.Project]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = nestedDao.Create().
				SetObject(privatev1.Project_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "other",
						Tenant: testTenant,
					}.Build(),
				}.Build()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			virtualNetworksDao, err := dao.NewGenericDAO[*privatev1.VirtualNetwork]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = virtualNetworksDao.Create().
				SetObject(privatev1.VirtualNetwork_builder{
					Metadata: privatev1.Metadata_builder{Name: "vn-2", Tenant: testTenant, Project: "other"}.Build(),
				}.Build()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			visibility, err := auth.NewVisibility().
				AddVisibleTenants(testTenant).
				AddVisibleProjects(testTenant, "team").
				Build()
			Expect(err).ToNot(HaveOccurred())
			restricted := auth.NewMockTenancyLogic(ctrl)
			restricted.EXPECT().DetermineVisibility(gomock.Any()).
				Return(visibility, nil).
				AnyTimes()
			server, err := NewQuotaUsageServer().
				SetLogger(logger).
				SetTenancyLogic(restricted).
				Build()
			Expect(err).ToNot(HaveOccurred())
			response, err := server.Get(ctx, publicv1.QuotaUsageGetRequest_builder{
				Tenant: testTenant,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			// The default project is always visible, and the granted project with its
			// subtree; the sibling project stays out of the breakdown, while the tenant's
			// own totals still cover it:
			var names []string
			for _, project := range response.GetReport().GetProjects() {
				names = append(names, project.GetProject())
			}
			Expect(names).To(ConsistOf("", "team"))
			gateways := findDimension(response.GetReport().GetDimensions(), "network.virtual_networks")
			Expect(gateways).ToNot(BeNil())
			Expect(gateways.GetUsage()).To(Equal(int64(2)))
		})

		It("resolves a project's limit through its nearest ancestor", func() {
			nestedDao, err := dao.NewGenericDAO[*privatev1.Project]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = nestedDao.Create().
				SetObject(privatev1.Project_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "team.web",
						Tenant: testTenant,
					}.Build(),
				}.Build()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			virtualNetworksDao, err := dao.NewGenericDAO[*privatev1.VirtualNetwork]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = virtualNetworksDao.Create().
				SetObject(privatev1.VirtualNetwork_builder{
					Metadata: privatev1.Metadata_builder{Name: "vn-2", Tenant: testTenant, Project: "team.web"}.Build(),
				}.Build()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			response, err := usageServer.Get(ctx, publicv1.QuotaUsageGetRequest_builder{
				Tenant: testTenant,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			var web *publicv1.QuotaUsageProject
			for _, project := range response.GetReport().GetProjects() {
				if project.GetProject() == "team.web" {
					web = project
				}
			}
			Expect(web).ToNot(BeNil())
			gateways := findDimension(web.GetDimensions(), "network.virtual_networks")
			Expect(gateways).ToNot(BeNil())
			// "team.web" has no limit of its own, so it is bound by "team"'s limit of 3,
			// not by the tenant default of 2:
			Expect(gateways.GetLimit()).To(Equal(int64(3)))
			Expect(gateways.GetUsage()).To(Equal(int64(1)))
		})

	})
})
