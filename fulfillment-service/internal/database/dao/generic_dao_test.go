/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package dao

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	testsv1 "github.com/osac-project/osac/proto/gen/osac/tests/v1"
)

var _ = Describe("Generic DAO", func() {
	const (
		defaultLimit = 5
		maxLimit     = 10
		objectCount  = maxLimit + 1
	)

	var (
		ctx         context.Context
		ctrl        *gomock.Controller
		tenancy     *auth.MockTenancyLogic
		tx          database.Tx
		tenantsDao  *GenericDAO[*privatev1.Tenant]
		projectsDao *GenericDAO[*privatev1.Project]
	)

	sort := func(objects []*testsv1.Object) {
		sort.Slice(objects, func(i, j int) bool {
			return strings.Compare(objects[i].GetId(), objects[j].GetId()) < 0
		})
	}

	// createTenant creates a tenant with the given name.
	createTenant := func(ctx context.Context, name string) {
		_, err := tenantsDao.Create().
			SetObject(privatev1.Tenant_builder{
				Id: name,
				Metadata: privatev1.Metadata_builder{
					Tenant: name,
					Name:   name,
				}.Build(),
			}.Build()).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
	}

	// createProject creates a project with the given tenant and name.
	createProject := func(ctx context.Context, tenant, name string) {
		_, err := projectsDao.Create().
			SetObject(privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Tenant: tenant,
					Name:   name,
				}.Build(),
			}.Build()).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
	}

	BeforeEach(func() {
		var err error

		// Create a context:
		ctx = context.Background()

		// Create the mock controller:
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)

		// Prepare the database pool:
		db, err := server.NewInstance().Build()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(db.Close)
		pool, err := db.Pool(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(pool.Close)

		// Create the transaction manager:
		tm, err := database.NewTxManager().
			SetLogger(logger).
			SetPool(pool).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Start a transaction and add it to the context:
		tx, err = tm.Begin(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			err := tx.End(ctx)
			Expect(err).ToNot(HaveOccurred())
		})
		ctx = database.TxIntoContext(ctx, tx)

		// Create a tenancy logic without restrictions:
		tenancy = auth.NewMockTenancyLogic(ctrl)
		tenancy.EXPECT().DetermineVisibility(gomock.Any()).
			Return(auth.TotalVisibility(), nil).
			AnyTimes()

		// Create the DAOs for tenants and projects:
		tenantsDao, err = NewGenericDAO[*privatev1.Tenant]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		projectsDao, err = NewGenericDAO[*privatev1.Project]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Create the tenant used in the tests:
		createTenant(ctx, "my-tenant")

		// Create the projects used in the tests:
		createProject(ctx, "my-tenant", "my-project")
	})

	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			generic, err := NewGenericDAO[*testsv1.Object]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(generic).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			generic, err := NewGenericDAO[*testsv1.Object]().
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(generic).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			generic, err := NewGenericDAO[*testsv1.Object]().
				SetLogger(logger).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(generic).To(BeNil())
		})

		It("Fails if default limit is zero", func() {
			generic, err := NewGenericDAO[*testsv1.Object]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				SetDefaultLimit(0).
				Build()
			Expect(err).To(MatchError("default limit must be a possitive integer, but it is 0"))
			Expect(generic).To(BeNil())
		})

		It("Fails if default limit is negative", func() {
			generic, err := NewGenericDAO[*testsv1.Object]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				SetDefaultLimit(-1).
				Build()
			Expect(err).To(MatchError("default limit must be a possitive integer, but it is -1"))
			Expect(generic).To(BeNil())
		})

		It("Fails if max limit is zero", func() {
			generic, err := NewGenericDAO[*testsv1.Object]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				SetMaxLimit(0).
				Build()
			Expect(err).To(MatchError("max limit must be a possitive integer, but it is 0"))
			Expect(generic).To(BeNil())
		})

		It("Fails if max limit is negative", func() {
			generic, err := NewGenericDAO[*testsv1.Object]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				SetMaxLimit(-1).
				Build()
			Expect(err).To(MatchError("max limit must be a possitive integer, but it is -1"))
			Expect(generic).To(BeNil())
		})

		It("Fails if max limit is less than default limit", func() {
			generic, err := NewGenericDAO[*testsv1.Object]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				SetMaxLimit(100).
				SetDefaultLimit(1000).
				Build()
			Expect(err).To(MatchError(
				"max limit must be greater or equal to default limit, but max limit is 100 and " +
					"default limit is 1000",
			))
			Expect(generic).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		var generic *GenericDAO[*testsv1.Object]

		BeforeEach(func() {
			// Create the DAO:
			var err error
			generic, err = NewGenericDAO[*testsv1.Object]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				SetDefaultLimit(defaultLimit).
				SetMaxLimit(maxLimit).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		Describe("Operation logging", func() {
			var buffer bytes.Buffer

			expectOperations := func(types []string, counts []int) {
				decoder := json.NewDecoder(&buffer)
				for i, op := range types {
					var record map[string]any
					Expect(decoder.Decode(&record)).To(Succeed())
					Expect(record["msg"]).To(Equal("Running SQL operation"))
					Expect(record["level"]).To(Equal("DEBUG"))
					Expect(record["type"]).To(Equal(op))
					Expect(record["parameter_count"]).To(Equal(float64(counts[i])))
					Expect(record).ToNot(HaveKey("sql"))
					Expect(record).To(HaveKeyWithValue("table", "objects"))
					Expect(record).To(HaveLen(6))
					Expect(record).To(HaveKey("time"))
				}
				Expect(decoder.More()).To(BeFalse())
				buffer.Reset()
			}

			BeforeEach(func() {
				buffer.Reset()
			})

			DescribeTable("Omits inline literals and bound values without changing PostgreSQL results",
				func(value string, redact bool) {
					captured, err := logging.NewLogger().SetWriter(&buffer).
						SetLevel(slog.LevelDebug.String()).SetRedact(redact).Build()
					Expect(err).ToNot(HaveOccurred())
					generic.logger = captured
					for _, id := range []string{"native-id-alpha", "native-id-beta", "native-id-decoy"} {
						payload := value
						if id == "native-id-decoy" {
							payload = "nonmatching-payload"
						}
						_, err = generic.Create().SetObject(testsv1.Object_builder{
							Id: id, MyString: payload,
							Metadata: testsv1.Metadata_builder{Tenant: "my-tenant", Name: id}.Build(),
						}.Build()).Do(ctx)
						Expect(err).ToNot(HaveOccurred())
					}

					visibility, err := auth.NewVisibility().AddVisibleTenant("my-tenant").Build()
					Expect(err).ToNot(HaveOccurred())
					filter := fmt.Sprintf("this.my_string == %q", value)
					count := generic.List().SetFilter(filter).SetLimit(0)
					count.visibility = visibility
					countResponse, err := count.Do(ctx)
					Expect(err).ToNot(HaveOccurred())
					Expect(countResponse.GetTotal()).To(Equal(int32(2)))
					Expect(countResponse.GetItems()).To(BeEmpty())

					page := generic.List().SetFilter(filter).SetOrder("id desc").SetOffset(1).SetLimit(1)
					page.visibility = visibility
					pageResponse, err := page.Do(ctx)
					Expect(err).ToNot(HaveOccurred())
					Expect(pageResponse.GetTotal()).To(Equal(int32(2)))
					Expect(pageResponse.GetItems()).To(HaveLen(1))
					Expect(pageResponse.GetItems()[0].GetId()).To(Equal("native-id-alpha"))
					Expect(pageResponse.GetItems()[0].GetMyString()).To(Equal(value))
					Expect(buffer.String()).ToNot(ContainSubstring(value[:8]))
					Expect(buffer.String()).ToNot(ContainSubstring("native-id-"))
					Expect(buffer.String()).ToNot(ContainSubstring("my-tenant"))
					expectOperations(
						[]string{"get", "create", "get", "create", "get", "create", "count", "count", "list"},
						[]int{1, 9, 1, 9, 1, 9, 1, 1, 3},
					)

					getResponse, err := generic.Get().SetId("native-id-alpha").Do(ctx)
					Expect(err).ToNot(HaveOccurred())
					Expect(getResponse.GetObject().GetMyString()).To(Equal(value))
					expectOperations([]string{"get"}, []int{1})
					getResponse.GetObject().SetMyString(value + "-updated")
					updateResponse, err := generic.Update().SetObject(getResponse.GetObject()).Do(ctx)
					Expect(err).ToNot(HaveOccurred())
					Expect(updateResponse.GetObject().GetMyString()).To(Equal(value + "-updated"))
					expectOperations([]string{"update"}, []int{8})
					stored, err := generic.Get().SetId("native-id-alpha").Do(ctx)
					Expect(err).ToNot(HaveOccurred())
					Expect(stored.GetObject().GetMyString()).To(Equal(value + "-updated"))
					expectOperations([]string{"get"}, []int{1})

					_, err = generic.Delete().SetId("native-id-alpha").Do(ctx)
					Expect(err).ToNot(HaveOccurred())
					expectOperations([]string{"update", "archive", "delete"}, []int{1, 11, 1})
					var archived []byte
					Expect(tx.QueryRow(ctx, `select data from archived_objects where id = $1`,
						"native-id-alpha").Scan(&archived)).To(Succeed())
					var data map[string]any
					Expect(json.Unmarshal(archived, &data)).To(Succeed())
					Expect(data["my_string"]).To(Equal(value + "-updated"))
				},
				Entry("raw native value with redaction disabled", "fixture-native-sensitive-prefix-not-a-uuid", false),
				Entry("UUID with redaction disabled", "f85c7d3a-0374-4b9e-9e2d-92c615bbec13", false),
				Entry("quoted SQL-shaped value with redaction disabled", "fixture-quoted-' OR true; -- \" \\ tail", false),
				Entry("control characters with redaction disabled", "fixture-control-\n\t\rvalue", false),
				Entry("UTF-8 with redaction disabled", "fixture-utf8-café-秘密-🔒", false),
				Entry("default redaction enabled", "fixture-default-sensitive-prefix", true),
			)

			It("Keeps safe operation metadata on errors and rejects malformed filters before SQL", func() {
				captured, err := logging.NewLogger().SetWriter(&buffer).
					SetLevel(slog.LevelDebug.String()).SetRedact(false).Build()
				Expect(err).ToNot(HaveOccurred())
				generic.logger = captured
				_, err = generic.Get().SetId("fixture-missing-native-value").Do(ctx)
				var missing *ErrNotFound
				Expect(errors.As(err, &missing)).To(BeTrue())
				size := buffer.Len()
				_, err = generic.List().SetFilter(`this.my_string == "fixture-malformed-sensitive" &&`).Do(ctx)
				var invalid *ErrInvalidFilter
				Expect(errors.As(err, &invalid)).To(BeTrue())
				Expect(buffer.Len()).To(Equal(size))
				expectOperations([]string{"get"}, []int{1})
			})

			It("Does not log bound payloads when PostgreSQL rejects an update", func() {
				for _, id := range []string{"first", "second"} {
					_, err := generic.Create().SetObject(testsv1.Object_builder{
						Id: id, MyString: "fixture-unique-sensitive-" + id,
						Metadata: testsv1.Metadata_builder{Tenant: "my-tenant", Name: id}.Build(),
					}.Build()).Do(ctx)
					Expect(err).ToNot(HaveOccurred())
				}
				_, err := tx.Exec(ctx, `create unique index objects_logging_payload on objects ((data->>'my_string'))`)
				Expect(err).ToNot(HaveOccurred())
				current, err := generic.Get().SetId("second").Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				captured, err := logging.NewLogger().SetWriter(&buffer).
					SetLevel(slog.LevelDebug.String()).SetRedact(false).Build()
				Expect(err).ToNot(HaveOccurred())
				generic.logger = captured
				current.GetObject().SetMyString("fixture-unique-sensitive-first")
				err = tx.Savepoint(ctx, func(spCtx context.Context) error {
					_, err := generic.Update().SetObject(current.GetObject()).Do(spCtx)
					return err
				})
				var exists *ErrAlreadyExists
				Expect(errors.As(err, &exists)).To(BeTrue())
				Expect(exists.ConstraintName).To(Equal("objects_logging_payload"))
				stored, err := generic.Get().SetId("second").Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(stored.GetObject().GetMyString()).To(Equal("fixture-unique-sensitive-second"))
				Expect(buffer.String()).ToNot(ContainSubstring("fixture-unique-sensitive-"))
				expectOperations([]string{"update", "get"}, []int{8, 1})
			})
		})

		Describe("Shared query contract", func() {
			seed := func(id string, creation, deletion time.Time, data string) {
				_, err := tx.Exec(ctx, `insert into objects
					(id, name, tenant, creation_timestamp, deletion_timestamp, data)
					values ($1, $1, 'my-tenant', $2, $3, $4)`, id, creation, deletion, data)
				Expect(err).ToNot(HaveOccurred())
			}
			ids := func(response *ListResponse[*testsv1.Object]) []string {
				result := []string{}
				for _, item := range response.GetItems() {
					result = append(result, item.GetId())
				}
				return result
			}

			It("Materializes only exact metadata epoch as absent", func() {
				epoch := time.Unix(0, 0).UTC()
				seed("absent", epoch, epoch, `{}`)
				seed("present", epoch.Add(time.Microsecond), epoch.Add(time.Microsecond), `{}`)
				for _, id := range []string{"absent", "present"} {
					response, err := generic.Get().SetId(id).Do(ctx)
					Expect(err).ToNot(HaveOccurred())
					md := response.GetObject().GetMetadata()
					if id == "absent" {
						Expect(md.HasCreationTimestamp()).To(BeFalse())
						Expect(md.HasDeletionTimestamp()).To(BeFalse())
					} else {
						Expect(md.HasCreationTimestamp()).To(BeTrue())
						Expect(md.HasDeletionTimestamp()).To(BeTrue())
						Expect(md.GetCreationTimestamp().AsTime()).To(Equal(epoch.Add(time.Microsecond)))
						Expect(md.GetDeletionTimestamp().AsTime()).To(Equal(epoch.Add(time.Microsecond)))
					}
				}
			})

			It("Filters metadata presence, nulls, ranges and version zero through PostgreSQL", func() {
				epoch := time.Unix(0, 0).UTC()
				seed("absent", epoch, epoch, `{}`)
				seed("present", epoch.Add(time.Microsecond), epoch.Add(time.Microsecond), `{}`)
				cases := []struct {
					filter string
					want   []string
				}{
					{`has(this.metadata.creation_timestamp)`, []string{"present"}},
					{`!has(this.metadata.creation_timestamp)`, []string{"absent"}},
					{`this.metadata.creation_timestamp == null`, []string{"absent"}},
					{`null == this.metadata.creation_timestamp`, []string{"absent"}},
					{`null != this.metadata.creation_timestamp`, []string{"present"}},
					{`this.metadata.creation_timestamp < now`, []string{"present"}},
					{`has(this.metadata.deletion_timestamp)`, []string{"present"}},
					{`this.metadata.deletion_timestamp == null`, []string{"absent"}},
					{`null != this.metadata.deletion_timestamp`, []string{"present"}},
					{`this.metadata.version == 0`, []string{"absent", "present"}},
					{`has(this.metadata.version)`, []string{}},
					{`this.metadata.version in [0, 2]`, []string{"absent", "present"}},
				}
				for _, tc := range cases {
					response, err := generic.List().SetFilter(tc.filter).Do(ctx)
					Expect(err).ToNot(HaveOccurred(), tc.filter)
					Expect(ids(response)).To(Equal(tc.want), tc.filter)
					Expect(response.GetTotal()).To(Equal(int32(len(tc.want))), tc.filter)
				}
				current, err := generic.Get().SetId("absent").Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				updated, err := generic.Update().SetObject(current.GetObject()).Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(updated.GetObject().GetMetadata().GetVersion()).To(Equal(int32(1)))
				for _, filter := range []string{`has(this.metadata.version)`, `this.metadata.version > 0`, `this.metadata.version in [1, 2]`} {
					response, err := generic.List().SetFilter(filter).Do(ctx)
					Expect(err).ToNot(HaveOccurred())
					Expect(ids(response)).To(Equal([]string{"absent"}))
				}
			})

			It("Archives after finalizer removal at epoch plus one microsecond but not exact epoch", func() {
				epoch := time.Unix(0, 0).UTC()
				for _, id := range []string{"active", "deleting"} {
					deletion := epoch
					if id == "deleting" {
						deletion = deletion.Add(time.Microsecond)
					}
					seed(id, epoch, deletion, `{}`)
					_, err := tx.Exec(ctx, `update objects set finalizers = array['controller'] where id = $1`, id)
					Expect(err).ToNot(HaveOccurred())
					current, err := generic.Get().SetId(id).Do(ctx)
					Expect(err).ToNot(HaveOccurred())
					current.GetObject().GetMetadata().SetFinalizers(nil)
					_, err = generic.Update().SetObject(current.GetObject()).Do(ctx)
					Expect(err).ToNot(HaveOccurred())
				}
				var live, archived []string
				Expect(tx.QueryRow(ctx, `select array_agg(id order by id) from objects`).Scan(&live)).To(Succeed())
				Expect(tx.QueryRow(ctx, `select array_agg(id order by id) from archived_objects`).Scan(&archived)).To(Succeed())
				Expect(live).To(Equal([]string{"active"}))
				Expect(archived).To(Equal([]string{"deleting"}))
				var deletion time.Time
				Expect(tx.QueryRow(ctx, `select deletion_timestamp from archived_objects where id = 'deleting'`).Scan(&deletion)).To(Succeed())
				Expect(deletion.Equal(epoch.Add(time.Microsecond))).To(BeTrue())
			})

			It("Preserves actual unique constraint names on Update errors", func() {
				for _, constraint := range []string{"objects_unique_name", "objects_native_key", "objects_token_key"} {
					req := generic.Update()
					err := req.translateError(ctx, "id", "name", "my-tenant", fmt.Errorf("wrapped: %w", &pgconn.PgError{
						Code: pgerrcode.UniqueViolation, ConstraintName: constraint, Message: "not a constraint name",
					}))
					var exists *ErrAlreadyExists
					Expect(errors.As(err, &exists)).To(BeTrue())
					Expect(exists.ConstraintName).To(Equal(constraint))
				}
			})

			It("Returns the real PostgreSQL Update constraint and preserves savepoint recovery", func() {
				epoch := time.Unix(0, 0).UTC()
				seed("first", epoch, epoch, `{"my_string":"first"}`)
				seed("second", epoch, epoch, `{"my_string":"second"}`)
				_, err := tx.Exec(ctx, `create unique index objects_query_unique_payload on objects ((data->>'my_string'))`)
				Expect(err).ToNot(HaveOccurred())
				current, err := generic.Get().SetId("second").Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				current.GetObject().SetMyString("first")
				err = tx.Savepoint(ctx, func(spCtx context.Context) error {
					_, err := generic.Update().SetObject(current.GetObject()).Do(spCtx)
					return err
				})
				var exists *ErrAlreadyExists
				Expect(errors.As(err, &exists)).To(BeTrue())
				Expect(exists.ConstraintName).To(Equal("objects_query_unique_payload"))
				stored, err := generic.Get().SetId("second").Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(stored.GetObject().GetMyString()).To(Equal("second"))
				Expect(stored.GetObject().GetMetadata().GetVersion()).To(BeZero())
				seed("after-recovery", epoch, epoch, `{"my_string":"third"}`)
			})

			It("Retains CEL type checks for numeric version versus null", func() {
				epoch := time.Unix(0, 0).UTC()
				seed("zero", epoch, epoch, `{}`)
				for _, filter := range []string{
					`this.metadata.version == null`, `null == this.metadata.version`,
					`this.metadata.version != null`, `null != this.metadata.version`,
				} {
					// CEL versions may reject scalar/null equality. Do not weaken typing to make it queryable.
					_, issues := generic.filterTranslator.celEnv.Compile(filter)
					if issues != nil && issues.Err() != nil {
						_, err := generic.filterTranslator.Translate(ctx, filter)
						Expect(err).To(HaveOccurred())
						continue
					}
					response, err := generic.List().SetFilter(filter).Do(ctx)
					Expect(err).ToNot(HaveOccurred())
					if strings.Contains(filter, "!=") {
						Expect(ids(response)).To(Equal([]string{"zero"}))
					} else {
						Expect(response.GetTotal()).To(BeZero())
					}
				}
			})

			It("Orders metadata epoch absence last without suppressing subsecond instants", func() {
				epoch := time.Unix(0, 0).UTC()
				seed("absent", epoch, epoch, `{}`)
				seed("early", epoch.Add(time.Microsecond), epoch.Add(time.Microsecond), `{}`)
				seed("later", epoch.Add(2*time.Microsecond), epoch.Add(2*time.Microsecond), `{}`)
				for _, field := range []string{"creation_timestamp", "deletion_timestamp"} {
					for _, tc := range []struct {
						direction string
						want      []string
					}{
						{"asc", []string{"early", "later", "absent"}},
						{"desc", []string{"later", "early", "absent"}},
					} {
						response, err := generic.List().SetOrder("metadata." + field + " " + tc.direction).Do(ctx)
						Expect(err).ToNot(HaveOccurred())
						Expect(ids(response)).To(Equal(tc.want))
					}
				}
			})

			It("Intersects trusted tenant and project visibility before ordered counts and pages", func() {
				createTenant(ctx, "other-tenant")
				for _, object := range []struct{ id, tenant, project string }{
					{"a", "my-tenant", ""}, {"b", "my-tenant", ""},
					{"c", "my-tenant", "my-project"}, {"d", "other-tenant", ""},
				} {
					_, err := generic.Create().SetObject(testsv1.Object_builder{
						Id: object.id,
						Metadata: testsv1.Metadata_builder{
							Name: object.id, Tenant: object.tenant, Project: object.project,
						}.Build(),
					}.Build()).Do(ctx)
					Expect(err).ToNot(HaveOccurred())
				}
				visibility, err := auth.NewVisibility().AddVisibleTenant("my-tenant").Build()
				Expect(err).ToNot(HaveOccurred())
				req := generic.List().SetOrder("id desc").SetOffset(1).SetLimit(1).
					SetFilter(`this.metadata.tenant == 'other-tenant' || this.metadata.tenant == 'my-tenant'`)
				req.visibility = visibility
				response, err := req.Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetTotal()).To(Equal(int32(2)))
				Expect(ids(response)).To(Equal([]string{"a"}))
				req = generic.List().SetOrder("id desc").SetLimit(0)
				req.visibility = visibility
				response, err = req.Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetTotal()).To(Equal(int32(2)))
				Expect(response.GetItems()).To(BeEmpty())
				req = generic.List().SetOrder("id desc")
				req.visibility = auth.ZeroVisibility()
				response, err = req.Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetTotal()).To(BeZero())
				Expect(response.GetItems()).To(BeEmpty())
			})

			It("Rejects invalid order and private queries through the DAO even for count-only requests", func() {
				publicDAO, err := NewGenericDAO[*testsv1.Object]().SetLogger(logger).SetTenancyLogic(tenancy).
					SetFilterDesc((*publicv1.Cluster)(nil).ProtoReflect().Descriptor()).Build()
				Expect(err).ToNot(HaveOccurred())
				for _, order := range []string{
					"metadata.finalizers", "status.hub", "metadata.labels", "metadata.creation_timestamp.seconds",
					"id desc; select pg_sleep(60)", "metadata.annotations['admin.osac.openshift.io/key']",
				} {
					err = tx.Savepoint(ctx, func(spCtx context.Context) error {
						_, err := publicDAO.List().SetOrder(order).SetLimit(0).Do(spCtx)
						return err
					})
					var invalid *ErrValidation
					Expect(errors.As(err, &invalid)).To(BeTrue(), order)
				}
				for _, filter := range []string{
					`has(this.metadata.finalizers)`, `this.metadata.finalizers == this.metadata.finalizers`,
				} {
					for _, d := range []*GenericDAO[*testsv1.Object]{generic, publicDAO} {
						err = tx.Savepoint(ctx, func(spCtx context.Context) error {
							_, err := d.List().SetFilter(filter).SetLimit(0).Do(spCtx)
							return err
						})
						var invalid *ErrInvalidFilter
						Expect(errors.As(err, &invalid)).To(BeTrue(), filter)
					}
				}
				response, err := generic.List().Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetTotal()).To(BeZero())
			})

			It("Orders Timestamp instants, NULLs and id ties before offset and limit in PostgreSQL", func() {
				epoch := time.Unix(0, 0).UTC()
				for id, data := range map[string]string{
					"a": `{"status":{"status_timestamp":"2026-01-01T01:00:00.000001+01:00"}}`,
					"b": `{"status":{"status_timestamp":"2026-01-01T00:00:00.000001Z"}}`,
					"c": `{"status":{"status_timestamp":"2025-12-31T23:00:00.000002-01:00"}}`,
					"d": `{}`,
					"e": `{"status":{"status_timestamp":null}}`,
					"f": `{"status":{"status_timestamp":"1970-01-01T00:00:00Z"}}`,
				} {
					seed(id, epoch, epoch, data)
				}
				for _, tc := range []struct {
					order string
					want  []string
				}{
					{"status.status_timestamp asc", []string{"f", "a", "b", "c", "d", "e"}},
					{"status.status_timestamp desc", []string{"c", "a", "b", "f", "d", "e"}},
				} {
					response, err := generic.List().SetOrder(tc.order).SetLimit(10).Do(ctx)
					Expect(err).ToNot(HaveOccurred())
					Expect(ids(response)).To(Equal(tc.want))
					Expect(response.GetTotal()).To(Equal(int32(6)))
					response, err = generic.List().SetOrder(tc.order).SetOffset(1).SetLimit(2).Do(ctx)
					Expect(err).ToNot(HaveOccurred())
					Expect(ids(response)).To(Equal(tc.want[1:3]))
					Expect(response.GetTotal()).To(Equal(int32(6)))
				}
				response, err := generic.List().SetLimit(10).Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(ids(response)).To(Equal([]string{"a", "b", "c", "d", "e", "f"}))
			})
		})

		It("Creates object", func() {
			object := &testsv1.Object{
				Metadata: &testsv1.Metadata{
					Tenant: "my-tenant",
				},
			}
			createResponse, err := generic.Create().
				SetObject(object).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			created := createResponse.GetObject()
			getResponse, err := generic.Get().
				SetId(created.GetId()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			result := getResponse.GetObject()
			Expect(result).ToNot(BeNil())
		})

		It("Creates empty object if no object is provided", func() {
			createResponse, err := generic.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "my-object",
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			created := createResponse.GetObject()
			Expect(created).ToNot(BeNil())
			Expect(created.GetId()).ToNot(BeEmpty())
			getResponse, err := generic.Get().
				SetId(created.GetId()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			result := getResponse.GetObject()
			Expect(result).ToNot(BeNil())
			Expect(result.GetId()).To(Equal(created.GetId()))
		})

		It("Sets metadata when creating", func() {
			object := &testsv1.Object{
				Metadata: &testsv1.Metadata{
					Tenant: "my-tenant",
				},
			}
			response, err := generic.Create().
				SetObject(object).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			result := response.GetObject()
			Expect(result.Metadata).ToNot(BeNil())
		})

		It("Sets creator when creating", func() {
			// Create the object and verify that the result has the creator set:
			object := &testsv1.Object{
				Metadata: &testsv1.Metadata{
					Creator: "my-user",
					Tenant:  "my-tenant",
				},
			}
			response, err := generic.Create().
				SetObject(object).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object = response.GetObject()
			Expect(object.GetMetadata().GetCreator()).To(Equal("my-user"))

			// Get the object and verify that the result has the creator set:
			getResponse, err := generic.Get().
				SetId(object.GetId()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object = getResponse.GetObject()
			Expect(object.GetMetadata().GetCreator()).To(Equal("my-user"))
		})

		It("Sets project when creating", func() {
			// Create the object with a project and verify that the result has the project set:
			object := &testsv1.Object{
				Metadata: &testsv1.Metadata{
					Tenant:  "my-tenant",
					Project: "my-project",
				},
			}
			response, err := generic.Create().
				SetObject(object).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object = response.GetObject()
			Expect(object.GetMetadata().GetProject()).To(Equal("my-project"))

			// Get the object and verify that the result has the project set:
			getResponse, err := generic.Get().
				SetId(object.GetId()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object = getResponse.GetObject()
			Expect(object.GetMetadata().GetProject()).To(Equal("my-project"))
		})

		It("Sets creation timestamp when creating", func() {
			object := &testsv1.Object{
				Metadata: &testsv1.Metadata{
					Tenant: "my-tenant",
				},
			}
			response, err := generic.Create().
				SetObject(object).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			result := response.GetObject()
			Expect(result).ToNot(BeNil())
			Expect(result.Metadata).ToNot(BeNil())
			Expect(result.Metadata.CreationTimestamp).ToNot(BeNil())
			Expect(result.Metadata.CreationTimestamp.AsTime()).ToNot(BeZero())
		})

		It("Doesn't set deletion timestamp when creating", func() {
			response, err := generic.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "my-object",
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object := response.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.Metadata).ToNot(BeNil())
			Expect(object.Metadata.DeletionTimestamp).To(BeNil())
		})

		It("Sets name when creating", func() {
			// Create the object with a name and verify that the result has the name set:
			object := &testsv1.Object{
				Metadata: &testsv1.Metadata{
					Name:   "my-name",
					Tenant: "my-tenant",
				},
			}
			response, err := generic.Create().
				SetObject(object).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object = response.GetObject()
			Expect(object.GetMetadata().GetName()).To(Equal("my-name"))

			// Get the object and verify that the result has the name set:
			getResponse, err := generic.Get().
				SetId(object.GetId()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object = getResponse.GetObject()
			Expect(object.GetMetadata().GetName()).To(Equal("my-name"))
		})

		It("Sets labels when creating", func() {
			object := &testsv1.Object{
				Metadata: &testsv1.Metadata{
					Labels: map[string]string{
						"my-label": "my-value",
					},
					Tenant: "my-tenant",
				},
			}
			response, err := generic.Create().
				SetObject(object).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object = response.GetObject()
			Expect(object.GetMetadata().GetLabels()).To(Equal(map[string]string{
				"my-label": "my-value",
			}))

			getResponse, err := generic.Get().
				SetId(object.GetId()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object = getResponse.GetObject()
			Expect(object.GetMetadata().GetLabels()).To(Equal(map[string]string{
				"my-label": "my-value",
			}))
		})

		It("Sets annotations when creating", func() {
			object := &testsv1.Object{
				Metadata: &testsv1.Metadata{
					Annotations: map[string]string{
						"my-annotation": "my-value",
					},
					Tenant: "my-tenant",
				},
			}
			response, err := generic.Create().
				SetObject(object).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object = response.GetObject()
			annotations := object.GetMetadata().GetAnnotations()
			Expect(annotations).To(HaveKeyWithValue("my-annotation", "my-value"))

			getResponse, err := generic.Get().
				SetId(object.GetId()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object = getResponse.GetObject()
			annotations = object.GetMetadata().GetAnnotations()
			Expect(annotations).To(HaveKeyWithValue("my-annotation", "my-value"))
		})

		It("Generates non empty identifiers", func() {
			response, err := generic.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "my-object",
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object := response.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).ToNot(BeEmpty())
		})

		It("Doesn't put the generated identifier inside the input object", func() {
			object := &testsv1.Object{
				Metadata: &testsv1.Metadata{
					Tenant: "my-tenant",
				},
			}
			_, err := generic.Create().
				SetObject(object).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(object.GetId()).To(BeEmpty())
		})

		It("Doesn't put the generated metadata inside the input object", func() {
			object := &testsv1.Object{
				Metadata: &testsv1.Metadata{
					Tenant: "my-tenant",
				},
			}
			_, err := generic.Create().
				SetObject(object).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(object.GetMetadata().GetCreationTimestamp()).To(BeNil())
		})

		It("Returns 'already exists' when creating object with existing identifier", func() {
			// Create an object with a specific identifier:
			id := uuid.New()
			_, err := generic.Create().
				SetObject(
					testsv1.Object_builder{
						Id: id,
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "my-object",
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Try to create another object with the same identifier:
			_, err = generic.Create().
				SetObject(
					testsv1.Object_builder{
						Id: id,
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "my-object",
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).To(HaveOccurred())
			var alreadyExistsErr *ErrAlreadyExists
			Expect(errors.As(err, &alreadyExistsErr)).To(BeTrue())
			Expect(alreadyExistsErr.ID).To(Equal(id))
		})

		It("Gets object", func() {
			createResponse, err := generic.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "my-object",
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()
			getResponse, err := generic.Get().
				SetId(object.GetId()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			result := getResponse.GetObject()
			Expect(result).ToNot(BeNil())
		})

		It("Returns not found error when getting object that doesn't exist", func() {
			_, err := generic.Get().
				SetId("does-not-exist").
				Do(ctx)
			Expect(err).To(HaveOccurred())
			var notFoundErr *ErrNotFound
			Expect(errors.As(err, &notFoundErr)).To(BeTrue())
			Expect(notFoundErr.IDs).To(ConsistOf("does-not-exist"))
		})

		It("Lists objects", func() {
			// Insert a couple of rows:
			const count = 2
			for i := range count {
				_, err := generic.Create().
					SetObject(
						testsv1.Object_builder{
							Metadata: testsv1.Metadata_builder{
								Tenant: "my-tenant",
								Name:   fmt.Sprintf("my-object-%d", i),
							}.Build(),
						}.Build(),
					).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
			}

			// Try to list:
			response, err := generic.List().
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(count))
			for _, item := range response.GetItems() {
				Expect(item).ToNot(BeNil())
			}
		})

		It("Lists objects sorted by identifier by default", func() {
			// Create objects with specific identifier in non-alphabetical order:
			ids := []string{"zebra", "apple", "banana"}
			objects := make([]*testsv1.Object, len(ids))
			for i, id := range ids {
				createResponse, err := generic.Create().
					SetObject(
						testsv1.Object_builder{
							Id: id,
							Metadata: testsv1.Metadata_builder{
								Tenant: "my-tenant",
								Name:   fmt.Sprintf("my-object-%d", i),
							}.Build(),
						}.Build(),
					).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				objects[i] = createResponse.GetObject()
			}

			// List objects and verify they are sorted by identifier:
			response, err := generic.List().
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(len(ids)))

			// Verify the objects are returned in alphabetical order:
			expected := []string{"apple", "banana", "zebra"}
			for i, item := range response.GetItems() {
				Expect(item.GetId()).To(Equal(expected[i]))
			}
		})

		It("Doesn't save the creation identifier in the 'data' column", func() {
			// Create an object:
			response, err := generic.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "my-object",
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object := response.GetObject()

			// Verify that the idientifier isn't stored in the 'data' column:
			row := tx.QueryRow(ctx, "select data from objects where id = $1", object.GetId())
			var data []byte
			err = row.Scan(&data)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(data)).ToNot(ContainSubstring(object.GetId()))
		})

		It("Doesn't save the creation timestamp in the 'data' column", func() {
			// Create an object:
			response, err := generic.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "my-object",
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object := response.GetObject()

			// Verify that the database isn't stored in the 'data' column:
			row := tx.QueryRow(ctx, "select data from objects where id = $1", object.GetId())
			var data []byte
			err = row.Scan(&data)
			Expect(err).ToNot(HaveOccurred())
			var value map[string]any
			err = json.Unmarshal(data, &value)
			Expect(err).ToNot(HaveOccurred())
			Expect(value).ToNot(HaveKey("creation_timestamp"))
		})

		It("Archives object if it has no finalizers when it is deleted", func() {
			// Create an object without finalizers:
			response, err := generic.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "my-object",
						}.Build(),
						MyString: "my value",
						MyBool:   true,
						MyInt32:  123,
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object := response.GetObject()

			// Delete the object:
			_, err = generic.Delete().
				SetId(object.GetId()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Verify that the object has been deleted and the data copied to the archive table:
			row := tx.QueryRow(ctx, `select count(*) from objects where id = $1`, object.GetId())
			var count int
			err = row.Scan(&count)
			Expect(err).ToNot(HaveOccurred())
			Expect(count).To(BeZero())
			row = tx.QueryRow(
				ctx,
				`
				select
					creation_timestamp,
					deletion_timestamp,
					archival_timestamp,
					data
				from
					archived_objects
				where
					id = $1
				`,
				object.GetId(),
			)
			var (
				creationTs time.Time
				deletionTs time.Time
				archivalTs time.Time
				data       []byte
			)
			err = row.Scan(
				&creationTs,
				&deletionTs,
				&archivalTs,
				&data,
			)
			Expect(err).ToNot(HaveOccurred())
			metadata := object.GetMetadata()
			now := time.Now()
			Expect(creationTs).To(BeTemporally("==", metadata.GetCreationTimestamp().AsTime()))
			Expect(deletionTs).To(BeTemporally("~", now, time.Second))
			Expect(archivalTs).To(BeTemporally("~", now, time.Second))
			Expect(data).To(MatchJSON(`{
				"my_string": "my value",
				"my_bool": true,
				"my_int32": 123
			}`))
		})

		It("Archives object when it is updated removing the finalizers", func() {
			// Create an object with finalizers:
			response, err := generic.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Finalizers: []string{"a"},
							Tenant:     "my-tenant",
							Name:       "my-object",
						}.Build(),
						MyString: "my value",
						MyBool:   true,
						MyInt32:  123,
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object := response.GetObject()

			// Delete the object:
			_, err = generic.Delete().
				SetId(object.GetId()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Verify that it hasn't been archived:
			row := tx.QueryRow(ctx, `select count(*) from archived_objects where id = $1`, object.GetId())
			var count int
			err = row.Scan(&count)
			Expect(err).ToNot(HaveOccurred())
			Expect(count).To(BeZero())

			// Update the object removing the finalizers:
			object.GetMetadata().SetFinalizers([]string{})
			object.SetMyString("your value")
			object.SetMyBool(false)
			object.SetMyInt32(456)
			updateResponse, err := generic.Update().
				SetObject(object).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object = updateResponse.GetObject()

			// Verify that it has been removed and copied to the archive table:
			row = tx.QueryRow(
				ctx,
				`
				select
					creation_timestamp,
					deletion_timestamp,
					archival_timestamp,
					data
				from
					archived_objects
				where
					id = $1
				`,
				object.GetId(),
			)
			var (
				creationTs time.Time
				deletionTs time.Time
				archivalTs time.Time
				data       []byte
			)
			err = row.Scan(
				&creationTs,
				&deletionTs,
				&archivalTs,
				&data,
			)
			Expect(err).ToNot(HaveOccurred())
			metadata := object.GetMetadata()
			now := time.Now()
			Expect(creationTs).To(BeTemporally("==", metadata.GetCreationTimestamp().AsTime()))
			Expect(deletionTs).To(BeTemporally("~", metadata.GetDeletionTimestamp().AsTime()))
			Expect(archivalTs).To(BeTemporally("~", now, time.Second))
			Expect(data).To(MatchJSON(`{
				"my_string": "your value",
				"my_int32": 456
			}`))
		})

		It("Copies labels and annotations when archived on delete", func() {
			// Create an object without finalizers:
			response, err := generic.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Labels: map[string]string{
								"my-label": "my-value",
							},
							Annotations: map[string]string{
								"my-annotation": "my-value",
							},
							Tenant: "my-tenant",
							Name:   "my-object",
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object := response.GetObject()

			// Delete the object:
			_, err = generic.Delete().
				SetId(object.GetId()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Verify that labels and annotations were archived:
			row := tx.QueryRow(
				ctx,
				`
				select
					labels,
					annotations
				from
					archived_objects
				where
					id = $1
				`,
				object.GetId(),
			)
			var (
				labelsData      []byte
				annotationsData []byte
			)
			err = row.Scan(&labelsData, &annotationsData)
			Expect(err).ToNot(HaveOccurred())
			var labels map[string]string
			err = json.Unmarshal(labelsData, &labels)
			Expect(err).ToNot(HaveOccurred())
			Expect(labels).To(Equal(map[string]string{
				"my-label": "my-value",
			}))
			var annotations map[string]string
			err = json.Unmarshal(annotationsData, &annotations)
			Expect(err).ToNot(HaveOccurred())
			Expect(annotations).To(Equal(map[string]string{
				"my-annotation": "my-value",
			}))
		})

		It("Copies labels and annotations when archived on update", func() {
			// Create an object with finalizers:
			response, err := generic.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Finalizers: []string{"a"},
							Tenant:     "my-tenant",
							Name:       "my-object",
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object := response.GetObject()

			// Delete the object:
			_, err = generic.Delete().
				SetId(object.GetId()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Update the object removing finalizers and adding labels and annotations:
			metadata := object.GetMetadata()
			metadata.SetFinalizers([]string{})
			metadata.SetLabels(map[string]string{
				"my-label": "my-value",
			})
			metadata.SetAnnotations(map[string]string{
				"my-annotation": "my-value",
			})
			_, err = generic.Update().
				SetObject(object).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Verify that labels and annotations were archived:
			row := tx.QueryRow(
				ctx,
				`
				select
					labels,
					annotations
				from
					archived_objects
				where
					id = $1
				`,
				object.GetId(),
			)
			var (
				labelsData      []byte
				annotationsData []byte
			)
			err = row.Scan(&labelsData, &annotationsData)
			Expect(err).ToNot(HaveOccurred())
			var labels map[string]string
			err = json.Unmarshal(labelsData, &labels)
			Expect(err).ToNot(HaveOccurred())
			Expect(labels).To(Equal(map[string]string{
				"my-label": "my-value",
			}))
			var annotations map[string]string
			err = json.Unmarshal(annotationsData, &annotations)
			Expect(err).ToNot(HaveOccurred())
			Expect(annotations).To(Equal(map[string]string{
				"my-annotation": "my-value",
			}))
		})

		It("Returns not found error when deleting object that doesn't exist", func() {
			_, err := generic.Delete().
				SetId("does_not_exist").
				Do(ctx)
			Expect(err).To(HaveOccurred())
			var notFoundErr *ErrNotFound
			Expect(errors.As(err, &notFoundErr)).To(BeTrue())
			Expect(notFoundErr.IDs).To(ConsistOf("does_not_exist"))
		})

		Describe("Finalizers", func() {
			checkDatabase := func(object *testsv1.Object, expected ...string) {
				row := tx.QueryRow(ctx, "select finalizers from objects where id = $1", object.GetId())
				var actual []string
				err := row.Scan(&actual)
				Expect(err).ToNot(HaveOccurred())
				values := make([]any, len(expected))
				for i, value := range expected {
					values[i] = value
				}
				Expect(actual).To(ConsistOf(values))
			}

			It("Gets finalizers", func() {
				response, err := generic.Create().
					SetObject(
						testsv1.Object_builder{
							Metadata: testsv1.Metadata_builder{
								Finalizers: []string{"a", "b"},
								Tenant:     "my-tenant",
								Name:       "my-object",
							}.Build(),
						}.Build(),
					).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				object := response.GetObject()
				getResponse, err := generic.Get().
					SetId(object.GetId()).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				object = getResponse.GetObject()
				Expect(object.GetMetadata().GetFinalizers()).To(ConsistOf("a", "b"))
			})

			It("Lists finalizers", func() {
				createResponse, err := generic.Create().
					SetObject(
						testsv1.Object_builder{
							Metadata: testsv1.Metadata_builder{
								Finalizers: []string{"a", "b"},
								Tenant:     "my-tenant",
								Name:       "my-object",
							}.Build(),
						}.Build(),
					).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				object := createResponse.GetObject()
				listResponse, err := generic.List().
					SetFilter(fmt.Sprintf("this.id == '%s'", object.GetId())).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				objects := listResponse.GetItems()
				Expect(objects).To(HaveLen(1))
				object = objects[0]
				Expect(object.GetMetadata().GetFinalizers()).To(ConsistOf("a", "b"))
			})

			It("Creates object without finalizers", func() {
				response, err := generic.Create().
					SetObject(
						testsv1.Object_builder{
							Metadata: testsv1.Metadata_builder{
								Tenant: "my-tenant",
								Name:   "my-object",
							}.Build(),
						}.Build(),
					).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				object := response.GetObject()
				Expect(object.GetMetadata().GetFinalizers()).To(BeEmpty())
				checkDatabase(object)
			})

			It("Creates object with one finalizer", func() {
				response, err := generic.Create().
					SetObject(
						testsv1.Object_builder{
							Metadata: testsv1.Metadata_builder{
								Finalizers: []string{"a"},
								Tenant:     "my-tenant",
								Name:       "my-object",
							}.Build(),
						}.Build()).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				object := response.GetObject()
				Expect(object.GetMetadata().GetFinalizers()).To(ConsistOf("a"))
				checkDatabase(object, "a")
			})

			It("Creates object with two finalizers", func() {
				response, err := generic.Create().
					SetObject(
						testsv1.Object_builder{
							Metadata: testsv1.Metadata_builder{
								Finalizers: []string{"a", "b"},
								Tenant:     "my-tenant",
								Name:       "my-object",
							}.Build(),
						}.Build(),
					).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				object := response.GetObject()
				Expect(object.GetMetadata().GetFinalizers()).To(ConsistOf("a", "b"))
				checkDatabase(object, "a", "b")
			})

			It("Eliminates duplicated finalizers when object is created", func() {
				response, err := generic.Create().
					SetObject(
						testsv1.Object_builder{
							Metadata: testsv1.Metadata_builder{
								Finalizers: []string{"a", "a"},
								Tenant:     "my-tenant",
								Name:       "my-object",
							}.Build(),
						}.Build(),
					).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				object := response.GetObject()
				Expect(object.GetMetadata().GetFinalizers()).To(ConsistOf("a"))
				checkDatabase(object, "a")
			})

			It("Adds one finalizer when object is updated", func() {
				response, err := generic.Create().
					SetObject(
						testsv1.Object_builder{
							Metadata: testsv1.Metadata_builder{
								Tenant: "my-tenant",
								Name:   "my-object",
							}.Build(),
						}.Build(),
					).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				object := response.GetObject()
				object.GetMetadata().SetFinalizers([]string{"a"})
				updateResponse, err := generic.Update().
					SetObject(object).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				object = updateResponse.GetObject()
				Expect(object.GetMetadata().GetFinalizers()).To(ConsistOf("a"))
				checkDatabase(object, "a")
			})

			It("Adds two finalizers when object is updated", func() {
				response, err := generic.Create().
					SetObject(
						testsv1.Object_builder{
							Metadata: testsv1.Metadata_builder{
								Tenant: "my-tenant",
								Name:   "my-object",
							}.Build(),
						}.Build(),
					).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				object := response.GetObject()
				object.GetMetadata().SetFinalizers([]string{"a", "b"})
				updateResponse, err := generic.Update().
					SetObject(object).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				object = updateResponse.GetObject()
				Expect(object.GetMetadata().GetFinalizers()).To(ConsistOf("a", "b"))
				checkDatabase(object, "a", "b")
			})

			It("Replaces finalizers when object is updated", func() {
				response, err := generic.Create().
					SetObject(
						testsv1.Object_builder{
							Metadata: testsv1.Metadata_builder{
								Finalizers: []string{"a", "b"},
								Tenant:     "my-tenant",
								Name:       "my-object",
							}.Build(),
						}.Build(),
					).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				object := response.GetObject()
				object.GetMetadata().SetFinalizers([]string{"a", "c"})
				updateResponse, err := generic.Update().
					SetObject(object).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				object = updateResponse.GetObject()
				Expect(object.GetMetadata().GetFinalizers()).To(ConsistOf("a", "c"))
				checkDatabase(object, "a", "c")
			})

			It("Eliminates duplicated finalizers when object is updated", func() {
				response, err := generic.Create().
					SetObject(
						testsv1.Object_builder{
							Metadata: testsv1.Metadata_builder{
								Tenant: "my-tenant",
								Name:   "my-object",
							}.Build(),
						}.Build(),
					).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				object := response.GetObject()
				object.GetMetadata().SetFinalizers([]string{"a", "a"})
				updateResponse, err := generic.Update().
					SetObject(object).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				object = updateResponse.GetObject()
				Expect(object.GetMetadata().GetFinalizers()).To(ConsistOf("a"))
				checkDatabase(object, "a")
			})
		})

		Describe("Paging", func() {
			var objects []*testsv1.Object

			BeforeEach(func() {
				// Create a list of objects and sort it like they will be sorted by the DAO. Not that
				// this works correctly because the DAO sorts object by identifier by default.
				objects = make([]*testsv1.Object, objectCount)
				for i := range len(objects) {
					objects[i] = testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   fmt.Sprintf("my-object-%d", i),
						}.Build(),
					}.Build()
					response, err := generic.Create().
						SetObject(objects[i]).
						Do(ctx)
					Expect(err).ToNot(HaveOccurred())
					objects[i] = response.GetObject()
				}
				sort(objects)
			})

			It("Uses zero as default offset", func() {
				response, err := generic.List().
					SetLimit(1).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetItems()[0].Id).To(Equal(objects[0].Id))
			})

			It("Honours valid offset", func() {
				for i := range len(objects) {
					response, err := generic.List().
						SetOffset(int32(i)).
						SetLimit(1).
						Do(ctx)
					Expect(err).ToNot(HaveOccurred())
					Expect(response.GetItems()[0].Id).To(Equal(objects[i].Id))
				}
			})

			It("Returns empty list if offset is greater or equal than available items", func() {
				response, err := generic.List().
					SetOffset(objectCount).
					SetLimit(1).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetItems()).To(BeEmpty())
			})

			It("Ignores negative offset", func() {
				response, err := generic.List().
					SetOffset(-123).
					SetLimit(1).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetItems()[0].Id).To(Equal(objects[0].Id))
			})

			It("Rejects negative limit with an error", func() {
				_, err := generic.List().
					SetLimit(-123).
					Do(ctx)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("limit must be a non-negative integer"))
			})

			It("Returns only count when limit is explicitly set to zero", func() {
				response, err := generic.List().
					SetLimit(0).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetSize()).To(BeZero())
				Expect(response.GetTotal()).To(BeNumerically("==", objectCount))
				Expect(response.GetItems()).To(BeEmpty())
			})

			It("Uses default limit when limit is not set", func() {
				response, err := generic.List().
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetSize()).To(BeNumerically("==", defaultLimit))
				Expect(response.GetItems()).To(HaveLen(defaultLimit))
			})

			It("Truncates limit to the maximum", func() {
				response, err := generic.List().
					SetLimit(maxLimit + 1).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetSize()).To(BeNumerically("==", maxLimit))
				Expect(response.GetItems()).To(HaveLen(maxLimit))
			})

			It("Honours valid limit", func() {
				for i := 1; i < maxLimit; i++ {
					response, err := generic.List().
						SetLimit(int32(i)).
						Do(ctx)
					Expect(err).ToNot(HaveOccurred())
					Expect(response.GetSize()).To(BeNumerically("==", i))
					Expect(response.GetItems()).To(HaveLen(i))
				}
			})

			It("Returns less items than requested if there are not enough", func() {
				response, err := generic.List().
					SetOffset(objectCount - 2).
					SetLimit(10).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetSize()).To(BeNumerically("==", 2))
				Expect(response.GetItems()).To(HaveLen(2))
			})

			It("Returns the total number of items", func() {
				response, err := generic.List().
					SetLimit(1).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetTotal()).To(BeNumerically("==", objectCount))
			})
		})

		Describe("Check if object exists", func() {
			It("Returns true if the object exists", func() {
				response, err := generic.Create().
					SetObject(
						testsv1.Object_builder{
							Metadata: testsv1.Metadata_builder{
								Tenant: "my-tenant",
								Name:   "my-object",
							}.Build(),
						}.Build(),
					).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				object := response.GetObject()
				existsResponse, err := generic.Exists().
					SetId(object.GetId()).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(existsResponse.GetExists()).To(BeTrue())
			})

			It("Returns false if the object doesn't exist", func() {
				response, err := generic.Exists().
					SetId(uuid.New()).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetExists()).To(BeFalse())
			})
		})

		It("Updates object", func() {
			response, err := generic.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "my-object",
						}.Build(),
						MyString: "my_value",
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object := response.GetObject()
			object.MyString = "your_value"
			updateResponse, err := generic.Update().
				SetObject(object).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object = updateResponse.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetMyString()).To(Equal("your_value"))
		})

		It("Returns current object when updating with no changes", func() {
			// Create an object:
			createResponse, err := generic.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "my-object",
						}.Build(),
						MyString: "my_value",
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			created := createResponse.GetObject()
			Expect(created).ToNot(BeNil())
			Expect(created.GetId()).ToNot(BeEmpty())
			Expect(created.GetMyString()).To(Equal("my_value"))

			// Update with the same object (no changes):
			updateResponse, err := generic.Update().
				SetObject(created).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			updated := updateResponse.GetObject()
			Expect(updated).ToNot(BeNil())
			Expect(updated.GetId()).To(Equal(created.GetId()))
			Expect(updated.GetMyString()).To(Equal("my_value"))
			Expect(updated.GetMetadata()).ToNot(BeNil())
			Expect(updated.GetMetadata().GetCreationTimestamp()).To(Equal(created.GetMetadata().GetCreationTimestamp()))
		})

		It("Updates labels", func() {
			response, err := generic.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Labels: map[string]string{
								"my-label": "my-value",
							},
							Tenant: "my-tenant",
							Name:   "my-object",
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object := response.GetObject()

			object.GetMetadata().SetLabels(map[string]string{
				"your-label": "your-value",
			})
			updateResponse, err := generic.Update().
				SetObject(object).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object = updateResponse.GetObject()
			Expect(object.GetMetadata().GetLabels()).To(Equal(map[string]string{
				"your-label": "your-value",
			}))

			getResponse, err := generic.Get().
				SetId(object.GetId()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object = getResponse.GetObject()
			Expect(object.GetMetadata().GetLabels()).To(Equal(map[string]string{
				"your-label": "your-value",
			}))
		})

		It("Updates annotations", func() {
			response, err := generic.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Annotations: map[string]string{
								"my-annotation": "my-value",
							},
							Tenant: "my-tenant",
							Name:   "my-object",
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object := response.GetObject()

			object.GetMetadata().SetAnnotations(map[string]string{
				"your-annotation": "your-value",
			})
			updateResponse, err := generic.Update().
				SetObject(object).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object = updateResponse.GetObject()
			annotations := object.GetMetadata().GetAnnotations()
			Expect(annotations).To(HaveKeyWithValue("your-annotation", "your-value"))

			getResponse, err := generic.Get().
				SetId(object.GetId()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object = getResponse.GetObject()
			annotations = object.GetMetadata().GetAnnotations()
			Expect(annotations).To(HaveKeyWithValue("your-annotation", "your-value"))
		})

		It("Updates finalizers", func() {
			response, err := generic.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Finalizers: []string{"my-finalizer"},
							Tenant:     "my-tenant",
							Name:       "my-object",
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object := response.GetObject()

			object.GetMetadata().SetFinalizers([]string{"your-finalizer"})
			updateResponse, err := generic.Update().
				SetObject(object).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object = updateResponse.GetObject()
			Expect(object.GetMetadata().GetFinalizers()).To(Equal([]string{"your-finalizer"}))

			getResponse, err := generic.Get().
				SetId(object.GetId()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object = getResponse.GetObject()
			Expect(object.GetMetadata().GetFinalizers()).To(Equal([]string{"your-finalizer"}))
		})

		It("Rejects updating object with empty tenant", func() {
			response, err := generic.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "my-object",
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object := response.GetObject()

			object.GetMetadata().SetTenant("")
			_, err = generic.Update().
				SetObject(object).
				Do(ctx)
			Expect(err).To(MatchError(ContainSubstring("cannot update object with empty tenant")))
		})

		It("Returns not found error when updating object that doesn't exist", func() {
			_, err := generic.Update().
				SetObject(
					testsv1.Object_builder{
						Id: "does-not-exist",
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "my-object",
						}.Build(),
						MyString: "some-value",
					}.Build(),
				).
				Do(ctx)
			Expect(err).To(HaveOccurred())
			var notFoundErr *ErrNotFound
			Expect(errors.As(err, &notFoundErr)).To(BeTrue())
			Expect(notFoundErr.IDs).To(ConsistOf("does-not-exist"))
		})
	})

	Describe("Filtering", func() {
		var objectsDao *GenericDAO[*testsv1.Object]

		BeforeEach(func() {
			var err error

			// Create the DAO:
			objectsDao, err = NewGenericDAO[*testsv1.Object]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Filters by identifier", func() {
			for i := range 10 {
				_, err := objectsDao.Create().
					SetObject(
						testsv1.Object_builder{
							Id: fmt.Sprintf("%d", i),
							Metadata: testsv1.Metadata_builder{
								Tenant: "my-tenant",
								Name:   fmt.Sprintf("my-object-%d", i),
							}.Build(),
						}.Build(),
					).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
			}
			response, err := objectsDao.List().
				SetFilter("this.id == '5'").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			items := response.GetItems()
			Expect(items).To(HaveLen(1))
			Expect(items[0].GetId()).To(Equal("5"))
		})

		It("Filters by identifier set", func() {
			for i := range 10 {
				_, err := objectsDao.Create().
					SetObject(
						testsv1.Object_builder{
							Id: fmt.Sprintf("%d", i),
							Metadata: testsv1.Metadata_builder{
								Tenant: "my-tenant",
								Name:   fmt.Sprintf("my-object-%d", i),
							}.Build(),
						}.Build(),
					).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
			}
			response, err := objectsDao.List().
				SetFilter("this.id in ['1', '3', '5', '7', '9']").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			items := response.GetItems()
			sort(items)
			Expect(items).To(HaveLen(5))
			Expect(items[0].GetId()).To(Equal("1"))
			Expect(items[1].GetId()).To(Equal("3"))
			Expect(items[2].GetId()).To(Equal("5"))
			Expect(items[3].GetId()).To(Equal("7"))
			Expect(items[4].GetId()).To(Equal("9"))
		})

		It("Filters by string JSON field", func() {
			for i := range 10 {
				_, err := objectsDao.Create().
					SetObject(
						testsv1.Object_builder{
							Metadata: testsv1.Metadata_builder{
								Tenant: "my-tenant",
								Name:   fmt.Sprintf("my-object-%d", i),
							}.Build(),
							MyString: fmt.Sprintf("my_value_%d", i),
						}.Build(),
					).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
			}
			response, err := objectsDao.List().
				SetFilter("this.my_string == 'my_value_5'").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			items := response.GetItems()
			Expect(items).To(HaveLen(1))
			Expect(items[0].GetMyString()).To(Equal("my_value_5"))
		})

		It("Filters by identifier or JSON field", func() {
			for i := range 10 {
				_, err := objectsDao.Create().
					SetObject(
						testsv1.Object_builder{
							Id: fmt.Sprintf("%d", i),
							Metadata: testsv1.Metadata_builder{
								Tenant: "my-tenant",
								Name:   fmt.Sprintf("my-object-%d", i),
							}.Build(),
							MyString: fmt.Sprintf("my_value_%d", i),
						}.Build(),
					).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
			}
			response, err := objectsDao.List().
				SetFilter("this.id == '1' || this.my_string == 'my_value_3'").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			items := response.GetItems()
			sort(items)
			Expect(items).To(HaveLen(2))
			Expect(items[0].GetId()).To(Equal("1"))
			Expect(items[0].GetMyString()).To(Equal("my_value_1"))
			Expect(items[1].GetId()).To(Equal("3"))
			Expect(items[1].GetMyString()).To(Equal("my_value_3"))
		})

		It("Filters by identifier and JSON field", func() {
			for i := range 10 {
				_, err := objectsDao.Create().
					SetObject(
						testsv1.Object_builder{
							Id: fmt.Sprintf("%d", i),
							Metadata: testsv1.Metadata_builder{
								Tenant: "my-tenant",
								Name:   fmt.Sprintf("my-object-%d", i),
							}.Build(),
							MyString: fmt.Sprintf("my_value_%d", i),
						}.Build(),
					).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
			}
			response, err := objectsDao.List().
				SetFilter("this.id == '1' && this.my_string == 'my_value_1'").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			items := response.GetItems()
			Expect(items).To(HaveLen(1))
			Expect(items[0].GetId()).To(Equal("1"))
			Expect(items[0].GetMyString()).To(Equal("my_value_1"))
		})

		It("Filters by calculated value", func() {
			for i := range 10 {
				_, err := objectsDao.Create().
					SetObject(
						testsv1.Object_builder{
							Metadata: testsv1.Metadata_builder{
								Tenant: "my-tenant",
								Name:   fmt.Sprintf("my-object-%d", i),
							}.Build(),
							MyInt32: int32(i),
						}.Build(),
					).Do(ctx)
				Expect(err).ToNot(HaveOccurred())
			}
			response, err := objectsDao.List().
				SetFilter("(this.my_int32 + 1) == 2").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			items := response.GetItems()
			Expect(items).To(HaveLen(1))
			Expect(items[0].GetMyInt32()).To(BeNumerically("==", 1))
		})

		It("Filters by nested JSON string field", func() {
			for i := range 10 {
				_, err := objectsDao.Create().
					SetObject(
						testsv1.Object_builder{
							Metadata: testsv1.Metadata_builder{
								Tenant: "my-tenant",
								Name:   fmt.Sprintf("my-object-%d", i),
							}.Build(),
							Spec: testsv1.Spec_builder{
								SpecString: fmt.Sprintf("my_value_%d", i),
							}.Build(),
						}.Build(),
					).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
			}
			response, err := objectsDao.List().
				SetFilter("this.spec.spec_string == 'my_value_5'").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			items := response.GetItems()
			Expect(items).To(HaveLen(1))
			Expect(items[0].GetSpec().GetSpecString()).ToNot(BeNil())
			Expect(items[0].GetSpec().GetSpecString()).To(Equal("my_value_5"))
		})

		It("Filters deleted", func() {
			createResponse, err := objectsDao.Create().
				SetObject(
					testsv1.Object_builder{
						Id: "0",
						Metadata: testsv1.Metadata_builder{
							Finalizers: []string{"a"},
							Tenant:     "my-tenant",
							Name:       "my-object",
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()
			_, err = objectsDao.Delete().
				SetId(object.GetId()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			response, err := objectsDao.List().
				SetFilter("this.metadata.deletion_timestamp != null").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			items := response.GetItems()
			Expect(items).To(HaveLen(1))
			Expect(items[0].GetId()).To(Equal("0"))
		})

		It("Filters not deleted", func() {
			createResponse, err := objectsDao.Create().
				SetObject(
					testsv1.Object_builder{
						Id: "0",
						Metadata: testsv1.Metadata_builder{
							Finalizers: []string{"a"},
							Tenant:     "my-tenant",
							Name:       "my-object",
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()
			_, err = objectsDao.Delete().
				SetId(object.GetId()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			response, err := objectsDao.List().
				SetFilter("this.metadata.deletion_timestamp == null").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			items := response.GetItems()
			Expect(items).To(BeEmpty())
		})

		It("Filters by timestamp in the future", func() {
			var err error
			now := time.Now()
			_, err = objectsDao.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "old",
						}.Build(),
						MyTimestamp: timestamppb.New(now.Add(-time.Minute)),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = objectsDao.Create().
				SetObject(
					testsv1.Object_builder{
						Id: "new",
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "new",
						}.Build(),
						MyTimestamp: timestamppb.New(now.Add(+time.Minute)),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			response, err := objectsDao.List().
				SetFilter("this.my_timestamp > now").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			items := response.GetItems()
			Expect(items).To(HaveLen(1))
			Expect(items[0].GetMetadata().GetName()).To(Equal("new"))
		})

		It("Filters by timestamp in the past", func() {
			var err error
			now := time.Now()
			_, err = objectsDao.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "old",
						}.Build(),
						MyTimestamp: timestamppb.New(now.Add(-time.Minute)),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = objectsDao.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "new",
						}.Build(),
						MyTimestamp: timestamppb.New(now.Add(+time.Minute)),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			response, err := objectsDao.List().
				SetFilter("this.my_timestamp < now").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			items := response.GetItems()
			Expect(items).To(HaveLen(1))
			Expect(items[0].GetMetadata().GetName()).To(Equal("old"))
		})

		It("Filters by presence of message field", func() {
			var err error
			_, err = objectsDao.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "good",
						}.Build(),
						Spec: testsv1.Spec_builder{}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = objectsDao.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "bad",
						}.Build(),
						Spec: nil,
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			response, err := objectsDao.List().
				SetFilter("has(this.spec)").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			items := response.GetItems()
			Expect(items).To(HaveLen(1))
			Expect(items[0].GetMetadata().GetName()).To(Equal("good"))
		})

		It("Filters by presence of string field", func() {
			var err error
			_, err = objectsDao.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "good",
						}.Build(),
						MyString: "my value",
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = objectsDao.Create().
				SetObject(
					testsv1.Object_builder{
						Id: "bad",
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "bad",
						}.Build(),
						MyString: "",
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			response, err := objectsDao.List().
				SetFilter("has(this.my_string)").Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			items := response.GetItems()
			Expect(items).To(HaveLen(1))
			Expect(items[0].GetMetadata().GetName()).To(Equal("good"))
		})

		It("Filters by presence of deletion timestamp", func() {
			createReponse, err := objectsDao.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Finalizers: []string{"a"},
							Tenant:     "my-tenant",
							Name:       "good",
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			goodObject := createReponse.GetObject()
			goodId := goodObject.GetId()
			_, err = objectsDao.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Finalizers: []string{"a"},
							Tenant:     "my-tenant",
							Name:       "bad",
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = objectsDao.Delete().
				SetId(goodId).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			response, err := objectsDao.List().
				SetFilter("has(this.metadata.deletion_timestamp)").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			items := response.GetItems()
			Expect(items).To(HaveLen(1))
			Expect(items[0].GetMetadata().GetName()).To(Equal("good"))
		})

		It("Filters by absence of deletion timestamp", func() {
			_, err := objectsDao.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Finalizers: []string{"a"},
							Tenant:     "my-tenant",
							Name:       "good",
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			createReponse, err := objectsDao.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Finalizers: []string{"a"},
							Tenant:     "my-tenant",
							Name:       "bad",
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			badObject := createReponse.GetObject()
			badId := badObject.GetId()
			_, err = objectsDao.Delete().
				SetId(badId).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			response, err := objectsDao.List().
				SetFilter("!has(this.metadata.deletion_timestamp)").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			items := response.GetItems()
			Expect(items).To(HaveLen(1))
			Expect(items[0].GetMetadata().GetName()).To(Equal("good"))
		})

		It("Filters by presence of nested string field", func() {
			var err error
			_, err = objectsDao.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "good",
						}.Build(),
						Spec: testsv1.Spec_builder{
							SpecString: "my value",
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = objectsDao.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "bad",
						}.Build(),
						Spec: testsv1.Spec_builder{
							SpecString: "",
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			response, err := objectsDao.List().
				SetFilter("has(this.spec.spec_string)").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			items := response.GetItems()
			Expect(items).To(HaveLen(1))
			Expect(items[0].GetMetadata().GetName()).To(Equal("good"))
		})

		It("Filters by string prefix", func() {
			var err error
			_, err = objectsDao.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "good",
						}.Build(),
						MyString: "my value",
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = objectsDao.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "bad",
						}.Build(),
						MyString: "your value",
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			response, err := objectsDao.List().
				SetFilter("this.my_string.startsWith('my')").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			items := response.GetItems()
			Expect(items).To(HaveLen(1))
			Expect(items[0].GetMetadata().GetName()).To(Equal("good"))
		})

		It("Filters by string suffix", func() {
			var err error
			_, err = objectsDao.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "good",
						}.Build(),
						MyString: "value my",
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = objectsDao.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "bad",
						}.Build(),
						MyString: "value your",
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			response, err := objectsDao.List().
				SetFilter("this.my_string.endsWith('my')").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			items := response.GetItems()
			Expect(items).To(HaveLen(1))
			Expect(items[0].GetMetadata().GetName()).To(Equal("good"))
		})

		It("Escapes percent in prefix", func() {
			var err error
			_, err = objectsDao.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "good",
						}.Build(),
						MyString: "my% value",
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = objectsDao.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "bad",
						}.Build(),
						MyString: "my value",
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			response, err := objectsDao.List().
				SetFilter("this.my_string.startsWith('my%')").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			items := response.GetItems()
			Expect(items).To(HaveLen(1))
			Expect(items[0].GetMetadata().GetName()).To(Equal("good"))
		})

		It("Escapes underscore in prefix", func() {
			var err error
			_, err = objectsDao.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "good",
						}.Build(),
						MyString: "my_ value",
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = objectsDao.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "bad",
						}.Build(),
						MyString: "my value",
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			response, err := objectsDao.List().
				SetFilter("this.my_string.startsWith('my_')").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			items := response.GetItems()
			Expect(items).To(HaveLen(1))
			Expect(items[0].GetMetadata().GetName()).To(Equal("good"))
		})

		It("Filters by tenant", func() {
			// Create objects with 'my-tenant' set in metadata:
			_, err := objectsDao.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "my-object",
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = objectsDao.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "your-object",
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Filter by the tenant that is set in the metadata:
			response, err := objectsDao.List().
				SetFilter("this.metadata.tenant == 'my-tenant'").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			items := response.GetItems()
			Expect(items).To(HaveLen(2))
			for _, item := range items {
				Expect(item.GetMetadata().GetTenant()).To(Equal("my-tenant"))
			}

			// Filter by a non-existent tenant:
			response, err = objectsDao.List().
				SetFilter("this.metadata.tenant == 'non_existent_tenant'").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			items = response.GetItems()
			Expect(items).To(BeEmpty())
		})

		It("Filters by label key", func() {
			_, err := objectsDao.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Labels: map[string]string{
								"mylabel": "myvalue",
							},
							Tenant: "my-tenant",
							Name:   "with-label",
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = objectsDao.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "without-label",
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			response, err := objectsDao.List().
				SetFilter("'mylabel' in this.metadata.labels").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			items := response.GetItems()
			Expect(items).To(HaveLen(1))
			Expect(items[0].GetMetadata().GetName()).To(Equal("with-label"))
		})

		It("Uses public metadata maps for whole-map filters and rejects reserved keys", func() {
			publicObjectsDao, err := NewGenericDAO[*testsv1.Object]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				SetFilterDesc((*publicv1.Cluster)(nil).ProtoReflect().Descriptor()).
				Build()
			Expect(err).ToNot(HaveOccurred())

			objects := []struct {
				name        string
				labels      map[string]string
				annotations map[string]string
			}{
				{
					name: "reserved-label",
					labels: map[string]string{
						"ordinary":                        "visible",
						"probe":                           "admin.osac.openshift.io/managed",
						"admin.osac.openshift.io/managed": "system",
					},
					annotations: map[string]string{
						"ordinary": "visible",
						"probe":    "admin.osac.openshift.io/managed",
					},
				},
				{
					name:   "reserved-annotation",
					labels: map[string]string{"ordinary": "visible"},
					annotations: map[string]string{
						"ordinary": "visible",
						"controller.admin.osac.openshift.io/managed": "system",
					},
				},
				{
					name:        "ordinary-only",
					labels:      map[string]string{"ordinary": "visible"},
					annotations: map[string]string{"ordinary": "visible"},
				},
			}
			for _, object := range objects {
				_, err = objectsDao.Create().SetObject(testsv1.Object_builder{
					Metadata: testsv1.Metadata_builder{
						Name:        object.name,
						Tenant:      "my-tenant",
						Labels:      object.labels,
						Annotations: object.annotations,
					}.Build(),
				}.Build()).Do(ctx)
				Expect(err).ToNot(HaveOccurred())
			}

			filter := "this.metadata.labels == this.metadata.annotations"
			response, err := publicObjectsDao.List().SetFilter(filter).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetTotal()).To(Equal(int32(3)))
			Expect(response.GetItems()).To(HaveLen(3))

			countOnly, err := publicObjectsDao.List().SetFilter(filter).SetLimit(0).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(countOnly.GetTotal()).To(Equal(int32(3)))
			Expect(countOnly.GetItems()).To(BeEmpty())

			privateResponse, err := objectsDao.List().SetFilter(filter).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(privateResponse.GetTotal()).To(Equal(int32(1)))
			Expect(privateResponse.GetItems()).To(HaveLen(1))

			ordinaryResponse, err := publicObjectsDao.List().
				SetFilter(`this.metadata.labels['ordinary'] == 'visible'`).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(ordinaryResponse.GetTotal()).To(Equal(int32(3)))

			// A public filter must not use an ordinary label's value to probe
			// whether that value also appears as a reserved metadata key.
			probeResponse, err := publicObjectsDao.List().
				SetFilter(`this.metadata.labels['probe'] in this.metadata.labels`).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(probeResponse.GetTotal()).To(Equal(int32(0)))

			clusterDao, err := NewGenericDAO[*privatev1.Cluster]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				SetFilterDesc((*publicv1.Cluster)(nil).ProtoReflect().Descriptor()).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = clusterDao.Create().SetObject(privatev1.Cluster_builder{
				Metadata: privatev1.Metadata_builder{
					Tenant: "my-tenant",
					Labels: map[string]string{"ordinary": "visible"},
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			filterSQL, err := clusterDao.filterTranslator.Translate(ctx, `this.metadata.labels['ordinary'] == 'visible'`)
			Expect(err).ToNot(HaveOccurred())
			_, err = tx.Exec(ctx, `set local enable_seqscan = off`)
			Expect(err).ToNot(HaveOccurred())
			rows, err := tx.Query(ctx, `explain select id from clusters where `+filterSQL)
			Expect(err).ToNot(HaveOccurred())
			defer rows.Close()
			var queryPlan strings.Builder
			for rows.Next() {
				var line string
				Expect(rows.Scan(&line)).To(Succeed())
				queryPlan.WriteString(line)
			}
			Expect(rows.Err()).ToNot(HaveOccurred())
			Expect(queryPlan.String()).To(ContainSubstring("clusters_by_label"))

			for _, forbiddenFilter := range []string{
				`this.metadata.labels['admin.osac.openshift.io/managed'] == 'system'`,
				`'controller.admin.osac.openshift.io/managed' in this.metadata.annotations`,
			} {
				_, err = publicObjectsDao.List().SetFilter(forbiddenFilter).Do(ctx)
				var invalidFilterErr *ErrInvalidFilter
				Expect(errors.As(err, &invalidFilterErr)).To(BeTrue(), "filter %q: %v", forbiddenFilter, err)
			}
		})

		It("Filters by absence of label key", func() {
			// Create an object with a lable, and another without:
			_, err := objectsDao.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "with-label",
							Labels: map[string]string{
								"my-label": "my-value",
							},
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = objectsDao.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "without-label",
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// List by absence of label and verify that we only get the object without the label:
			response, err := objectsDao.List().
				SetFilter("!('my-label' in this.metadata.labels)").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			items := response.GetItems()
			Expect(items).To(HaveLen(1))
			Expect(items[0].GetMetadata().GetName()).To(Equal("without-label"))
		})

		It("Filters by integer in repeated int32 field", func() {
			_, err := objectsDao.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "with-int",
						}.Build(),
						MyInt32List: []int32{42, 100},
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = objectsDao.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "without-int",
						}.Build(),
						MyInt32List: []int32{99},
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			response, err := objectsDao.List().
				SetFilter("42 in this.my_int32_list").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			items := response.GetItems()
			Expect(items).To(HaveLen(1))
			Expect(items[0].GetMetadata().GetName()).To(Equal("with-int"))
		})

		It("Filters by field reference in repeated string field", func() {
			_, err := objectsDao.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "match",
						}.Build(),
						MyString:     "hello",
						MyStringList: []string{"hello", "world"},
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = objectsDao.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "no-match",
						}.Build(),
						MyString:     "other",
						MyStringList: []string{"hello", "world"},
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			response, err := objectsDao.List().
				SetFilter("this.my_string in this.my_string_list").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			items := response.GetItems()
			Expect(items).To(HaveLen(1))
			Expect(items[0].GetMetadata().GetName()).To(Equal("match"))
		})

		It("Filters by enum field", func() {
			_, err := objectsDao.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Name:   "enum-a",
							Tenant: "my-tenant",
						}.Build(),
						Spec: testsv1.Spec_builder{
							SpecEnum: testsv1.MyEnum_MY_ENUM_VALUE_A,
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = objectsDao.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Name:   "enum-b",
							Tenant: "my-tenant",
						}.Build(),
						Spec: testsv1.Spec_builder{
							SpecEnum: testsv1.MyEnum_MY_ENUM_VALUE_B,
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			response, err := objectsDao.List().
				SetFilter(fmt.Sprintf("this.spec.spec_enum == %d",
					int32(testsv1.MyEnum_MY_ENUM_VALUE_A))).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			items := response.GetItems()
			Expect(items).To(HaveLen(1))
			Expect(items[0].GetMetadata().GetName()).To(Equal("enum-a"))

			response, err = objectsDao.List().
				SetFilter(fmt.Sprintf("this.spec.spec_enum != %d",
					int32(testsv1.MyEnum_MY_ENUM_VALUE_A))).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			items = response.GetItems()
			Expect(items).To(HaveLen(1))
			Expect(items[0].GetMetadata().GetName()).To(Equal("enum-b"))
		})

		It("Filters by boolean field", func() {
			// Create two objects, one with the boolean set to 'true' and one with the boolean set to 'false':
			_, err := objectsDao.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Name:   "my-true",
							Tenant: "my-tenant",
						}.Build(),
						Spec: testsv1.Spec_builder{
							SpecBool: true,
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = objectsDao.Create().
				SetObject(
					testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Name:   "my-false",
							Tenant: "my-tenant",
						}.Build(),
						Spec: testsv1.Spec_builder{
							SpecBool: false,
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Check that we can get only the object with the boolean set to 'true':
			response, err := objectsDao.List().
				SetFilter("this.spec.spec_bool").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			items := response.GetItems()
			Expect(items).To(HaveLen(1))
			Expect(items[0].GetMetadata().GetName()).To(Equal("my-true"))

			// Check that we can get only the object with the boolean set to 'false':
			response, err = objectsDao.List().
				SetFilter("!this.spec.spec_bool").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			items = response.GetItems()
			Expect(items).To(HaveLen(1))
			Expect(items[0].GetMetadata().GetName()).To(Equal("my-false"))
		})

		Describe("Filter by project", func() {
			// createobject creates an object with the given project and name.
			createObject := func(ctx context.Context, project, name string) {
				_, err := objectsDao.Create().
					SetObject(testsv1.Object_builder{
						Metadata: testsv1.Metadata_builder{
							Tenant:  "my-tenant",
							Project: project,
							Name:    name,
						}.Build(),
					}.Build()).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
			}

			It("Filters by project", func() {
				// Create two projects with different names:
				createProject(ctx, "my-tenant", "project-a")
				createProject(ctx, "my-tenant", "project-b")

				// Create two objects, each belonging to a different project:
				createObject(ctx, "project-a", "object-a")
				createObject(ctx, "project-b", "object-b")

				// Filter by project and verify that we only get the object with the requested project:
				response, err := objectsDao.List().
					SetFilter("this.metadata.project == 'project-a'").
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				objects := response.GetItems()
				Expect(objects).To(HaveLen(1))
				object := objects[0]
				Expect(object.GetMetadata().GetName()).To(Equal("object-a"))
			})

			It("Filters by project prefix", func() {
				// Create two projects with different names:
				createProject(ctx, "my-tenant", "a-project")
				createProject(ctx, "my-tenant", "b-project")

				// Create two objects, each belonging to a different project:
				createObject(ctx, "a-project", "a-object")
				createObject(ctx, "b-project", "b-object")

				// Filter by project and verify that we only get the object with the requested project:
				response, err := objectsDao.List().
					SetFilter("this.metadata.project.startsWith('a-')").
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				objects := response.GetItems()
				Expect(objects).To(HaveLen(1))
				object := objects[0]
				Expect(object.GetMetadata().GetName()).To(Equal("a-object"))
			})

			It("Filters by project suffix", func() {
				// Create two projects with different names:
				createProject(ctx, "my-tenant", "project-a")
				createProject(ctx, "my-tenant", "project-b")

				// Create two objects, each belonging to a different project:
				createObject(ctx, "project-a", "object-a")
				createObject(ctx, "project-b", "object-b")

				// Filter by project and verify that we only get the object with the requested project:
				response, err := objectsDao.List().
					SetFilter("this.metadata.project.endsWith('-a')").
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				objects := response.GetItems()
				Expect(objects).To(HaveLen(1))
				object := objects[0]
				Expect(object.GetMetadata().GetName()).To(Equal("object-a"))
			})

			It("Filters by project contains", func() {
				// Create two projects with different names:
				createProject(ctx, "my-tenant", "my-a-project")
				createProject(ctx, "my-tenant", "my-b-project")

				// Create two objects, each belonging to a different project:
				createObject(ctx, "my-a-project", "object-a")
				createObject(ctx, "my-b-project", "object-b")

				// Filter by project and verify that we only get the object with the requested project:
				response, err := objectsDao.List().
					SetFilter("this.metadata.project.contains('-a-')").
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				objects := response.GetItems()
				Expect(objects).To(HaveLen(1))
				object := objects[0]
				Expect(object.GetMetadata().GetName()).To(Equal("object-a"))
			})
		})

		Describe("Filter descriptor override", func() {
			It("Behaves identically when SetFilterDesc is set explicitly to the object's own descriptor", func() {
				explicitDao, err := NewGenericDAO[*testsv1.Object]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					SetFilterDesc((*testsv1.Object)(nil).ProtoReflect().Descriptor()).
					Build()
				Expect(err).ToNot(HaveOccurred())
				_, err = explicitDao.Create().
					SetObject(
						testsv1.Object_builder{
							Metadata: testsv1.Metadata_builder{
								Tenant: "my-tenant",
								Name:   "my-object",
							}.Build(),
							MyBool: true,
						}.Build(),
					).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				response, err := explicitDao.List().
					SetFilter("this.my_bool == true").
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetItems()).To(HaveLen(1))
			})

			It("Rejects a filter referencing a field absent from the overridden descriptor", func() {
				restrictedDao, err := NewGenericDAO[*testsv1.Object]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					SetFilterDesc((*testsv1.Status)(nil).ProtoReflect().Descriptor()).
					Build()
				Expect(err).ToNot(HaveOccurred())
				_, err = restrictedDao.List().
					SetFilter("this.my_bool == true").
					Do(ctx)
				Expect(err).To(HaveOccurred())
				var invalidFilterErr *ErrInvalidFilter
				Expect(errors.As(err, &invalidFilterErr)).To(BeTrue())
			})
		})
	})

	Describe("Project filtering", func() {
		It("Filters by name", func() {
			// Create two projects with different names:
			createProject(ctx, "my-tenant", "project-a")
			createProject(ctx, "my-tenant", "project-b")

			// Filter by name and verify that we only get the project with the requested name:
			response, err := projectsDao.List().
				SetFilter("this.metadata.name == 'project-a'").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			projects := response.GetItems()
			Expect(projects).To(HaveLen(1))
			project := projects[0]
			Expect(project.GetMetadata().GetName()).To(Equal("project-a"))
		})

		It("Filters by name prefix", func() {
			// Create two projects with different names:
			createProject(ctx, "my-tenant", "a-project")
			createProject(ctx, "my-tenant", "b-project")

			// Filter by name and verify that we only get the project with the requested name:
			response, err := projectsDao.List().
				SetFilter("this.metadata.name.startsWith('a-')").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			projects := response.GetItems()
			Expect(projects).To(HaveLen(1))
			project := projects[0]
			Expect(project.GetMetadata().GetName()).To(Equal("a-project"))
		})

		It("Filters by name suffix", func() {
			// Create two projects with different names:
			createProject(ctx, "my-tenant", "project-a")
			createProject(ctx, "my-tenant", "project-b")

			// Filter by name and verify that we only get the project with the requested name:
			response, err := projectsDao.List().
				SetFilter("this.metadata.name.endsWith('-a')").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			projects := response.GetItems()
			Expect(projects).To(HaveLen(1))
			project := projects[0]
			Expect(project.GetMetadata().GetName()).To(Equal("project-a"))
		})

		It("Filters by name contains", func() {
			// Create two projects with different names:
			createProject(ctx, "my-tenant", "my-a-project")
			createProject(ctx, "my-tenant", "my-b-project")

			// Filter by name and verify that we only get the project with the requested name:
			response, err := projectsDao.List().
				SetFilter("this.metadata.name.contains('a')").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			projects := response.GetItems()
			Expect(projects).To(HaveLen(1))
			project := projects[0]
			Expect(project.GetMetadata().GetName()).To(Equal("my-a-project"))
		})
	})
})
