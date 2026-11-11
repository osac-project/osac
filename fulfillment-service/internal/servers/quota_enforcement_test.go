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
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	osactesting "github.com/osac-project/osac/fulfillment-service/internal/testing"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// quotaTierResolver resolves every tier to the same provider, so the volume tests need no
// storage backends.
var quotaTierResolver = TierResolverFunc(func(_ context.Context, _ string) (*TierResolution, error) {
	return &TierResolution{
		Provider: "test-provider",
		Protocol: privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
	}, nil
})

var _ = Describe("Quota enforcement", func() {
	var volumes *PrivateVolumesServer
	var instances *GenericServer[*privatev1.ComputeInstance]

	BeforeEach(func() {
		var err error
		volumes, err = NewPrivateVolumesServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			SetTierResolver(quotaTierResolver).
			Build()
		Expect(err).ToNot(HaveOccurred())

		instances, err = NewGenericServer[*privatev1.ComputeInstance]().
			SetLogger(logger).
			SetService(privatev1.ComputeInstances_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	// insertRow inserts one row of an object table with the standard columns.
	insertRow := func(table, id, tenant, project, data string) {
		tx, err := database.TxFromContext(ctx)
		Expect(err).ToNot(HaveOccurred())
		_, err = tx.Exec(ctx,
			`insert into `+table+` (id, name, tenant, project, creator, data)
			values ($1, $1, $2, $3::ltree, 'system', $4::jsonb)`,
			id, tenant, project, data,
		)
		Expect(err).ToNot(HaveOccurred())
	}

	deleteRow := func(table, id string) {
		tx, err := database.TxFromContext(ctx)
		Expect(err).ToNot(HaveOccurred())
		_, err = tx.Exec(ctx, `delete from `+table+` where id = $1`, id)
		Expect(err).ToNot(HaveOccurred())
	}

	insertLimit := func(project, dimension string, value int64) {
		insertRow("quota_limits", "limit-"+dimension+"-"+project, testTenant, project,
			`{"spec":{"dimension":"`+dimension+`","value":`+fmt.Sprint(value)+`}}`)
	}

	insertDefaultLimit := func(dimension string, value int64) {
		tx, err := database.TxFromContext(ctx)
		Expect(err).ToNot(HaveOccurred())
		_, err = tx.Exec(ctx,
			`insert into quota_default_limits (id, name, creator, data)
			values ($1, $1, 'system', $2::jsonb)`,
			"default-"+dimension, `{"spec":{"dimension":"`+dimension+`","value":`+fmt.Sprint(value)+`}}`,
		)
		Expect(err).ToNot(HaveOccurred())
	}

	insertThreshold := func(project, dimension string, value int64) {
		insertRow("quota_thresholds", "threshold-"+dimension+"-"+project, testTenant, project,
			`{"spec":{"dimension":"`+dimension+`","value":`+fmt.Sprint(value)+`}}`)
	}

	createProject := func(name string) {
		tx, err := database.TxFromContext(ctx)
		Expect(err).ToNot(HaveOccurred())
		_, err = tx.Exec(ctx,
			`insert into projects (id, tenant, project, name, creator, data)
			values (uuidv7(), $1, ''::ltree, $2::ltree, 'system', '{}')`, testTenant, name,
		)
		Expect(err).ToNot(HaveOccurred())
	}

	insertInstanceType := func(name string, vcpus, memoryGib int32) {
		insertRow("instance_types", name, auth.SharedTenant, "",
			`{"spec":{"vcpus":`+fmt.Sprint(vcpus)+`,"memory_gib":`+fmt.Sprint(memoryGib)+`}}`)
	}

	createVolume := func(name, project, tier string, sizeGib int64) error {
		_, err := volumes.Create(ctx, privatev1.VolumesCreateRequest_builder{
			Object: privatev1.Volume_builder{
				Metadata: privatev1.Metadata_builder{Name: name, Tenant: testTenant, Project: project}.Build(),
				Spec: privatev1.VolumeSpec_builder{
					StorageTier: tier,
					SizeGib:     sizeGib,
					AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
				}.Build(),
			}.Build(),
		}.Build())
		return err
	}

	createInstance := func(name, project, typeName string) error {
		var response *privatev1.ComputeInstancesCreateResponse
		return instances.Create(ctx, privatev1.ComputeInstancesCreateRequest_builder{
			Object: privatev1.ComputeInstance_builder{
				Metadata: privatev1.Metadata_builder{Name: name, Tenant: testTenant, Project: project}.Build(),
				Spec: privatev1.ComputeInstanceSpec_builder{
					InstanceType: &privatev1.InstanceTypeReference{Name: typeName},
				}.Build(),
			}.Build(),
		}.Build(), &response)
	}

	updateInstance := func(request *privatev1.ComputeInstancesUpdateRequest) error {
		var response *privatev1.ComputeInstancesUpdateResponse
		return instances.Update(ctx, request, &response)
	}

	countRows := func(table string) int {
		tx, err := database.TxFromContext(ctx)
		Expect(err).ToNot(HaveOccurred())
		var count int
		Expect(tx.QueryRow(ctx, `select count(*) from `+table).Scan(&count)).To(Succeed())
		return count
	}

	outboxRows := func() []map[string]any {
		tx, err := database.TxFromContext(ctx)
		Expect(err).ToNot(HaveOccurred())
		rows, err := tx.Query(ctx,
			`select tenant, project::text, dimension, data from quota_outbox order by dimension`)
		Expect(err).ToNot(HaveOccurred())
		defer rows.Close()
		var result []map[string]any
		for rows.Next() {
			var tenant, project, dimension string
			var data []byte
			Expect(rows.Scan(&tenant, &project, &dimension, &data)).To(Succeed())
			result = append(result, map[string]any{
				"tenant": tenant, "project": project, "dimension": dimension, "data": data,
			})
		}
		Expect(rows.Err()).To(Succeed())
		return result
	}

	// quotaFailure extracts the ErrorInfo reason and the QuotaFailure detail of a refusal.
	quotaFailure := func(err error) (reason string, failure *publicv1.QuotaFailure) {
		status := grpcstatus.Convert(err)
		ExpectWithOffset(1, status.Code()).To(Equal(grpccodes.FailedPrecondition))
		for _, detail := range status.Details() {
			switch detail := detail.(type) {
			case *errdetails.ErrorInfo:
				reason = detail.GetReason()
			case *publicv1.QuotaFailure:
				failure = detail
			}
		}
		return reason, failure
	}

	firstInstanceId := func() string {
		tx, err := database.TxFromContext(ctx)
		Expect(err).ToNot(HaveOccurred())
		var id string
		Expect(tx.QueryRow(ctx, `select id from compute_instances`).Scan(&id)).To(Succeed())
		return id
	}

	Describe("Creates over a limit", func() {
		BeforeEach(func() {
			insertLimit("", "storage.volumes", 100)
			insertLimit("", "storage.fast.gib", 10)
		})

		It("refuses a create over the tenant limit and writes nothing", func() {
			Expect(createVolume("vol-1", "", "fast", 5)).To(Succeed())

			err := createVolume("vol-2", "", "fast", 6)
			reason, failure := quotaFailure(err)
			Expect(reason).To(Equal("QUOTA_EXCEEDED"))
			Expect(failure.GetScope()).To(Equal("tenant"))
			Expect(failure.GetTenant()).To(Equal(testTenant))
			Expect(failure.GetDimension()).To(Equal("storage.fast.gib"))
			Expect(failure.GetLimit()).To(Equal(int64(10)))
			Expect(failure.GetUsage()).To(Equal(int64(5)))
			Expect(failure.GetAdded()).To(Equal(int64(6)))
			Expect(failure.GetSources()).To(ConsistOf("spec.size_gib"))
			Expect(countRows("volumes")).To(Equal(1))
		})

		It("refuses a create over a project limit and names the project", func() {
			createProject("team")
			insertLimit("team", "storage.fast.gib", 4)

			err := createVolume("vol-1", "team", "fast", 5)
			reason, failure := quotaFailure(err)
			Expect(reason).To(Equal("QUOTA_EXCEEDED"))
			Expect(failure.GetScope()).To(Equal("project"))
			Expect(failure.GetProject()).To(Equal("team"))
			Expect(failure.GetDimension()).To(Equal("storage.fast.gib"))
			Expect(failure.GetLimit()).To(Equal(int64(4)))
			Expect(failure.GetUsage()).To(Equal(int64(0)))
			Expect(failure.GetAdded()).To(Equal(int64(5)))
			Expect(countRows("volumes")).To(Equal(0))
		})

		It("refuses a create over an ancestor project's limit and names the ancestor", func() {
			createProject("team")
			createProject("team.web")
			insertLimit("team", "storage.fast.gib", 4)

			err := createVolume("vol-1", "team.web", "fast", 5)
			reason, failure := quotaFailure(err)
			Expect(reason).To(Equal("QUOTA_EXCEEDED"))
			Expect(failure.GetScope()).To(Equal("ancestor_project"))
			Expect(failure.GetProject()).To(Equal("team"))
			Expect(failure.GetDimension()).To(Equal("storage.fast.gib"))
			Expect(countRows("volumes")).To(Equal(0))
		})

		It("resolves the limit from the tenant override, the default, then 0", func() {
			insertDefaultLimit("storage.slow.gib", 3)
			Expect(createVolume("vol-1", "", "slow", 2)).To(Succeed())

			err := createVolume("vol-2", "", "slow", 2)
			_, failure := quotaFailure(err)
			Expect(failure.GetScope()).To(Equal("tenant"))
			Expect(failure.GetLimit()).To(Equal(int64(3)))

			// The tenant's override replaces the default for the dimension:
			insertLimit("", "storage.archive.gib", 10)
			insertDefaultLimit("storage.archive.gib", 3)
			Expect(createVolume("vol-3", "", "archive", 5)).To(Succeed())
			Expect(createVolume("vol-4", "", "archive", 5)).To(Succeed())
			err = createVolume("vol-5", "", "archive", 1)
			_, failure = quotaFailure(err)
			Expect(failure.GetLimit()).To(Equal(int64(10)))
		})

		It("runs the check on a dry run and writes nothing", func() {
			_, err := volumes.Create(dryRunCtx(), privatev1.VolumesCreateRequest_builder{
				Object: privatev1.Volume_builder{
					Metadata: privatev1.Metadata_builder{Name: "dry", Tenant: testTenant}.Build(),
					Spec: privatev1.VolumeSpec_builder{
						StorageTier: "fast",
						SizeGib:     11,
						AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
					}.Build(),
				}.Build(),
			}.Build())
			reason, _ := quotaFailure(err)
			Expect(reason).To(Equal("QUOTA_EXCEEDED"))
			Expect(countRows("volumes")).To(Equal(0))
		})

		It("fits at a limit of 0 on a dimension the request adds nothing to", func() {
			// The suite seeds a compute.vcpus default; drop it, so the dimension's limit is 0,
			// but a volume adds no vCPUs. No type-seeding trigger re-creates the row, because
			// the spec inserts no types:
			tx, err := database.TxFromContext(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = tx.Exec(ctx,
				`delete from quota_default_limits where data -> 'spec' ->> 'dimension' = 'compute.vcpus'`)
			Expect(err).ToNot(HaveOccurred())
			Expect(createVolume("vol-1", "", "fast", 5)).To(Succeed())
		})

		It("skips the check for the built-in shared and system tenants", func() {
			sharedServer, err := NewGenericServer[*privatev1.Volume]().
				SetLogger(logger).
				SetService(privatev1.Volumes_ServiceDesc.ServiceName).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				AddAllowedTenants(auth.SharedTenant, auth.SystemTenant).
				Build()
			Expect(err).ToNot(HaveOccurred())
			for _, tenant := range []string{auth.SharedTenant, auth.SystemTenant} {
				var response *privatev1.VolumesCreateResponse
				err = sharedServer.Create(ctx, privatev1.VolumesCreateRequest_builder{
					Object: privatev1.Volume_builder{
						Metadata: privatev1.Metadata_builder{Name: "builtin-" + tenant, Tenant: tenant}.Build(),
						Spec: privatev1.VolumeSpec_builder{
							StorageTier: "fast",
							SizeGib:     1000,
							AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
						}.Build(),
					}.Build(),
				}.Build(), &response)
				Expect(err).ToNot(HaveOccurred())
			}
		})
	})

	Describe("Updates over a limit", func() {
		updateInstanceType := func(id, typeName string) error {
			return updateInstance(privatev1.ComputeInstancesUpdateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Id:   id,
					Spec: privatev1.ComputeInstanceSpec_builder{InstanceType: &privatev1.InstanceTypeReference{Name: typeName}}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.instance_type"}},
			}.Build())
		}

		// statusClock gives each status write a later instance type transition time, the
		// way the operator feedback does; the database rejects one that doesn't move
		// forward. The hour of headroom clears the row's creation timestamp whatever the
		// database clock says.
		statusClock := time.Now().Add(time.Hour)
		updateInstanceStatusType := func(id, typeName string) error {
			statusClock = statusClock.Add(time.Second)
			return updateInstance(privatev1.ComputeInstancesUpdateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Id: id,
					Status: privatev1.ComputeInstanceStatus_builder{
						InstanceType:               &privatev1.InstanceTypeReference{Name: typeName},
						InstanceTypeTransitionTime: timestamppb.New(statusClock),
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{
					"status.instance_type", "status.instance_type_transition_time",
				}},
			}.Build())
		}

		It("refuses an update that adds over the limit and writes nothing", func() {
			insertLimit("", "compute.vcpus", 4)
			insertLimit("", "compute.memory_gib", 100)
			insertInstanceType("small", 2, 4)
			insertInstanceType("large", 6, 8)
			Expect(createInstance("vm-1", "", "small")).To(Succeed())

			id := firstInstanceId()
			err := updateInstanceType(id, "large")
			reason, failure := quotaFailure(err)
			Expect(reason).To(Equal("QUOTA_EXCEEDED"))
			Expect(failure.GetDimension()).To(Equal("compute.vcpus"))
			Expect(failure.GetLimit()).To(Equal(int64(4)))
			Expect(failure.GetUsage()).To(Equal(int64(2)))
			Expect(failure.GetAdded()).To(Equal(int64(4)))
			Expect(failure.GetSources()).To(ConsistOf("spec.instance_type"))
			var getResponse *privatev1.ComputeInstancesGetResponse
			Expect(instances.Get(ctx, privatev1.ComputeInstancesGetRequest_builder{Id: id}.Build(),
				&getResponse)).To(Succeed())
			Expect(getResponse.GetObject().GetSpec().GetInstanceType().GetName()).To(Equal("small"))
		})

		It("does not check an update whose mask names no annotated spec field", func() {
			insertLimit("", "compute.vcpus", 0)
			insertLimit("", "compute.memory_gib", 0)
			insertInstanceType("small", 2, 4)
			Expect(createInstance("vm-1", "", "small")).ToNot(Succeed())

			// The create was refused at limit 0, so insert the row directly and update its
			// status, which no annotation reads:
			insertRow("compute_instances", "vm-1", testTenant, "",
				`{"spec":{"instance_type":{"name":"small"}},"status":{}}`)
			Expect(updateInstanceStatusType("vm-1", "small")).To(Succeed())
			Expect(updateInstance(privatev1.ComputeInstancesUpdateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Id: "vm-1",
					Spec: privatev1.ComputeInstanceSpec_builder{
						RunStrategy: privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.run_strategy"}},
			}.Build())).To(Succeed())
		})

		It("checks an update with no field mask", func() {
			insertLimit("", "compute.vcpus", 4)
			insertLimit("", "compute.memory_gib", 100)
			insertInstanceType("small", 2, 4)
			insertInstanceType("large", 6, 8)
			Expect(createInstance("vm-1", "", "small")).To(Succeed())

			err := updateInstance(privatev1.ComputeInstancesUpdateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Id:       firstInstanceId(),
					Metadata: privatev1.Metadata_builder{Name: "vm-1", Tenant: testTenant}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						InstanceType: &privatev1.InstanceTypeReference{Name: "large"},
					}.Build(),
				}.Build(),
			}.Build())
			_, failure := quotaFailure(err)
			Expect(failure.GetDimension()).To(Equal("compute.vcpus"))
		})

		It("does not free quota on a resize down until the resource runs smaller", func() {
			insertLimit("", "compute.vcpus", 10)
			insertLimit("", "compute.memory_gib", 100)
			insertInstanceType("small", 2, 4)
			insertInstanceType("large", 6, 8)
			Expect(createInstance("vm-1", "", "large")).To(Succeed())
			id := firstInstanceId()

			// The resize down adds nothing, so it passes, but the instance still runs large:
			Expect(updateInstanceType(id, "small")).To(Succeed())
			Expect(updateInstanceStatusType(id, "large")).To(Succeed())

			// By the spec alone vm-1 counts 2 and the create would fit; the larger-of rule
			// keeps it at 6 while it runs large:
			err := createInstance("vm-2", "", "large")
			reason, failure := quotaFailure(err)
			Expect(reason).To(Equal("QUOTA_EXCEEDED"))
			Expect(failure.GetDimension()).To(Equal("compute.vcpus"))
			Expect(failure.GetUsage()).To(Equal(int64(6)))
			Expect(failure.GetAdded()).To(Equal(int64(6)))
			Expect(countRows("compute_instances")).To(Equal(1))

			// Once the instance runs small, the create fits:
			Expect(updateInstanceStatusType(id, "small")).To(Succeed())
			Expect(createInstance("vm-2", "", "large")).To(Succeed())
		})
	})

	Describe("Concurrent requests in one tenant", func() {
		// committed runs the function in its own transaction, committed, so concurrent
		// transactions can see what it wrote.
		committed := func(fn func(runCtx context.Context)) {
			tx, err := tm.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())
			fn(database.TxIntoContext(ctx, tx))
			Expect(tx.End(ctx)).To(Succeed())
		}

		BeforeEach(func() {
			committed(func(runCtx context.Context) {
				tx, err := database.TxFromContext(runCtx)
				Expect(err).ToNot(HaveOccurred())
				_, err = tx.Exec(runCtx,
					`insert into tenants (id, name, tenant, creator, data)
					values ('acme', 'acme', 'acme', 'system', '{}')`)
				Expect(err).ToNot(HaveOccurred())
				for _, limit := range []struct {
					id, dimension string
					value         int64
				}{
					{"limit-vcpus", "compute.vcpus", 1},
					{"limit-memory", "compute.memory_gib", 100},
					{"limit-caas", "caas.control_planes", 10},
				} {
					_, err = tx.Exec(runCtx,
						`insert into quota_limits (id, name, tenant, project, creator, data)
						values ($1, $1, 'acme', ''::ltree, 'system', $2)`,
						limit.id, `{"spec":{"dimension":"`+limit.dimension+`","value":`+fmt.Sprint(limit.value)+`}}`,
					)
					Expect(err).ToNot(HaveOccurred())
				}
				_, err = tx.Exec(runCtx,
					`insert into instance_types (id, name, tenant, project, creator, data)
					values ('small', 'small', 'shared', ''::ltree, 'system',
					'{"spec":{"vcpus":1,"memory_gib":2}}')`)
				Expect(err).ToNot(HaveOccurred())
			})
		})

		createInstanceInTx := func(runCtx context.Context, name string) error {
			var response *privatev1.ComputeInstancesCreateResponse
			return instances.Create(runCtx, privatev1.ComputeInstancesCreateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Metadata: privatev1.Metadata_builder{Name: name, Tenant: "acme"}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						InstanceType: &privatev1.InstanceTypeReference{Name: "small"},
					}.Build(),
				}.Build(),
			}.Build(), &response)
		}

		It("lets only one of two parallel creates for the last vCPU pass", func() {
			txOne, err := tm.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())
			txTwo, err := tm.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())

			// The first create holds the tenant lock until its transaction commits; the
			// second waits for the lock, then counts the first:
			Expect(createInstanceInTx(database.TxIntoContext(ctx, txOne), "vm-one")).To(Succeed())

			var twoErr error
			var done sync.WaitGroup
			done.Add(1)
			go func() {
				defer done.Done()
				twoErr = createInstanceInTx(database.TxIntoContext(ctx, txTwo), "vm-two")
			}()
			Expect(txOne.End(ctx)).To(Succeed())
			done.Wait()
			Expect(txTwo.End(ctx)).To(Succeed())

			reason, failure := quotaFailure(twoErr)
			Expect(reason).To(Equal("QUOTA_EXCEEDED"))
			Expect(failure.GetDimension()).To(Equal("compute.vcpus"))
			Expect(failure.GetUsage()).To(Equal(int64(1)))
			Expect(failure.GetAdded()).To(Equal(int64(1)))
		})

		It("finishes a cluster create and a VM create that lock the same subnet row", func() {
			clusters, err := NewGenericServer[*privatev1.Cluster]().
				SetLogger(logger).
				SetService(privatev1.Clusters_ServiceDesc.ServiceName).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			committed(func(runCtx context.Context) {
				tx, err := database.TxFromContext(runCtx)
				Expect(err).ToNot(HaveOccurred())
				_, err = tx.Exec(runCtx,
					`insert into subnets (id, name, tenant, project, creator, data)
					values ('subnet', 'subnet', 'acme', ''::ltree, 'system', '{}')`)
				Expect(err).ToNot(HaveOccurred())
			})

			// Both fill-ins lock the subnet's row after the tenant lock, the way the
			// networking creates do; neither request waits for the other's locks in the
			// opposite order:
			lockSubnet := func(runCtx context.Context) error {
				subnetsDao, err := dao.NewGenericDAO[*privatev1.Subnet]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				if err != nil {
					return err
				}
				_, err = subnetsDao.Get().SetId("subnet").SetLock(true).Do(runCtx)
				return err
			}

			txCluster, err := tm.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())
			txVm, err := tm.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())

			clusterDone := make(chan struct{})
			clusterEnded := make(chan struct{})
			var clusterErr error
			go func() {
				defer close(clusterDone)
				var response *privatev1.ClustersCreateResponse
				clusterErr = clusters.CreateWithCandidatePreparation(
					database.TxIntoContext(ctx, txCluster),
					privatev1.ClustersCreateRequest_builder{
						Object: privatev1.Cluster_builder{
							Metadata: privatev1.Metadata_builder{Name: "cluster", Tenant: "acme"}.Build(),
						}.Build(),
					}.Build(),
					&response,
					func(runCtx context.Context, _, candidate *privatev1.Cluster) error {
						return lockSubnet(runCtx)
					},
				)
			}()
			go func() {
				<-clusterDone
				if endErr := txCluster.End(ctx); endErr != nil {
					clusterErr = endErr
				}
				close(clusterEnded)
			}()

			var vmErr error
			var vmDone sync.WaitGroup
			vmDone.Add(1)
			go func() {
				defer vmDone.Done()
				var response *privatev1.ComputeInstancesCreateResponse
				vmErr = instances.CreateWithCandidatePreparation(
					database.TxIntoContext(ctx, txVm),
					privatev1.ComputeInstancesCreateRequest_builder{
						Object: privatev1.ComputeInstance_builder{
							Metadata: privatev1.Metadata_builder{Name: "vm-subnet", Tenant: "acme"}.Build(),
							Spec: privatev1.ComputeInstanceSpec_builder{
								InstanceType: &privatev1.InstanceTypeReference{Name: "small"},
							}.Build(),
						}.Build(),
					}.Build(),
					&response,
					func(runCtx context.Context, _, candidate *privatev1.ComputeInstance) error {
						return lockSubnet(runCtx)
					},
				)
			}()
			vmDone.Wait()
			Expect(txVm.End(ctx)).To(Succeed())
			<-clusterEnded

			Expect(clusterErr).ToNot(HaveOccurred())
			Expect(vmErr).ToNot(HaveOccurred())
		})
	})

	Describe("Warnings", func() {
		var thresholds *QuotaThresholdsServer
		var limits *QuotaLimitsServer

		BeforeEach(func() {
			var err error
			thresholds, err = NewQuotaThresholdsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			limits, err = NewQuotaLimitsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			createProject("team")
			insertLimit("", "storage.volumes", 100)
			insertLimit("", "storage.fast.gib", 10)
			insertLimit("team", "storage.fast.gib", 10)
		})

		It("writes one outbox row when a request crosses a threshold", func() {
			insertThreshold("team", "storage.fast.gib", 2)
			Expect(createVolume("vol-1", "team", "fast", 1)).To(Succeed())
			Expect(outboxRows()).To(BeEmpty())

			Expect(createVolume("vol-2", "team", "fast", 1)).To(Succeed())
			rows := outboxRows()
			Expect(rows).To(HaveLen(1))
			Expect(rows[0]["tenant"]).To(Equal(testTenant))
			Expect(rows[0]["project"]).To(Equal("team"))
			Expect(rows[0]["dimension"]).To(Equal("storage.fast.gib"))
			Expect(rows[0]["data"]).To(MatchJSON(`{"dimension":"storage.fast.gib","threshold":2,"usage":2,"limit":10}`))
		})

		It("writes no warning when the usage stays below the threshold", func() {
			insertThreshold("team", "storage.fast.gib", 5)
			Expect(createVolume("vol-1", "team", "fast", 1)).To(Succeed())
			Expect(createVolume("vol-2", "team", "fast", 1)).To(Succeed())
			Expect(outboxRows()).To(BeEmpty())
		})

		It("writes a warning when a threshold change puts the project at or above it", func() {
			Expect(createVolume("vol-1", "team", "fast", 3)).To(Succeed())
			_, err := thresholds.Create(ctx, publicv1.QuotaThresholdsCreateRequest_builder{
				Object: publicv1.QuotaThreshold_builder{
					Metadata: publicv1.Metadata_builder{Tenant: testTenant, Project: "team"}.Build(),
					Spec: publicv1.QuotaThresholdSpec_builder{
						Dimension: "storage.fast.gib",
						Value:     3,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			rows := outboxRows()
			Expect(rows).To(HaveLen(1))
			Expect(rows[0]["dimension"]).To(Equal("storage.fast.gib"))
		})

		createTeamLimit := func(value int64) *publicv1.QuotaLimit {
			response, createErr := limits.Create(ctx, publicv1.QuotaLimitsCreateRequest_builder{
				Object: publicv1.QuotaLimit_builder{
					Metadata: publicv1.Metadata_builder{Tenant: testTenant, Project: "team"}.Build(),
					Spec:     publicv1.QuotaLimitSpec_builder{Dimension: "storage.fast.gib", Value: value}.Build(),
				}.Build(),
			}.Build())
			Expect(createErr).ToNot(HaveOccurred())
			return response.GetObject()
		}

		updateTeamLimit := func(limit *publicv1.QuotaLimit, value int64) error {
			_, updateErr := limits.Update(ctx, publicv1.QuotaLimitsUpdateRequest_builder{
				Object: publicv1.QuotaLimit_builder{
					Id:       limit.GetId(),
					Metadata: limit.GetMetadata(),
					Spec:     publicv1.QuotaLimitSpec_builder{Dimension: "storage.fast.gib", Value: value}.Build(),
				}.Build(),
			}.Build())
			return updateErr
		}

		It("writes no warning when a limit change raises the limit", func() {
			// Replace the raw-seeded team limit with a server-created one, so the update
			// below passes name validation:
			deleteRow("quota_limits", "limit-storage.fast.gib-team")
			limit := createTeamLimit(10)
			insertThreshold("team", "storage.fast.gib", 2)
			Expect(createVolume("vol-1", "team", "fast", 3)).To(Succeed())
			Expect(outboxRows()).To(HaveLen(1))

			Expect(updateTeamLimit(limit, 20)).To(Succeed())
			Expect(outboxRows()).To(HaveLen(1))
		})

		It("writes a warning when a limit change lowers the limit", func() {
			deleteRow("quota_limits", "limit-storage.fast.gib-team")
			limit := createTeamLimit(10)
			insertThreshold("team", "storage.fast.gib", 2)
			Expect(createVolume("vol-1", "team", "fast", 3)).To(Succeed())
			Expect(outboxRows()).To(HaveLen(1))

			Expect(updateTeamLimit(limit, 5)).To(Succeed())
			rows := outboxRows()
			Expect(rows).To(HaveLen(2))
			payloads := []any{}
			for _, row := range rows {
				payloads = append(payloads, row["data"])
			}
			Expect(payloads).To(ContainElement(
				MatchJSON(`{"dimension":"storage.fast.gib","threshold":2,"usage":3,"limit":5}`)))
		})
	})

	Describe("Default limit changes", func() {
		It("warns, after the change commits, the tenants a lowered default tightens", func() {
			// The scan runs in its own transactions after the commit, so everything it
			// reads must be committed first:
			committed := func(fn func(runCtx context.Context)) {
				tx, err := tm.Begin(ctx)
				Expect(err).ToNot(HaveOccurred())
				fn(database.TxIntoContext(ctx, tx))
				Expect(tx.End(ctx)).To(Succeed())
			}
			committed(func(runCtx context.Context) {
				tx, err := database.TxFromContext(runCtx)
				Expect(err).ToNot(HaveOccurred())
				_, err = tx.Exec(runCtx,
					`insert into tenants (id, name, tenant, creator, data)
					values ('acme', 'acme', 'acme', 'system', '{}')`)
				Expect(err).ToNot(HaveOccurred())
				_, err = tx.Exec(runCtx,
					`insert into projects (id, tenant, project, name, creator, data)
					values (uuidv7(), 'acme', ''::ltree, 'team'::ltree, 'system', '{}')`)
				Expect(err).ToNot(HaveOccurred())
				_, err = tx.Exec(runCtx,
					`insert into quota_default_limits (id, name, creator, data)
					values ('default', 'default', 'system',
					'{"spec":{"dimension":"storage.slow.gib","value":10}}')`)
				Expect(err).ToNot(HaveOccurred())
				_, err = tx.Exec(runCtx,
					`insert into quota_limits (id, name, tenant, project, creator, data)
					values ('limit', 'limit', 'acme', 'team'::ltree, 'system',
					'{"spec":{"dimension":"storage.slow.gib","value":10}}')`)
				Expect(err).ToNot(HaveOccurred())
				_, err = tx.Exec(runCtx,
					`insert into quota_thresholds (id, name, tenant, project, creator, data)
					values ('threshold', 'threshold', 'acme', 'team'::ltree, 'system',
					'{"spec":{"dimension":"storage.slow.gib","value":2}}')`)
				Expect(err).ToNot(HaveOccurred())
				_, err = tx.Exec(runCtx,
					`insert into volumes (id, name, tenant, project, creator, data)
					values ('vol', 'vol', 'acme', 'team'::ltree, 'system',
					'{"spec":{"storage_tier":"slow","size_gib":3}}')`)
				Expect(err).ToNot(HaveOccurred())
			})

			limits, err := NewPrivateQuotaLimitsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			tx, err := tm.Begin(ctx)
			Expect(err).ToNot(HaveOccurred())
			managerCtx := database.TxManagerIntoContext(ctx, tm)
			setCtx := database.TxIntoContext(managerCtx, tx)
			DeferCleanup(func() {
				if tx != nil {
					Expect(tx.End(ctx)).To(Succeed())
					tx = nil
				}
			})
			_, err = limits.SetDefault(setCtx,
				privatev1.QuotaLimitsSetDefaultRequest_builder{
					Object: privatev1.QuotaDefaultLimit_builder{
						Spec: privatev1.QuotaDefaultLimitSpec_builder{
							Dimension: "storage.slow.gib",
							Value:     1,
						}.Build(),
					}.Build(),
				}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(tx.End(ctx)).To(Succeed())
			tx = nil

			// The scan runs after the commit, detached from the request, so its warnings
			// appear once it finishes:
			committedOutbox := func() []map[string]any {
				readTx, err := tm.Begin(ctx)
				Expect(err).ToNot(HaveOccurred())
				readCtx := database.TxIntoContext(ctx, readTx)
				rows, err := readTx.Query(readCtx,
					`select tenant, project::text, dimension, data from quota_outbox`)
				Expect(err).ToNot(HaveOccurred())
				defer rows.Close()
				var result []map[string]any
				for rows.Next() {
					var tenant, project, dimension string
					var data []byte
					Expect(rows.Scan(&tenant, &project, &dimension, &data)).To(Succeed())
					result = append(result, map[string]any{
						"tenant": tenant, "project": project, "dimension": dimension, "data": data,
					})
				}
				Expect(rows.Err()).To(Succeed())
				Expect(readTx.End(ctx)).To(Succeed())
				return result
			}
			var result []map[string]any
			Eventually(func(g Gomega) {
				result = committedOutbox()
				g.Expect(result).To(HaveLen(1))
			}).WithTimeout(10 * time.Second).Should(Succeed())
			Expect(result[0]["tenant"]).To(Equal("acme"))
			Expect(result[0]["project"]).To(Equal("team"))
		})
	})

	Describe("Metrics", func() {
		It("exposes the four quota metrics", func() {
			metricsServer := osactesting.NewMetricsServer()
			DeferCleanup(metricsServer.Close)

			metricVolumes, err := NewPrivateVolumesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				SetTierResolver(quotaTierResolver).
				SetMetricsRegisterer(metricsServer.Registry()).
				Build()
			Expect(err).ToNot(HaveOccurred())

			createProject("team")
			insertLimit("", "storage.volumes", 100)
			insertLimit("team", "storage.fast.gib", 10)
			insertThreshold("team", "storage.fast.gib", 2)

			metricVolume := func(name string, sizeGib int64) error {
				_, err := metricVolumes.Create(ctx, privatev1.VolumesCreateRequest_builder{
					Object: privatev1.Volume_builder{
						Metadata: privatev1.Metadata_builder{Name: name, Tenant: testTenant, Project: "team"}.Build(),
						Spec: privatev1.VolumeSpec_builder{
							StorageTier: "fast",
							SizeGib:     sizeGib,
							AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
						}.Build(),
					}.Build(),
				}.Build())
				return err
			}

			// One fitting create that crosses the threshold, one refused check:
			Expect(metricVolume("vol-1", 2)).To(Succeed())
			quotaFailure(metricVolume("vol-2", 20))

			metrics := metricsServer.Metrics()
			Expect(metrics).To(ContainElement(ContainSubstring("quota_refused_requests_total")))
			Expect(metrics).To(ContainElement(ContainSubstring("quota_check_duration_seconds")))
			Expect(metrics).To(ContainElement(ContainSubstring("quota_tenant_lock_wait_seconds")))
			Expect(metrics).To(ContainElement(ContainSubstring("quota_outbox_depth")))
		})
	})
})
