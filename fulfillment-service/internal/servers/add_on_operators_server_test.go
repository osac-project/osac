/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"fmt"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Add-on operators server", func() {
	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewAddOnOperatorsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewAddOnOperatorsServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewAddOnOperatorsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Published filtering", func() {
		var publicServer *AddOnOperatorsServer
		var privateServer *PrivateAddOnOperatorsServer

		BeforeEach(func() {
			var err error

			privateServer, err = NewPrivateAddOnOperatorsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			publicServer, err = NewAddOnOperatorsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("List returns only published operators", func() {
			_, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("published-%s", uuid.New()[24:32]),
					}.Build(),
					Title:     "Published Operator",
					Published: new(true),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("unpublished-%s", uuid.New()[24:32]),
					}.Build(),
					Title: "Unpublished Operator",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			response, err := publicServer.List(ctx, publicv1.AddOnOperatorsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			titles := make([]string, len(response.GetItems()))
			for i, item := range response.GetItems() {
				Expect(item.GetPublished()).To(BeTrue())
				titles[i] = item.GetTitle()
			}
			Expect(titles).To(ContainElement("Published Operator"))
			Expect(titles).ToNot(ContainElement("Unpublished Operator"))
		})

		It("Get returns published operator", func() {
			createResponse, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("published-%s", uuid.New()[24:32]),
					}.Build(),
					Title:     "Published Operator",
					Published: new(true),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			getResponse, err := publicServer.Get(ctx, publicv1.AddOnOperatorsGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetTitle()).To(Equal("Published Operator"))
		})

		It("Get returns NotFound for unpublished operator", func() {
			createResponse, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("unpublished-%s", uuid.New()[24:32]),
					}.Build(),
					Title: "Unpublished Operator",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = publicServer.Get(ctx, publicv1.AddOnOperatorsGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.NotFound))
		})

		It("List with filter composes correctly with published filter", func() {
			_, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("gpu-%s", uuid.New()[24:32]),
					}.Build(),
					Title:     "GPU Operator",
					Published: new(true),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			response, err := publicServer.List(ctx, publicv1.AddOnOperatorsListRequest_builder{
				Filter: new("this.title == 'GPU Operator'"),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).ToNot(BeEmpty())
			for _, item := range response.GetItems() {
				Expect(item.GetTitle()).To(Equal("GPU Operator"))
				Expect(item.GetPublished()).To(BeTrue())
			}
		})
	})

	Describe("Dependency lifecycle", func() {
		var privateServer *PrivateAddOnOperatorsServer

		BeforeEach(func() {
			var err error
			privateServer, err = NewPrivateAddOnOperatorsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("persists canonical direct edges for a published operator", func() {
			dependency, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata:  privatev1.Metadata_builder{Name: "dependency"}.Build(),
					Title:     "dependency",
					Published: new(true),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			dependent, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata:  privatev1.Metadata_builder{Name: "dependent"}.Build(),
					Title:     "dependent",
					Published: new(true),
					Dependencies: []*privatev1.AddOnOperatorLocalReference{
						privatev1.AddOnOperatorLocalReference_builder{Id: dependency.GetObject().GetId()}.Build(),
					},
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			Expect(dependent.GetObject().GetDependencies()).To(HaveLen(1))
			Expect(dependent.GetObject().GetDependencies()[0].GetId()).To(Equal(dependency.GetObject().GetId()))
		})

		It("rejects publication when a dependency is not published", func() {
			dependency, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{Name: "draft-dependency"}.Build(),
					Title:    "draft-dependency",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata:  privatev1.Metadata_builder{Name: "invalid-dependent"}.Build(),
					Title:     "invalid-dependent",
					Published: new(true),
					Dependencies: []*privatev1.AddOnOperatorLocalReference{
						privatev1.AddOnOperatorLocalReference_builder{Id: dependency.GetObject().GetId()}.Build(),
					},
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())

			Expect(suiteTx.End(ctx)).To(Succeed())
			suiteTx = nil
			queryTx, err := tm.Begin(context.Background())
			Expect(err).ToNot(HaveOccurred())
			queryCtx := database.TxIntoContext(context.Background(), queryTx)
			response, err := privateServer.List(queryCtx, privatev1.AddOnOperatorsListRequest_builder{
				Filter: new("this.metadata.name == 'invalid-dependent'"),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(BeEmpty())
			Expect(queryTx.End(queryCtx)).To(Succeed())
		})

		It("does not persist dependency edges for draft operators", func() {
			dependency, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata:  privatev1.Metadata_builder{Name: "published-dependency"}.Build(),
					Title:     "published-dependency",
					Published: new(true),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			draft, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{Name: "draft-dependent"}.Build(),
					Title:    "draft-dependent",
					Dependencies: []*privatev1.AddOnOperatorLocalReference{
						privatev1.AddOnOperatorLocalReference_builder{Id: dependency.GetObject().GetId()}.Build(),
					},
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			Expect(draft.GetObject().GetDependencies()).To(HaveLen(1))
		})

		It("replaces direct edges when published dependencies are updated", func() {
			first, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata:  privatev1.Metadata_builder{Name: "first"}.Build(),
					Title:     "first",
					Published: new(true),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			second, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata:  privatev1.Metadata_builder{Name: "second"}.Build(),
					Title:     "second",
					Published: new(true),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			dependent, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata:  privatev1.Metadata_builder{Name: "updatable"}.Build(),
					Title:     "updatable",
					Published: new(true),
					Dependencies: []*privatev1.AddOnOperatorLocalReference{
						privatev1.AddOnOperatorLocalReference_builder{Id: first.GetObject().GetId()}.Build(),
					},
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			updated, err := privateServer.Update(ctx, privatev1.AddOnOperatorsUpdateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Id: dependent.GetObject().GetId(),
					Dependencies: []*privatev1.AddOnOperatorLocalReference{
						privatev1.AddOnOperatorLocalReference_builder{Id: second.GetObject().GetId()}.Build(),
					},
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"dependencies"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updated.GetObject().GetDependencies()).To(HaveLen(1))
			Expect(updated.GetObject().GetDependencies()[0].GetId()).To(Equal(second.GetObject().GetId()))
		})

		It("rejects unpublishing an operator with a published dependent", func() {
			dependency, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata:  privatev1.Metadata_builder{Name: "unpublish-dependency"}.Build(),
					Title:     "unpublish-dependency",
					Published: new(true),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			_, err = privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata:  privatev1.Metadata_builder{Name: "unpublish-dependent"}.Build(),
					Title:     "unpublish-dependent",
					Published: new(true),
					Dependencies: []*privatev1.AddOnOperatorLocalReference{
						privatev1.AddOnOperatorLocalReference_builder{Id: dependency.GetObject().GetId()}.Build(),
					},
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = privateServer.Update(ctx, privatev1.AddOnOperatorsUpdateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Id:        dependency.GetObject().GetId(),
					Published: new(false),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"published"}},
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring("published dependents"))
		})

		It("rejects dry-run unpublishing without changing the operator", func() {
			dependency, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata:  privatev1.Metadata_builder{Name: "dry-run-unpublish-dependency"}.Build(),
					Title:     "dry-run-unpublish-dependency",
					Published: new(true),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			_, err = privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata:  privatev1.Metadata_builder{Name: "dry-run-unpublish-dependent"}.Build(),
					Title:     "dry-run-unpublish-dependent",
					Published: new(true),
					Dependencies: []*privatev1.AddOnOperatorLocalReference{
						privatev1.AddOnOperatorLocalReference_builder{Id: dependency.GetObject().GetId()}.Build(),
					},
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = privateServer.Update(dryRunCtx(), privatev1.AddOnOperatorsUpdateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Id:        dependency.GetObject().GetId(),
					Published: new(false),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"published"}},
			}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.Unimplemented))

			stored, err := privateServer.Get(ctx, privatev1.AddOnOperatorsGetRequest_builder{
				Id: dependency.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(stored.GetObject().GetPublished()).To(BeTrue())
		})

		It("allows unpublishing an operator without published dependents", func() {
			leaf, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata:  privatev1.Metadata_builder{Name: "unpublish-leaf"}.Build(),
					Title:     "unpublish-leaf",
					Published: new(true),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			root, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata:  privatev1.Metadata_builder{Name: "unpublish-root"}.Build(),
					Title:     "unpublish-root",
					Published: new(true),
					Dependencies: []*privatev1.AddOnOperatorLocalReference{
						privatev1.AddOnOperatorLocalReference_builder{Id: leaf.GetObject().GetId()}.Build(),
					},
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = privateServer.Update(ctx, privatev1.AddOnOperatorsUpdateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Id:        root.GetObject().GetId(),
					Published: new(false),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"published"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())

		})

		It("rejects deleting an operator with a published dependent", func() {
			dependency, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata:  privatev1.Metadata_builder{Name: "delete-dependency"}.Build(),
					Title:     "delete-dependency",
					Published: new(true),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			_, err = privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata:  privatev1.Metadata_builder{Name: "delete-dependent"}.Build(),
					Title:     "delete-dependent",
					Published: new(true),
					Dependencies: []*privatev1.AddOnOperatorLocalReference{
						privatev1.AddOnOperatorLocalReference_builder{Id: dependency.GetObject().GetId()}.Build(),
					},
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = privateServer.Delete(ctx, privatev1.AddOnOperatorsDeleteRequest_builder{
				Id: dependency.GetObject().GetId(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring("published dependents"))
		})

		It("rejects dry-run deleting without removing the operator", func() {
			dependency, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata:  privatev1.Metadata_builder{Name: "dry-run-delete-dependency"}.Build(),
					Title:     "dry-run-delete-dependency",
					Published: new(true),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			_, err = privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata:  privatev1.Metadata_builder{Name: "dry-run-delete-dependent"}.Build(),
					Title:     "dry-run-delete-dependent",
					Published: new(true),
					Dependencies: []*privatev1.AddOnOperatorLocalReference{
						privatev1.AddOnOperatorLocalReference_builder{Id: dependency.GetObject().GetId()}.Build(),
					},
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = privateServer.Delete(dryRunCtx(), privatev1.AddOnOperatorsDeleteRequest_builder{
				Id: dependency.GetObject().GetId(),
			}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.Unimplemented))

			stored, err := privateServer.Get(ctx, privatev1.AddOnOperatorsGetRequest_builder{
				Id: dependency.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(stored.GetObject().GetId()).To(Equal(dependency.GetObject().GetId()))
		})

		It("allows deleting an operator without published dependents", func() {
			dependency, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata:  privatev1.Metadata_builder{Name: "delete-leaf"}.Build(),
					Title:     "delete-leaf",
					Published: new(true),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			root, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata:  privatev1.Metadata_builder{Name: "delete-root"}.Build(),
					Title:     "delete-root",
					Published: new(true),
					Dependencies: []*privatev1.AddOnOperatorLocalReference{
						privatev1.AddOnOperatorLocalReference_builder{Id: dependency.GetObject().GetId()}.Build(),
					},
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = privateServer.Delete(ctx, privatev1.AddOnOperatorsDeleteRequest_builder{
				Id: root.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

		})

		It("rolls back an invalid published dependency update", func() {
			valid, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata:  privatev1.Metadata_builder{Name: "valid"}.Build(),
					Title:     "valid",
					Published: new(true),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			draft, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{Name: "unpublished"}.Build(),
					Title:    "unpublished",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			dependent, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata:  privatev1.Metadata_builder{Name: "rollback"}.Build(),
					Title:     "rollback",
					Published: new(true),
					Dependencies: []*privatev1.AddOnOperatorLocalReference{
						privatev1.AddOnOperatorLocalReference_builder{Id: valid.GetObject().GetId()}.Build(),
					},
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			Expect(suiteTx.End(ctx)).To(Succeed())
			suiteTx = nil
			updateTx, err := tm.Begin(context.Background())
			Expect(err).ToNot(HaveOccurred())
			updateCtx := database.TxIntoContext(context.Background(), updateTx)
			_, updateErr := privateServer.Update(updateCtx, privatev1.AddOnOperatorsUpdateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Id: dependent.GetObject().GetId(),
					Dependencies: []*privatev1.AddOnOperatorLocalReference{
						privatev1.AddOnOperatorLocalReference_builder{Id: draft.GetObject().GetId()}.Build(),
					},
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"dependencies"}},
			}.Build())
			Expect(updateErr).To(HaveOccurred())
			Expect(updateTx.End(updateCtx)).To(Succeed())

			queryTx, err := tm.Begin(context.Background())
			Expect(err).ToNot(HaveOccurred())
			queryCtx := database.TxIntoContext(context.Background(), queryTx)
			stored, err := privateServer.Get(queryCtx, privatev1.AddOnOperatorsGetRequest_builder{
				Id: dependent.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(stored.GetObject().GetDependencies()).To(HaveLen(1))
			Expect(stored.GetObject().GetDependencies()[0].GetId()).To(Equal(valid.GetObject().GetId()))
			Expect(queryTx.End(queryCtx)).To(Succeed())
		})

		It("serializes concurrent published dependency updates", func() {
			first, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata:  privatev1.Metadata_builder{Name: "concurrent-first"}.Build(),
					Title:     "concurrent-first",
					Published: new(true),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			second, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata:  privatev1.Metadata_builder{Name: "concurrent-second"}.Build(),
					Title:     "concurrent-second",
					Published: new(true),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			dependent, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata:  privatev1.Metadata_builder{Name: "concurrent-dependent"}.Build(),
					Title:     "concurrent-dependent",
					Published: new(true),
					Dependencies: []*privatev1.AddOnOperatorLocalReference{
						privatev1.AddOnOperatorLocalReference_builder{Id: first.GetObject().GetId()}.Build(),
					},
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(suiteTx.End(ctx)).To(Succeed())
			suiteTx = nil

			type updateResult struct{ err error }
			results := make(chan updateResult, 2)
			var group sync.WaitGroup
			for _, dependency := range []string{first.GetObject().GetId(), second.GetObject().GetId()} {
				dependency := dependency
				tx, txErr := tm.Begin(context.Background())
				Expect(txErr).ToNot(HaveOccurred())
				group.Add(1)
				go func(tx database.Tx) {
					defer group.Done()
					updateCtx := database.TxIntoContext(context.Background(), tx)
					_, updateErr := privateServer.Update(updateCtx, privatev1.AddOnOperatorsUpdateRequest_builder{
						Object: privatev1.AddOnOperator_builder{
							Id: dependent.GetObject().GetId(),
							Dependencies: []*privatev1.AddOnOperatorLocalReference{
								privatev1.AddOnOperatorLocalReference_builder{Id: dependency}.Build(),
							},
						}.Build(),
						UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"dependencies"}},
					}.Build())
					endErr := tx.End(updateCtx)
					if updateErr == nil {
						updateErr = endErr
					}
					results <- updateResult{err: updateErr}
				}(tx)
			}
			group.Wait()
			close(results)
			for result := range results {
				Expect(result.err).ToNot(HaveOccurred())
			}

			queryTx, err := tm.Begin(context.Background())
			Expect(err).ToNot(HaveOccurred())
			queryCtx := database.TxIntoContext(context.Background(), queryTx)
			stored, err := privateServer.Get(queryCtx, privatev1.AddOnOperatorsGetRequest_builder{
				Id: dependent.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(stored.GetObject().GetDependencies()).To(HaveLen(1))
			Expect(stored.GetObject().GetDependencies()[0].GetId()).To(BeElementOf(first.GetObject().GetId(), second.GetObject().GetId()))
			Expect(queryTx.End(queryCtx)).To(Succeed())
		})
	})
})
