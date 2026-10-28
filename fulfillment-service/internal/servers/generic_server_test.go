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
	grpcmetadata "google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Generic server", func() {
	It("preserves reserved metadata from the locked object on public updates", func() {
		server, err := NewGenericServer[*privatev1.BareMetalInstanceTemplate]().
			SetLogger(logger).
			SetService(privatev1.BareMetalInstanceTemplates_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		createResponse := &privatev1.BareMetalInstanceTemplatesCreateResponse{}
		err = server.Create(ctx, privatev1.BareMetalInstanceTemplatesCreateRequest_builder{
			Object: privatev1.BareMetalInstanceTemplate_builder{
				Id: "public_reserved_metadata_update",
				Metadata: privatev1.Metadata_builder{
					Name: "public-reserved-metadata-update",
					Labels: map[string]string{
						"admin.osac.openshift.io/managed": "system",
						"ordinary":                        "before",
					},
					Annotations: map[string]string{
						"provider.admin.osac.openshift.io/id": "provider",
						"ordinary":                            "before",
					},
				}.Build(),
				Description: "before",
			}.Build(),
		}.Build(), &createResponse)
		Expect(err).ToNot(HaveOccurred())

		updateResponse := &privatev1.BareMetalInstanceTemplatesUpdateResponse{}
		err = server.UpdateWithCandidatePreparation(withPublicAPIView(ctx), privatev1.BareMetalInstanceTemplatesUpdateRequest_builder{
			Object: privatev1.BareMetalInstanceTemplate_builder{
				Id: createResponse.GetObject().GetId(),
				Metadata: privatev1.Metadata_builder{
					Name:   createResponse.GetObject().GetMetadata().GetName(),
					Labels: map[string]string{"ordinary": "after"},
				}.Build(),
				Description: "after",
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.labels", "description"}},
		}.Build(), &updateResponse, func(_ context.Context, current, candidate *privatev1.BareMetalInstanceTemplate) error {
			Expect(current.GetMetadata().GetLabels()).To(HaveKeyWithValue("admin.osac.openshift.io/managed", "system"))
			Expect(current.GetMetadata().GetAnnotations()).To(HaveKeyWithValue("provider.admin.osac.openshift.io/id", "provider"))
			candidate.GetMetadata().GetLabels()["admin.osac.openshift.io/managed"] = "callback-value"
			delete(candidate.GetMetadata().GetAnnotations(), "provider.admin.osac.openshift.io/id")
			return nil
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(updateResponse.GetObject().GetMetadata().GetLabels()).To(Equal(map[string]string{
			"admin.osac.openshift.io/managed": "system",
			"ordinary":                        "after",
		}))
		Expect(updateResponse.GetObject().GetMetadata().GetAnnotations()).To(Equal(map[string]string{
			"provider.admin.osac.openshift.io/id": "provider",
			"ordinary":                            "before",
		}))

		clearResponse := &privatev1.BareMetalInstanceTemplatesUpdateResponse{}
		err = server.Update(withPublicAPIView(ctx), privatev1.BareMetalInstanceTemplatesUpdateRequest_builder{
			Object: privatev1.BareMetalInstanceTemplate_builder{
				Id: createResponse.GetObject().GetId(),
				Metadata: privatev1.Metadata_builder{
					Labels: map[string]string{},
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.labels"}},
		}.Build(), &clearResponse)
		Expect(err).ToNot(HaveOccurred())
		Expect(clearResponse.GetObject().GetMetadata().GetLabels()).To(Equal(map[string]string{
			"admin.osac.openshift.io/managed": "system",
		}))

		privateUpdateResponse := &privatev1.BareMetalInstanceTemplatesUpdateResponse{}
		err = server.Update(ctx, privatev1.BareMetalInstanceTemplatesUpdateRequest_builder{
			Object: privatev1.BareMetalInstanceTemplate_builder{
				Id: createResponse.GetObject().GetId(),
				Metadata: privatev1.Metadata_builder{
					Name:   createResponse.GetObject().GetMetadata().GetName(),
					Labels: map[string]string{"ordinary": "private"},
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.labels"}},
		}.Build(), &privateUpdateResponse)
		Expect(err).ToNot(HaveOccurred())
		Expect(privateUpdateResponse.GetObject().GetMetadata().GetLabels()).To(Equal(map[string]string{"ordinary": "private"}))
	})

	It("rejects a stale version when public update locking is requested", func() {
		server, err := NewGenericServer[*privatev1.BareMetalInstanceTemplate]().
			SetLogger(logger).
			SetService(privatev1.BareMetalInstanceTemplates_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		createResponse := &privatev1.BareMetalInstanceTemplatesCreateResponse{}
		err = server.Create(ctx, privatev1.BareMetalInstanceTemplatesCreateRequest_builder{
			Object: privatev1.BareMetalInstanceTemplate_builder{
				Id:          "public_stale_lock",
				Metadata:    privatev1.Metadata_builder{Name: "public-stale-lock"}.Build(),
				Description: "before",
			}.Build(),
		}.Build(), &createResponse)
		Expect(err).ToNot(HaveOccurred())
		stale := proto.Clone(createResponse.GetObject()).(*privatev1.BareMetalInstanceTemplate)

		updated := &privatev1.BareMetalInstanceTemplatesUpdateResponse{}
		err = server.Update(withPublicAPIView(ctx), privatev1.BareMetalInstanceTemplatesUpdateRequest_builder{
			Object: privatev1.BareMetalInstanceTemplate_builder{
				Id:          createResponse.GetObject().GetId(),
				Description: "after",
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"description"}},
		}.Build(), &updated)
		Expect(err).ToNot(HaveOccurred())
		Expect(updated.GetObject().GetMetadata().GetVersion()).To(BeNumerically(">", stale.GetMetadata().GetVersion()))

		stale.SetDescription("stale update")
		var staleResponse privatev1.BareMetalInstanceTemplatesUpdateResponse
		err = server.Update(withPublicAPIView(ctx), privatev1.BareMetalInstanceTemplatesUpdateRequest_builder{
			Object:     stale,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"description"}},
			Lock:       true,
		}.Build(), &staleResponse)
		Expect(status.Code(err)).To(Equal(codes.Aborted))
	})

	It("preserves the latest reserved metadata after waiting for a concurrent update", func() {
		server, err := NewGenericServer[*privatev1.BareMetalInstanceTemplate]().
			SetLogger(logger).
			SetService(privatev1.BareMetalInstanceTemplates_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		createResponse := &privatev1.BareMetalInstanceTemplatesCreateResponse{}
		err = server.Create(ctx, privatev1.BareMetalInstanceTemplatesCreateRequest_builder{
			Object: privatev1.BareMetalInstanceTemplate_builder{
				Id: "concurrent_reserved_metadata",
				Metadata: privatev1.Metadata_builder{
					Name: "concurrent-reserved-metadata",
					Labels: map[string]string{
						"admin.osac.openshift.io/managed":         "original",
						"operator.admin.osac.openshift.io/remove": "remove-me",
						"ordinary": "original",
					},
					Annotations: map[string]string{
						"admin.osac.openshift.io/provider": "original",
						"ordinary":                         "original",
					},
				}.Build(),
				Description: "retained through the public update",
			}.Build(),
		}.Build(), &createResponse)
		Expect(err).ToNot(HaveOccurred())

		// Commit the fixture so two independent database transactions can update it.
		Expect(suiteTx.End(ctx)).To(Succeed())
		suiteTx = nil

		var publicTx database.Tx
		var updateDone chan error
		writerTx, err := tm.Begin(context.Background())
		Expect(err).ToNot(HaveOccurred())
		writerCtx := database.TxIntoContext(context.Background(), writerTx)
		DeferCleanup(func() {
			if writerTx != nil {
				_ = writerTx.End(context.Background())
				writerTx = nil
			}
			if updateDone != nil {
				select {
				case <-updateDone:
				case <-time.After(10 * time.Second):
				}
			}
			if publicTx != nil {
				_ = publicTx.End(context.Background())
				publicTx = nil
			}
		})
		writerResponse := &privatev1.BareMetalInstanceTemplatesUpdateResponse{}
		err = server.Update(writerCtx, privatev1.BareMetalInstanceTemplatesUpdateRequest_builder{
			Object: privatev1.BareMetalInstanceTemplate_builder{
				Id: createResponse.GetObject().GetId(),
				Metadata: privatev1.Metadata_builder{
					Labels: map[string]string{
						"admin.osac.openshift.io/managed":        "updated-by-writer",
						"operator.admin.osac.openshift.io/added": "added-by-writer",
						"ordinary":                               "updated-by-writer",
					},
					Annotations: map[string]string{
						"admin.osac.openshift.io/provider": "updated-by-writer",
					},
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.labels", "metadata.annotations"}},
		}.Build(), &writerResponse)
		Expect(err).ToNot(HaveOccurred())

		publicTx, err = tm.Begin(context.Background())
		Expect(err).ToNot(HaveOccurred())
		publicCtx, cancel := context.WithTimeout(
			database.TxIntoContext(context.Background(), publicTx),
			10*time.Second,
		)
		defer cancel()
		publicResponse := &privatev1.BareMetalInstanceTemplatesUpdateResponse{}
		updateDone = make(chan error, 1)
		go func() {
			updateDone <- server.Update(withPublicAPIView(publicCtx), privatev1.BareMetalInstanceTemplatesUpdateRequest_builder{
				Object: privatev1.BareMetalInstanceTemplate_builder{
					Id: createResponse.GetObject().GetId(),
					Metadata: privatev1.Metadata_builder{
						Labels:      map[string]string{"ordinary": "public-update"},
						Annotations: map[string]string{"ordinary": "public-update"},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.labels", "metadata.annotations"}},
			}.Build(), &publicResponse)
		}()

		// Wait until PostgreSQL reports that the public update is blocked by writerTx.
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
					  and query ilike '%bare_metal_instance_templates%'
				)
			`).Scan(&waiting)
			return waiting, err
		}, 5*time.Second, 10*time.Millisecond).Should(BeTrue())
		Expect(writerTx.End(context.Background())).To(Succeed())
		writerTx = nil

		updateErr := <-updateDone
		updateDone = nil
		Expect(updateErr).To(Succeed())
		Expect(publicTx.End(publicCtx)).To(Succeed())
		publicTx = nil
		expected := proto.Clone(writerResponse.GetObject()).(*privatev1.BareMetalInstanceTemplate)
		expected.GetMetadata().SetVersion(2)
		expected.GetMetadata().SetLabels(map[string]string{
			"admin.osac.openshift.io/managed":        "updated-by-writer",
			"operator.admin.osac.openshift.io/added": "added-by-writer",
			"ordinary":                               "public-update",
		})
		expected.GetMetadata().SetAnnotations(map[string]string{
			"admin.osac.openshift.io/provider": "updated-by-writer",
			"ordinary":                         "public-update",
		})
		Expect(proto.Equal(publicResponse.GetObject(), expected)).To(BeTrue())
	})

	It("signals objects through a flagged no-op update", func() {
		server, err := NewGenericServer[*privatev1.BareMetalInstanceTemplate]().
			SetLogger(logger).
			SetService(privatev1.BareMetalInstanceTemplates_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		createResponse := &privatev1.BareMetalInstanceTemplatesCreateResponse{}
		err = server.Create(ctx, privatev1.BareMetalInstanceTemplatesCreateRequest_builder{
			Object: privatev1.BareMetalInstanceTemplate_builder{
				Metadata:    privatev1.Metadata_builder{Name: "signal-event"}.Build(),
				Description: "sensitive",
			}.Build(),
		}.Build(), &createResponse)
		Expect(err).ToNot(HaveOccurred())

		signalResponse := &privatev1.BareMetalInstanceTemplatesSignalResponse{}
		err = server.Signal(ctx, privatev1.BareMetalInstanceTemplatesSignalRequest_builder{
			Id: createResponse.GetObject().GetId(),
		}.Build(), &signalResponse)
		Expect(err).ToNot(HaveOccurred())

		tx, err := database.TxFromContext(ctx)
		Expect(err).ToNot(HaveOccurred())
		var table, id string
		var version int32
		err = tx.QueryRow(ctx, `
			select "table", data->>'id', (data->>'version')::integer
			from changes
			where op = 'SIGNAL'
		`).Scan(&table, &id, &version)
		Expect(err).ToNot(HaveOccurred())
		Expect(table).To(Equal("bare_metal_instance_templates"))
		Expect(id).To(Equal(createResponse.GetObject().GetId()))
		Expect(version).To(Equal(int32(0)))

		getResponse := &privatev1.BareMetalInstanceTemplatesGetResponse{}
		err = server.Get(ctx, privatev1.BareMetalInstanceTemplatesGetRequest_builder{
			Id: createResponse.GetObject().GetId(),
		}.Build(), &getResponse)
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetMetadata().GetVersion()).To(Equal(int32(0)))
		Expect(getResponse.GetObject().GetDescription()).To(Equal("sensitive"))
	})

	It("clones the prepared create candidate and invokes the callback before persistence", func() {
		var order []string
		server, err := NewGenericServer[*privatev1.BareMetalInstanceTemplate]().
			SetLogger(logger).
			SetService(privatev1.BareMetalInstanceTemplates_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		requestObject := privatev1.BareMetalInstanceTemplate_builder{
			Id:          "callback_create",
			Metadata:    privatev1.Metadata_builder{Name: "callback-create"}.Build(),
			Description: "request-description",
		}.Build()
		response := &privatev1.BareMetalInstanceTemplatesCreateResponse{}
		err = server.CreateWithCandidatePreparation(
			ctx,
			privatev1.BareMetalInstanceTemplatesCreateRequest_builder{Object: requestObject}.Build(),
			&response,
			func(_ context.Context, current *privatev1.BareMetalInstanceTemplate, candidate *privatev1.BareMetalInstanceTemplate) error {
				order = append(order, "callback")
				Expect(current).To(BeNil())
				Expect(candidate).ToNot(BeIdenticalTo(requestObject))
				Expect(candidate.GetMetadata()).ToNot(BeIdenticalTo(requestObject.GetMetadata()))
				Expect(candidate.GetMetadata().GetCreator()).To(Equal("system"))
				Expect(candidate.GetMetadata().GetTenant()).To(Equal(testTenant))
				candidate.SetDescription("callback-description")
				return nil
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(order).To(Equal([]string{"callback"}))
		Expect(requestObject.GetDescription()).To(Equal("request-description"))
		Expect(requestObject.GetMetadata().GetCreator()).To(BeEmpty())
		Expect(requestObject.GetMetadata().GetTenant()).To(BeEmpty())
		Expect(response.GetObject().GetDescription()).To(Equal("callback-description"))
	})

	It("rejects a create callback that clears metadata before persistence", func() {
		server, err := NewGenericServer[*privatev1.BareMetalInstanceTemplate]().
			SetLogger(logger).
			SetService(privatev1.BareMetalInstanceTemplates_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		response := &privatev1.BareMetalInstanceTemplatesCreateResponse{}
		err = server.CreateWithCandidatePreparation(ctx, privatev1.BareMetalInstanceTemplatesCreateRequest_builder{
			Object: privatev1.BareMetalInstanceTemplate_builder{Metadata: privatev1.Metadata_builder{Name: "create-metadata-clear"}.Build()}.Build(),
		}.Build(), &response,
			func(_ context.Context, _ *privatev1.BareMetalInstanceTemplate, candidate *privatev1.BareMetalInstanceTemplate) error {
				candidate.SetMetadata(nil)
				return nil
			},
		)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))

		listResponse := &privatev1.BareMetalInstanceTemplatesListResponse{}
		err = server.List(ctx, privatev1.BareMetalInstanceTemplatesListRequest_builder{}.Build(), &listResponse)
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse.GetTotal()).To(Equal(int32(0)))
	})

	It("rejects a create callback that sets a disallowed tenant before persistence", func() {
		server, err := NewGenericServer[*privatev1.BareMetalInstanceTemplate]().
			SetLogger(logger).
			SetService(privatev1.BareMetalInstanceTemplates_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		response := &privatev1.BareMetalInstanceTemplatesCreateResponse{}
		err = server.CreateWithCandidatePreparation(ctx, privatev1.BareMetalInstanceTemplatesCreateRequest_builder{
			Object: privatev1.BareMetalInstanceTemplate_builder{Metadata: privatev1.Metadata_builder{Name: "create-disallowed-tenant"}.Build()}.Build(),
		}.Build(), &response,
			func(_ context.Context, _ *privatev1.BareMetalInstanceTemplate, candidate *privatev1.BareMetalInstanceTemplate) error {
				candidate.GetMetadata().SetTenant(auth.SharedTenant)
				return nil
			},
		)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.PermissionDenied))

		listResponse := &privatev1.BareMetalInstanceTemplatesListResponse{}
		err = server.List(ctx, privatev1.BareMetalInstanceTemplatesListRequest_builder{}.Build(), &listResponse)
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse.GetTotal()).To(Equal(int32(0)))
	})

	It("rejects a create callback that changes the assigned ID", func() {
		server, err := NewGenericServer[*privatev1.BareMetalInstanceTemplate]().
			SetLogger(logger).
			SetService(privatev1.BareMetalInstanceTemplates_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		response := &privatev1.BareMetalInstanceTemplatesCreateResponse{}
		err = server.CreateWithCandidatePreparation(ctx, privatev1.BareMetalInstanceTemplatesCreateRequest_builder{
			Object: privatev1.BareMetalInstanceTemplate_builder{Metadata: privatev1.Metadata_builder{Name: "create-id-change"}.Build()}.Build(),
		}.Build(), &response,
			func(_ context.Context, _ *privatev1.BareMetalInstanceTemplate, candidate *privatev1.BareMetalInstanceTemplate) error {
				candidate.SetId("replacement-id")
				return nil
			},
		)
		Expect(status.Code(err)).To(Equal(codes.PermissionDenied))

		listResponse := &privatev1.BareMetalInstanceTemplatesListResponse{}
		err = server.List(ctx, privatev1.BareMetalInstanceTemplatesListRequest_builder{}.Build(), &listResponse)
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse.GetTotal()).To(Equal(int32(0)))
	})

	It("applies masked clears to a detached update candidate before the callback", func() {
		server, err := NewGenericServer[*privatev1.BareMetalInstanceTemplate]().
			SetLogger(logger).
			SetService(privatev1.BareMetalInstanceTemplates_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		createResponse := &privatev1.BareMetalInstanceTemplatesCreateResponse{}
		err = server.Create(ctx, privatev1.BareMetalInstanceTemplatesCreateRequest_builder{
			Object: privatev1.BareMetalInstanceTemplate_builder{
				Id:          "callback_update",
				Metadata:    privatev1.Metadata_builder{Name: "callback-update"}.Build(),
				Description: "original-description",
			}.Build(),
		}.Build(), &createResponse)
		Expect(err).ToNot(HaveOccurred())
		created := createResponse.GetObject()
		updateObject := privatev1.BareMetalInstanceTemplate_builder{
			Id:       created.GetId(),
			Metadata: privatev1.Metadata_builder{Name: created.GetMetadata().GetName()}.Build(),
		}.Build()
		var currentSeen, candidateSeen *privatev1.BareMetalInstanceTemplate
		updateResponse := &privatev1.BareMetalInstanceTemplatesUpdateResponse{}
		err = server.UpdateWithCandidatePreparation(ctx, privatev1.BareMetalInstanceTemplatesUpdateRequest_builder{
			Object:     updateObject,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"description"}},
		}.Build(), &updateResponse,
			func(_ context.Context, current *privatev1.BareMetalInstanceTemplate, candidate *privatev1.BareMetalInstanceTemplate) error {
				currentSeen = current
				candidateSeen = candidate
				Expect(current.GetDescription()).To(Equal("original-description"))
				Expect(candidate.GetDescription()).To(BeEmpty())
				return nil
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(currentSeen).ToNot(BeIdenticalTo(candidateSeen))
		Expect(currentSeen).ToNot(BeIdenticalTo(created))
		Expect(candidateSeen).ToNot(BeIdenticalTo(updateObject))
		Expect(updateResponse.GetObject().GetDescription()).To(BeEmpty())
	})

	It("does not alias a no-mask update request after persistence", func() {
		server, err := NewGenericServer[*privatev1.BareMetalInstanceTemplate]().
			SetLogger(logger).
			SetService(privatev1.BareMetalInstanceTemplates_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		createResponse := &privatev1.BareMetalInstanceTemplatesCreateResponse{}
		err = server.Create(ctx, privatev1.BareMetalInstanceTemplatesCreateRequest_builder{
			Object: privatev1.BareMetalInstanceTemplate_builder{
				Id:          "no_mask_alias",
				Metadata:    privatev1.Metadata_builder{Name: "no-mask-alias"}.Build(),
				Description: "original-description",
			}.Build(),
		}.Build(), &createResponse)
		Expect(err).ToNot(HaveOccurred())
		created := createResponse.GetObject()
		updateObject := privatev1.BareMetalInstanceTemplate_builder{
			Id: created.GetId(),
			Metadata: privatev1.Metadata_builder{
				Name:   created.GetMetadata().GetName(),
				Labels: map[string]string{"phase": "updated"},
			}.Build(),
			Description: "updated-description",
		}.Build()
		updateResponse := &privatev1.BareMetalInstanceTemplatesUpdateResponse{}
		err = server.Update(ctx, privatev1.BareMetalInstanceTemplatesUpdateRequest_builder{Object: updateObject}.Build(), &updateResponse)
		Expect(err).ToNot(HaveOccurred())

		updateObject.SetDescription("mutated-after-update")
		updateObject.GetMetadata().GetLabels()["phase"] = "mutated-after-update"
		getResponse := &privatev1.BareMetalInstanceTemplatesGetResponse{}
		err = server.Get(ctx, privatev1.BareMetalInstanceTemplatesGetRequest_builder{Id: created.GetId()}.Build(), &getResponse)
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetDescription()).To(Equal("updated-description"))
		Expect(getResponse.GetObject().GetMetadata().GetLabels()).To(Equal(map[string]string{"phase": "updated"}))
	})

	It("does not persist when a create callback fails", func() {
		server, err := NewGenericServer[*privatev1.BareMetalInstanceTemplate]().
			SetLogger(logger).
			SetService(privatev1.BareMetalInstanceTemplates_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		response := &privatev1.BareMetalInstanceTemplatesCreateResponse{}
		err = server.CreateWithCandidatePreparation(ctx, privatev1.BareMetalInstanceTemplatesCreateRequest_builder{
			Object: privatev1.BareMetalInstanceTemplate_builder{Metadata: privatev1.Metadata_builder{Name: "create-callback-failure"}.Build()}.Build(),
		}.Build(), &response,
			func(context.Context, *privatev1.BareMetalInstanceTemplate, *privatev1.BareMetalInstanceTemplate) error {
				return status.Error(codes.InvalidArgument, "callback failed")
			},
		)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		listResponse := &privatev1.BareMetalInstanceTemplatesListResponse{}
		err = server.List(ctx, privatev1.BareMetalInstanceTemplatesListRequest_builder{}.Build(), &listResponse)
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse.GetTotal()).To(Equal(int32(0)))
	})

	It("does not persist a failed update callback and leaves the stored object unchanged", func() {
		server, err := NewGenericServer[*privatev1.BareMetalInstanceTemplate]().
			SetLogger(logger).
			SetService(privatev1.BareMetalInstanceTemplates_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		createResponse := &privatev1.BareMetalInstanceTemplatesCreateResponse{}
		err = server.Create(ctx, privatev1.BareMetalInstanceTemplatesCreateRequest_builder{
			Object: privatev1.BareMetalInstanceTemplate_builder{
				Metadata:    privatev1.Metadata_builder{Name: "update-callback-failure"}.Build(),
				Description: "original-description",
			}.Build(),
		}.Build(), &createResponse)
		Expect(err).ToNot(HaveOccurred())
		created := createResponse.GetObject()
		updateResponse := &privatev1.BareMetalInstanceTemplatesUpdateResponse{}
		err = server.UpdateWithCandidatePreparation(ctx, privatev1.BareMetalInstanceTemplatesUpdateRequest_builder{
			Object: privatev1.BareMetalInstanceTemplate_builder{
				Id:          created.GetId(),
				Metadata:    privatev1.Metadata_builder{Name: created.GetMetadata().GetName()}.Build(),
				Description: "attempted-description",
			}.Build(),
		}.Build(), &updateResponse,
			func(context.Context, *privatev1.BareMetalInstanceTemplate, *privatev1.BareMetalInstanceTemplate) error {
				return status.Error(codes.InvalidArgument, "callback failed")
			},
		)
		Expect(err).To(HaveOccurred())
		getResponse := &privatev1.BareMetalInstanceTemplatesGetResponse{}
		err = server.Get(ctx, privatev1.BareMetalInstanceTemplatesGetRequest_builder{Id: created.GetId()}.Build(), &getResponse)
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetDescription()).To(Equal("original-description"))
	})

	It("revalidates a callback-mutated update before persistence", func() {
		server, err := NewGenericServer[*privatev1.BareMetalInstanceTemplate]().
			SetLogger(logger).
			SetService(privatev1.BareMetalInstanceTemplates_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		createResponse := &privatev1.BareMetalInstanceTemplatesCreateResponse{}
		err = server.Create(ctx, privatev1.BareMetalInstanceTemplatesCreateRequest_builder{
			Object: privatev1.BareMetalInstanceTemplate_builder{
				Metadata:    privatev1.Metadata_builder{Name: "invalid-callback-update"}.Build(),
				Description: "original-description",
			}.Build(),
		}.Build(), &createResponse)
		Expect(err).ToNot(HaveOccurred())
		created := createResponse.GetObject()
		updateResponse := &privatev1.BareMetalInstanceTemplatesUpdateResponse{}
		err = server.UpdateWithCandidatePreparation(ctx, privatev1.BareMetalInstanceTemplatesUpdateRequest_builder{
			Object: privatev1.BareMetalInstanceTemplate_builder{
				Id:       created.GetId(),
				Metadata: privatev1.Metadata_builder{Name: created.GetMetadata().GetName()}.Build(),
			}.Build(),
		}.Build(), &updateResponse,
			func(_ context.Context, _ *privatev1.BareMetalInstanceTemplate, candidate *privatev1.BareMetalInstanceTemplate) error {
				candidate.SetMetadata(nil)
				return nil
			},
		)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		getResponse := &privatev1.BareMetalInstanceTemplatesGetResponse{}
		err = server.Get(ctx, privatev1.BareMetalInstanceTemplatesGetRequest_builder{Id: created.GetId()}.Build(), &getResponse)
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetDescription()).To(Equal("original-description"))
	})

	It("rejects an update callback that clears metadata", func() {
		server, err := NewGenericServer[*privatev1.BareMetalInstanceTemplate]().
			SetLogger(logger).
			SetService(privatev1.BareMetalInstanceTemplates_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		createResponse := &privatev1.BareMetalInstanceTemplatesCreateResponse{}
		err = server.Create(ctx, privatev1.BareMetalInstanceTemplatesCreateRequest_builder{
			Object: privatev1.BareMetalInstanceTemplate_builder{
				Id:          "update_metadata_clear",
				Metadata:    privatev1.Metadata_builder{Name: "update-metadata-clear"}.Build(),
				Description: "original-description",
			}.Build(),
		}.Build(), &createResponse)
		Expect(err).ToNot(HaveOccurred())
		created := createResponse.GetObject()

		updateResponse := &privatev1.BareMetalInstanceTemplatesUpdateResponse{}
		err = server.UpdateWithCandidatePreparation(ctx, privatev1.BareMetalInstanceTemplatesUpdateRequest_builder{
			Object: privatev1.BareMetalInstanceTemplate_builder{
				Id:          created.GetId(),
				Metadata:    privatev1.Metadata_builder{Name: created.GetMetadata().GetName()}.Build(),
				Description: "attempted-description",
			}.Build(),
		}.Build(), &updateResponse,
			func(_ context.Context, _ *privatev1.BareMetalInstanceTemplate, candidate *privatev1.BareMetalInstanceTemplate) error {
				candidate.SetMetadata(nil)
				return nil
			},
		)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))

		getResponse := &privatev1.BareMetalInstanceTemplatesGetResponse{}
		err = server.Get(ctx, privatev1.BareMetalInstanceTemplatesGetRequest_builder{Id: created.GetId()}.Build(), &getResponse)
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetDescription()).To(Equal("original-description"))
		Expect(getResponse.GetObject().GetMetadata().GetTenant()).To(Equal(testTenant))
	})

	It("rejects an update callback that sets a disallowed tenant", func() {
		server, err := NewGenericServer[*privatev1.BareMetalInstanceTemplate]().
			SetLogger(logger).
			SetService(privatev1.BareMetalInstanceTemplates_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		createResponse := &privatev1.BareMetalInstanceTemplatesCreateResponse{}
		err = server.Create(ctx, privatev1.BareMetalInstanceTemplatesCreateRequest_builder{
			Object: privatev1.BareMetalInstanceTemplate_builder{
				Id:          "update_disallowed_tenant",
				Metadata:    privatev1.Metadata_builder{Name: "update-disallowed-tenant"}.Build(),
				Description: "original-description",
			}.Build(),
		}.Build(), &createResponse)
		Expect(err).ToNot(HaveOccurred())
		created := createResponse.GetObject()

		updateResponse := &privatev1.BareMetalInstanceTemplatesUpdateResponse{}
		err = server.UpdateWithCandidatePreparation(ctx, privatev1.BareMetalInstanceTemplatesUpdateRequest_builder{
			Object: privatev1.BareMetalInstanceTemplate_builder{
				Id:          created.GetId(),
				Metadata:    privatev1.Metadata_builder{Name: created.GetMetadata().GetName()}.Build(),
				Description: "attempted-description",
			}.Build(),
		}.Build(), &updateResponse,
			func(_ context.Context, _ *privatev1.BareMetalInstanceTemplate, candidate *privatev1.BareMetalInstanceTemplate) error {
				candidate.GetMetadata().SetTenant(auth.SharedTenant)
				return nil
			},
		)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.PermissionDenied))

		getResponse := &privatev1.BareMetalInstanceTemplatesGetResponse{}
		err = server.Get(ctx, privatev1.BareMetalInstanceTemplatesGetRequest_builder{Id: created.GetId()}.Build(), &getResponse)
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetDescription()).To(Equal("original-description"))
		Expect(getResponse.GetObject().GetMetadata().GetTenant()).To(Equal(testTenant))
	})

	It("does not add a row when persistence fails", func() {
		server, err := NewGenericServer[*privatev1.BareMetalInstanceTemplate]().
			SetLogger(logger).
			SetService(privatev1.BareMetalInstanceTemplates_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		request := privatev1.BareMetalInstanceTemplatesCreateRequest_builder{
			Object: privatev1.BareMetalInstanceTemplate_builder{Metadata: privatev1.Metadata_builder{Name: "duplicate-create"}.Build()}.Build(),
		}.Build()
		response := &privatev1.BareMetalInstanceTemplatesCreateResponse{}
		err = server.Create(ctx, request, &response)
		Expect(err).ToNot(HaveOccurred())
		tx, err := database.TxFromContext(ctx)
		Expect(err).ToNot(HaveOccurred())
		err = tx.Savepoint(ctx, func(savepointCtx context.Context) error {
			return server.Create(savepointCtx, request, &response)
		})
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.AlreadyExists))
		listResponse := &privatev1.BareMetalInstanceTemplatesListResponse{}
		err = server.List(ctx, privatev1.BareMetalInstanceTemplatesListRequest_builder{}.Build(), &listResponse)
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse.GetTotal()).To(Equal(int32(1)))
	})

	It("runs dry-run callbacks without persisting", func() {
		server, err := NewGenericServer[*privatev1.BareMetalInstanceTemplate]().
			SetLogger(logger).
			SetService(privatev1.BareMetalInstanceTemplates_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		requestObject := privatev1.BareMetalInstanceTemplate_builder{
			Id:          "dry_run_callback",
			Metadata:    privatev1.Metadata_builder{Name: "dry-run-callback"}.Build(),
			Description: "request-description",
		}.Build()
		response := &privatev1.BareMetalInstanceTemplatesCreateResponse{}
		err = server.CreateWithCandidatePreparation(dryRunCtx(), privatev1.BareMetalInstanceTemplatesCreateRequest_builder{Object: requestObject}.Build(), &response,
			func(_ context.Context, current *privatev1.BareMetalInstanceTemplate, candidate *privatev1.BareMetalInstanceTemplate) error {
				Expect(current).To(BeNil())
				candidate.SetDescription("dry-run-description")
				return nil
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetObject().GetDescription()).To(Equal("dry-run-description"))
		Expect(requestObject.GetDescription()).To(Equal("request-description"))
		listResponse := &privatev1.BareMetalInstanceTemplatesListResponse{}
		err = server.List(ctx, privatev1.BareMetalInstanceTemplatesListRequest_builder{}.Build(), &listResponse)
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse.GetTotal()).To(Equal(int32(0)))
	})
})

var _ = Describe("Generic server filtering", func() {
	It("Classifies a filter-translate failure as InvalidArgument, not Internal", func() {
		// Build a server whose filter descriptor is restricted to a message that doesn't have a 'title' field.
		server, err := NewGenericServer[*privatev1.BareMetalInstanceTemplate]().
			SetLogger(logger).
			SetService(privatev1.BareMetalInstanceTemplates_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			SetFilterDesc((*privatev1.Tenant)(nil).ProtoReflect().Descriptor()).
			Build()
		Expect(err).ToNot(HaveOccurred())

		response := &privatev1.BareMetalInstanceTemplatesListResponse{}
		err = server.List(
			ctx,
			privatev1.BareMetalInstanceTemplatesListRequest_builder{
				Filter: new("this.title == 'x'"),
			}.Build(),
			&response,
		)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
	})
})

var _ = Describe("Generic server dry run", func() {
	var server *GenericServer[*privatev1.BareMetalInstanceTemplate]

	BeforeEach(func() {
		var err error
		server, err = NewGenericServer[*privatev1.BareMetalInstanceTemplate]().
			SetLogger(logger).
			SetService(privatev1.BareMetalInstanceTemplates_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	It("Assigns creator and tenant to the object", func() {
		response := &privatev1.BareMetalInstanceTemplatesCreateResponse{}
		err := server.Create(
			dryRunCtx(),
			privatev1.BareMetalInstanceTemplatesCreateRequest_builder{
				Object: privatev1.BareMetalInstanceTemplate_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "my-dry-run-object",
					}.Build(),
				}.Build(),
			}.Build(),
			&response,
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetObject()).ToNot(BeNil())
		Expect(response.GetObject().GetMetadata().GetName()).To(Equal("my-dry-run-object"))
		Expect(response.GetObject().GetMetadata().GetCreator()).To(Equal("system"))
		Expect(response.GetObject().GetMetadata().GetTenant()).To(Equal(testTenant))
	})

	It("Validates metadata and rejects invalid labels", func() {
		response := &privatev1.BareMetalInstanceTemplatesCreateResponse{}
		err := server.Create(
			dryRunCtx(),
			privatev1.BareMetalInstanceTemplatesCreateRequest_builder{
				Object: privatev1.BareMetalInstanceTemplate_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "valid-name",
						Labels: map[string]string{"!!!invalid": "value"},
					}.Build(),
				}.Build(),
			}.Build(),
			&response,
		)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
	})

	It("Does not persist the object", func() {
		response := &privatev1.BareMetalInstanceTemplatesCreateResponse{}
		err := server.Create(
			dryRunCtx(),
			privatev1.BareMetalInstanceTemplatesCreateRequest_builder{
				Object: privatev1.BareMetalInstanceTemplate_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "dry-run-no-persist",
					}.Build(),
				}.Build(),
			}.Build(),
			&response,
		)
		Expect(err).ToNot(HaveOccurred())

		listResponse := &privatev1.BareMetalInstanceTemplatesListResponse{}
		err = server.List(ctx,
			privatev1.BareMetalInstanceTemplatesListRequest_builder{}.Build(),
			&listResponse,
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse.GetTotal()).To(Equal(int32(0)))
	})

	It("Does not persist objects", func() {
		response := &privatev1.BareMetalInstanceTemplatesCreateResponse{}
		err := server.Create(
			dryRunCtx(),
			privatev1.BareMetalInstanceTemplatesCreateRequest_builder{
				Object: privatev1.BareMetalInstanceTemplate_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "dry-run-no-events",
					}.Build(),
				}.Build(),
			}.Build(),
			&response,
		)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Persists normally when header value is false", func() {
		srv, err := NewGenericServer[*privatev1.BareMetalInstanceTemplate]().
			SetLogger(logger).
			SetService(privatev1.BareMetalInstanceTemplates_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		falseCtx := grpcmetadata.NewIncomingContext(ctx,
			grpcmetadata.Pairs(DryRunMetadataKey, "false"))

		response := &privatev1.BareMetalInstanceTemplatesCreateResponse{}
		err = srv.Create(
			falseCtx,
			privatev1.BareMetalInstanceTemplatesCreateRequest_builder{
				Object: privatev1.BareMetalInstanceTemplate_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "not-a-dry-run",
					}.Build(),
				}.Build(),
			}.Build(),
			&response,
		)
		Expect(err).ToNot(HaveOccurred())

		listResponse := &privatev1.BareMetalInstanceTemplatesListResponse{}
		err = srv.List(ctx,
			privatev1.BareMetalInstanceTemplatesListRequest_builder{}.Build(),
			&listResponse,
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse.GetTotal()).To(Equal(int32(1)))
	})

	Describe("Status field protection", func() {
		It("clears status on public API Create but preserves it on private API Create", func() {
			server, err := NewGenericServer[*privatev1.Subnet]().
				SetLogger(logger).
				SetService(privatev1.Subnets_ServiceDesc.ServiceName).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Public API Create with status - should be cleared
			publicObject := privatev1.Subnet_builder{
				Id: "public_create_status_cleared",
				Metadata: privatev1.Metadata_builder{
					Name: "public-create-status-cleared",
				}.Build(),
				Spec: privatev1.SubnetSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: "vnet-1"}.Build(),
				}.Build(),
				Status: privatev1.SubnetStatus_builder{
					State: privatev1.SubnetState_SUBNET_STATE_READY,
					Hub:   "malicious-hub",
				}.Build(),
			}.Build()
			publicObject.GetSpec().SetIpv4Cidr("10.0.0.0/24")

			publicCreateResponse := &privatev1.SubnetsCreateResponse{}
			err = server.Create(withPublicAPIView(ctx), privatev1.SubnetsCreateRequest_builder{
				Object: publicObject,
			}.Build(), &publicCreateResponse)
			Expect(err).ToNot(HaveOccurred())
			Expect(publicCreateResponse.GetObject().GetStatus().GetState()).To(Equal(privatev1.SubnetState_SUBNET_STATE_UNSPECIFIED))
			Expect(publicCreateResponse.GetObject().GetStatus().GetHub()).To(BeEmpty())

			// Private API Create with status - should be preserved (controllers can set status)
			privateObject := privatev1.Subnet_builder{
				Id: "private_create_status_preserved",
				Metadata: privatev1.Metadata_builder{
					Name: "private-create-status-preserved",
				}.Build(),
				Spec: privatev1.SubnetSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: "vnet-2"}.Build(),
				}.Build(),
				Status: privatev1.SubnetStatus_builder{
					State: privatev1.SubnetState_SUBNET_STATE_PENDING,
					Hub:   "controller-hub",
				}.Build(),
			}.Build()
			privateObject.GetSpec().SetIpv4Cidr("10.0.1.0/24")

			privateCreateResponse := &privatev1.SubnetsCreateResponse{}
			err = server.Create(ctx, privatev1.SubnetsCreateRequest_builder{
				Object: privateObject,
			}.Build(), &privateCreateResponse)
			Expect(err).ToNot(HaveOccurred())
			Expect(privateCreateResponse.GetObject().GetStatus().GetState()).To(Equal(privatev1.SubnetState_SUBNET_STATE_PENDING))
			Expect(privateCreateResponse.GetObject().GetStatus().GetHub()).To(Equal("controller-hub"))
		})

		It("restores status on public API Update when status not in mask", func() {
			server, err := NewGenericServer[*privatev1.Subnet]().
				SetLogger(logger).
				SetService(privatev1.Subnets_ServiceDesc.ServiceName).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Create subnet
			createObject := privatev1.Subnet_builder{
				Id: "status_restore_test",
				Metadata: privatev1.Metadata_builder{
					Name: "status-restore-test",
				}.Build(),
				Spec: privatev1.SubnetSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: "vnet-1"}.Build(),
				}.Build(),
			}.Build()
			createObject.GetSpec().SetIpv4Cidr("10.0.0.0/24")

			createResponse := &privatev1.SubnetsCreateResponse{}
			err = server.Create(ctx, privatev1.SubnetsCreateRequest_builder{
				Object: createObject,
			}.Build(), &createResponse)
			Expect(err).ToNot(HaveOccurred())

			// Controller sets status via private API
			controllerUpdateResponse := &privatev1.SubnetsUpdateResponse{}
			err = server.Update(ctx, privatev1.SubnetsUpdateRequest_builder{
				Object: privatev1.Subnet_builder{
					Id: createResponse.GetObject().GetId(),
					Status: privatev1.SubnetStatus_builder{
						State: privatev1.SubnetState_SUBNET_STATE_READY,
						Hub:   "hub-123",
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state", "status.hub"}},
			}.Build(), &controllerUpdateResponse)
			Expect(err).ToNot(HaveOccurred())
			Expect(controllerUpdateResponse.GetObject().GetStatus().GetState()).To(Equal(privatev1.SubnetState_SUBNET_STATE_READY))
			Expect(controllerUpdateResponse.GetObject().GetStatus().GetHub()).To(Equal("hub-123"))

			// User tries to modify spec via public API with spoofed status - status should be restored from DB
			userUpdateObject := privatev1.Subnet_builder{
				Id: createResponse.GetObject().GetId(),
				Spec: privatev1.SubnetSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: "vnet-1"}.Build(),
				}.Build(),
				Status: privatev1.SubnetStatus_builder{
					State: privatev1.SubnetState_SUBNET_STATE_FAILED,
					Hub:   "malicious-hub",
				}.Build(),
			}.Build()
			userUpdateObject.GetSpec().SetIpv4Cidr("10.0.2.0/24")

			userUpdateResponse := &privatev1.SubnetsUpdateResponse{}
			err = server.Update(withPublicAPIView(ctx), privatev1.SubnetsUpdateRequest_builder{
				Object:     userUpdateObject,
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.ipv4_cidr"}},
			}.Build(), &userUpdateResponse)
			Expect(err).ToNot(HaveOccurred())
			// Status should be restored from DB, not spoofed value
			Expect(userUpdateResponse.GetObject().GetStatus().GetState()).To(Equal(privatev1.SubnetState_SUBNET_STATE_READY))
			Expect(userUpdateResponse.GetObject().GetStatus().GetHub()).To(Equal("hub-123"))
		})

		It("rejects public API Update with status in mask", func() {
			server, err := NewGenericServer[*privatev1.Subnet]().
				SetLogger(logger).
				SetService(privatev1.Subnets_ServiceDesc.ServiceName).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Create subnet
			createObject := privatev1.Subnet_builder{
				Id: "status_mask_reject_test",
				Metadata: privatev1.Metadata_builder{
					Name: "status-mask-reject-test",
				}.Build(),
				Spec: privatev1.SubnetSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: "vnet-1"}.Build(),
				}.Build(),
			}.Build()
			createObject.GetSpec().SetIpv4Cidr("10.0.0.0/24")

			createResponse := &privatev1.SubnetsCreateResponse{}
			err = server.Create(ctx, privatev1.SubnetsCreateRequest_builder{
				Object: createObject,
			}.Build(), &createResponse)
			Expect(err).ToNot(HaveOccurred())

			// User tries to update status via public API with explicit mask
			var updateResponse privatev1.SubnetsUpdateResponse
			err = server.Update(withPublicAPIView(ctx), privatev1.SubnetsUpdateRequest_builder{
				Object: privatev1.Subnet_builder{
					Id: createResponse.GetObject().GetId(),
					Status: privatev1.SubnetStatus_builder{
						State: privatev1.SubnetState_SUBNET_STATE_READY,
						Hub:   "user-hub",
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
			}.Build(), &updateResponse)
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("OUTPUT_ONLY"))
		})

		It("rejects public API Update with malformed status paths in mask", func() {
			server, err := NewGenericServer[*privatev1.Subnet]().
				SetLogger(logger).
				SetService(privatev1.Subnets_ServiceDesc.ServiceName).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Create subnet
			createObject := privatev1.Subnet_builder{
				Id: "malformed_path_test",
				Metadata: privatev1.Metadata_builder{
					Name: "malformed-path-test",
				}.Build(),
				Spec: privatev1.SubnetSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: "vnet-1"}.Build(),
				}.Build(),
			}.Build()
			createObject.GetSpec().SetIpv4Cidr("10.0.0.0/24")

			createResponse := &privatev1.SubnetsCreateResponse{}
			err = server.Create(ctx, privatev1.SubnetsCreateRequest_builder{
				Object: createObject,
			}.Build(), &createResponse)
			Expect(err).ToNot(HaveOccurred())

			// Test various malformed paths that should all be rejected
			malformedPaths := []string{
				"status. state",  // space after dot
				"status..state",  // double dots
				" status.state ", // leading/trailing spaces
				"status. hub",    // space after dot
				"status",         // just "status"
				" status ",       // status with spaces
			}

			for _, path := range malformedPaths {
				var updateResponse privatev1.SubnetsUpdateResponse
				err = server.Update(withPublicAPIView(ctx), privatev1.SubnetsUpdateRequest_builder{
					Object: privatev1.Subnet_builder{
						Id: createResponse.GetObject().GetId(),
						Status: privatev1.SubnetStatus_builder{
							State: privatev1.SubnetState_SUBNET_STATE_READY,
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{path}},
				}.Build(), &updateResponse)
				Expect(status.Code(err)).To(Equal(codes.InvalidArgument), "path: %q", path)
				Expect(err.Error()).To(ContainSubstring("OUTPUT_ONLY"), "path: %q", path)
			}
		})

		It("allows private API Update with status in mask", func() {
			server, err := NewGenericServer[*privatev1.Subnet]().
				SetLogger(logger).
				SetService(privatev1.Subnets_ServiceDesc.ServiceName).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Create subnet
			createObject := privatev1.Subnet_builder{
				Id: "private_status_update",
				Metadata: privatev1.Metadata_builder{
					Name: "private-status-update",
				}.Build(),
				Spec: privatev1.SubnetSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: "vnet-1"}.Build(),
				}.Build(),
			}.Build()
			createObject.GetSpec().SetIpv4Cidr("10.0.0.0/24")

			createResponse := &privatev1.SubnetsCreateResponse{}
			err = server.Create(ctx, privatev1.SubnetsCreateRequest_builder{
				Object: createObject,
			}.Build(), &createResponse)
			Expect(err).ToNot(HaveOccurred())

			// Controller updates status via private API - should succeed
			updateResponse := &privatev1.SubnetsUpdateResponse{}
			err = server.Update(ctx, privatev1.SubnetsUpdateRequest_builder{
				Object: privatev1.Subnet_builder{
					Id: createResponse.GetObject().GetId(),
					Status: privatev1.SubnetStatus_builder{
						State: privatev1.SubnetState_SUBNET_STATE_READY,
						Hub:   "hub-456",
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state", "status.hub"}},
			}.Build(), &updateResponse)
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetStatus().GetState()).To(Equal(privatev1.SubnetState_SUBNET_STATE_READY))
			Expect(updateResponse.GetObject().GetStatus().GetHub()).To(Equal("hub-456"))

			// Verify the status persisted
			getResponse := &privatev1.SubnetsGetResponse{}
			err = server.Get(ctx, privatev1.SubnetsGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build(), &getResponse)
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetStatus().GetState()).To(Equal(privatev1.SubnetState_SUBNET_STATE_READY))
			Expect(getResponse.GetObject().GetStatus().GetHub()).To(Equal("hub-456"))
		})
	})
})
