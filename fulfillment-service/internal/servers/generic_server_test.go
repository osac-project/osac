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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	grpcmetadata "google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Generic server", func() {
	It("signals objects through a flagged no-op update", func() {
		server, err := NewGenericServer[*privatev1.HostType]().
			SetLogger(logger).
			SetService(privatev1.HostTypes_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		createResponse := &privatev1.HostTypesCreateResponse{}
		err = server.Create(ctx, privatev1.HostTypesCreateRequest_builder{
			Object: privatev1.HostType_builder{
				Metadata:    privatev1.Metadata_builder{Name: "signal-event"}.Build(),
				Description: "sensitive",
			}.Build(),
		}.Build(), &createResponse)
		Expect(err).ToNot(HaveOccurred())

		signalResponse := &privatev1.HostTypesSignalResponse{}
		err = server.Signal(ctx, privatev1.HostTypesSignalRequest_builder{
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
		Expect(table).To(Equal("host_types"))
		Expect(id).To(Equal(createResponse.GetObject().GetId()))
		Expect(version).To(Equal(int32(0)))

		getResponse := &privatev1.HostTypesGetResponse{}
		err = server.Get(ctx, privatev1.HostTypesGetRequest_builder{
			Id: createResponse.GetObject().GetId(),
		}.Build(), &getResponse)
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetMetadata().GetVersion()).To(Equal(int32(0)))
		Expect(getResponse.GetObject().GetDescription()).To(Equal("sensitive"))
	})

	It("clones the prepared create candidate and invokes the callback before persistence", func() {
		var order []string
		server, err := NewGenericServer[*privatev1.HostType]().
			SetLogger(logger).
			SetService(privatev1.HostTypes_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		requestObject := privatev1.HostType_builder{
			Metadata:    privatev1.Metadata_builder{Name: "callback-create"}.Build(),
			Description: "request-description",
		}.Build()
		response := &privatev1.HostTypesCreateResponse{}
		err = server.CreateWithCandidatePreparation(
			ctx,
			privatev1.HostTypesCreateRequest_builder{Object: requestObject}.Build(),
			&response,
			func(_ context.Context, current *privatev1.HostType, candidate *privatev1.HostType) error {
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
		server, err := NewGenericServer[*privatev1.HostType]().
			SetLogger(logger).
			SetService(privatev1.HostTypes_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		response := &privatev1.HostTypesCreateResponse{}
		err = server.CreateWithCandidatePreparation(ctx, privatev1.HostTypesCreateRequest_builder{
			Object: privatev1.HostType_builder{Metadata: privatev1.Metadata_builder{Name: "create-metadata-clear"}.Build()}.Build(),
		}.Build(), &response,
			func(_ context.Context, _ *privatev1.HostType, candidate *privatev1.HostType) error {
				candidate.SetMetadata(nil)
				return nil
			},
		)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))

		listResponse := &privatev1.HostTypesListResponse{}
		err = server.List(ctx, privatev1.HostTypesListRequest_builder{}.Build(), &listResponse)
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse.GetTotal()).To(Equal(int32(0)))
	})

	It("rejects a create callback that sets a disallowed tenant before persistence", func() {
		server, err := NewGenericServer[*privatev1.HostType]().
			SetLogger(logger).
			SetService(privatev1.HostTypes_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		response := &privatev1.HostTypesCreateResponse{}
		err = server.CreateWithCandidatePreparation(ctx, privatev1.HostTypesCreateRequest_builder{
			Object: privatev1.HostType_builder{Metadata: privatev1.Metadata_builder{Name: "create-disallowed-tenant"}.Build()}.Build(),
		}.Build(), &response,
			func(_ context.Context, _ *privatev1.HostType, candidate *privatev1.HostType) error {
				candidate.GetMetadata().SetTenant(auth.SharedTenant)
				return nil
			},
		)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.PermissionDenied))

		listResponse := &privatev1.HostTypesListResponse{}
		err = server.List(ctx, privatev1.HostTypesListRequest_builder{}.Build(), &listResponse)
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse.GetTotal()).To(Equal(int32(0)))
	})

	It("rejects a create callback that changes the assigned ID", func() {
		server, err := NewGenericServer[*privatev1.HostType]().
			SetLogger(logger).
			SetService(privatev1.HostTypes_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		response := &privatev1.HostTypesCreateResponse{}
		err = server.CreateWithCandidatePreparation(ctx, privatev1.HostTypesCreateRequest_builder{
			Object: privatev1.HostType_builder{Metadata: privatev1.Metadata_builder{Name: "create-id-change"}.Build()}.Build(),
		}.Build(), &response,
			func(_ context.Context, _ *privatev1.HostType, candidate *privatev1.HostType) error {
				candidate.SetId("replacement-id")
				return nil
			},
		)
		Expect(status.Code(err)).To(Equal(codes.PermissionDenied))

		listResponse := &privatev1.HostTypesListResponse{}
		err = server.List(ctx, privatev1.HostTypesListRequest_builder{}.Build(), &listResponse)
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse.GetTotal()).To(Equal(int32(0)))
	})

	It("applies masked clears to a detached update candidate before the callback", func() {
		server, err := NewGenericServer[*privatev1.HostType]().
			SetLogger(logger).
			SetService(privatev1.HostTypes_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		createResponse := &privatev1.HostTypesCreateResponse{}
		err = server.Create(ctx, privatev1.HostTypesCreateRequest_builder{
			Object: privatev1.HostType_builder{
				Metadata:    privatev1.Metadata_builder{Name: "callback-update"}.Build(),
				Description: "original-description",
			}.Build(),
		}.Build(), &createResponse)
		Expect(err).ToNot(HaveOccurred())
		created := createResponse.GetObject()
		updateObject := privatev1.HostType_builder{
			Id:       created.GetId(),
			Metadata: privatev1.Metadata_builder{Name: created.GetMetadata().GetName()}.Build(),
		}.Build()
		var currentSeen, candidateSeen *privatev1.HostType
		updateResponse := &privatev1.HostTypesUpdateResponse{}
		err = server.UpdateWithCandidatePreparation(ctx, privatev1.HostTypesUpdateRequest_builder{
			Object:     updateObject,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"description"}},
		}.Build(), &updateResponse,
			func(_ context.Context, current *privatev1.HostType, candidate *privatev1.HostType) error {
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
		server, err := NewGenericServer[*privatev1.HostType]().
			SetLogger(logger).
			SetService(privatev1.HostTypes_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		createResponse := &privatev1.HostTypesCreateResponse{}
		err = server.Create(ctx, privatev1.HostTypesCreateRequest_builder{
			Object: privatev1.HostType_builder{
				Metadata:    privatev1.Metadata_builder{Name: "no-mask-alias"}.Build(),
				Description: "original-description",
			}.Build(),
		}.Build(), &createResponse)
		Expect(err).ToNot(HaveOccurred())
		created := createResponse.GetObject()
		updateObject := privatev1.HostType_builder{
			Id: created.GetId(),
			Metadata: privatev1.Metadata_builder{
				Name:   created.GetMetadata().GetName(),
				Labels: map[string]string{"phase": "updated"},
			}.Build(),
			Description: "updated-description",
		}.Build()
		updateResponse := &privatev1.HostTypesUpdateResponse{}
		err = server.Update(ctx, privatev1.HostTypesUpdateRequest_builder{Object: updateObject}.Build(), &updateResponse)
		Expect(err).ToNot(HaveOccurred())

		updateObject.SetDescription("mutated-after-update")
		updateObject.GetMetadata().GetLabels()["phase"] = "mutated-after-update"
		getResponse := &privatev1.HostTypesGetResponse{}
		err = server.Get(ctx, privatev1.HostTypesGetRequest_builder{Id: created.GetId()}.Build(), &getResponse)
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetDescription()).To(Equal("updated-description"))
		Expect(getResponse.GetObject().GetMetadata().GetLabels()).To(Equal(map[string]string{"phase": "updated"}))
	})

	It("does not persist when a create callback fails", func() {
		server, err := NewGenericServer[*privatev1.HostType]().
			SetLogger(logger).
			SetService(privatev1.HostTypes_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		response := &privatev1.HostTypesCreateResponse{}
		err = server.CreateWithCandidatePreparation(ctx, privatev1.HostTypesCreateRequest_builder{
			Object: privatev1.HostType_builder{Metadata: privatev1.Metadata_builder{Name: "create-callback-failure"}.Build()}.Build(),
		}.Build(), &response,
			func(context.Context, *privatev1.HostType, *privatev1.HostType) error {
				return status.Error(codes.InvalidArgument, "callback failed")
			},
		)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		listResponse := &privatev1.HostTypesListResponse{}
		err = server.List(ctx, privatev1.HostTypesListRequest_builder{}.Build(), &listResponse)
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse.GetTotal()).To(Equal(int32(0)))
	})

	It("does not persist a failed update callback and leaves the stored object unchanged", func() {
		server, err := NewGenericServer[*privatev1.HostType]().
			SetLogger(logger).
			SetService(privatev1.HostTypes_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		createResponse := &privatev1.HostTypesCreateResponse{}
		err = server.Create(ctx, privatev1.HostTypesCreateRequest_builder{
			Object: privatev1.HostType_builder{
				Metadata:    privatev1.Metadata_builder{Name: "update-callback-failure"}.Build(),
				Description: "original-description",
			}.Build(),
		}.Build(), &createResponse)
		Expect(err).ToNot(HaveOccurred())
		created := createResponse.GetObject()
		updateResponse := &privatev1.HostTypesUpdateResponse{}
		err = server.UpdateWithCandidatePreparation(ctx, privatev1.HostTypesUpdateRequest_builder{
			Object: privatev1.HostType_builder{
				Id:          created.GetId(),
				Metadata:    privatev1.Metadata_builder{Name: created.GetMetadata().GetName()}.Build(),
				Description: "attempted-description",
			}.Build(),
		}.Build(), &updateResponse,
			func(context.Context, *privatev1.HostType, *privatev1.HostType) error {
				return status.Error(codes.InvalidArgument, "callback failed")
			},
		)
		Expect(err).To(HaveOccurred())
		getResponse := &privatev1.HostTypesGetResponse{}
		err = server.Get(ctx, privatev1.HostTypesGetRequest_builder{Id: created.GetId()}.Build(), &getResponse)
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetDescription()).To(Equal("original-description"))
	})

	It("revalidates a callback-mutated update before persistence", func() {
		server, err := NewGenericServer[*privatev1.HostType]().
			SetLogger(logger).
			SetService(privatev1.HostTypes_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		createResponse := &privatev1.HostTypesCreateResponse{}
		err = server.Create(ctx, privatev1.HostTypesCreateRequest_builder{
			Object: privatev1.HostType_builder{
				Metadata:    privatev1.Metadata_builder{Name: "invalid-callback-update"}.Build(),
				Description: "original-description",
			}.Build(),
		}.Build(), &createResponse)
		Expect(err).ToNot(HaveOccurred())
		created := createResponse.GetObject()
		updateResponse := &privatev1.HostTypesUpdateResponse{}
		err = server.UpdateWithCandidatePreparation(ctx, privatev1.HostTypesUpdateRequest_builder{
			Object: privatev1.HostType_builder{
				Id:       created.GetId(),
				Metadata: privatev1.Metadata_builder{Name: created.GetMetadata().GetName()}.Build(),
			}.Build(),
		}.Build(), &updateResponse,
			func(_ context.Context, _ *privatev1.HostType, candidate *privatev1.HostType) error {
				candidate.SetMetadata(nil)
				return nil
			},
		)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		getResponse := &privatev1.HostTypesGetResponse{}
		err = server.Get(ctx, privatev1.HostTypesGetRequest_builder{Id: created.GetId()}.Build(), &getResponse)
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetDescription()).To(Equal("original-description"))
	})

	It("rejects an update callback that clears metadata", func() {
		server, err := NewGenericServer[*privatev1.HostType]().
			SetLogger(logger).
			SetService(privatev1.HostTypes_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		createResponse := &privatev1.HostTypesCreateResponse{}
		err = server.Create(ctx, privatev1.HostTypesCreateRequest_builder{
			Object: privatev1.HostType_builder{
				Metadata:    privatev1.Metadata_builder{Name: "update-metadata-clear"}.Build(),
				Description: "original-description",
			}.Build(),
		}.Build(), &createResponse)
		Expect(err).ToNot(HaveOccurred())
		created := createResponse.GetObject()

		updateResponse := &privatev1.HostTypesUpdateResponse{}
		err = server.UpdateWithCandidatePreparation(ctx, privatev1.HostTypesUpdateRequest_builder{
			Object: privatev1.HostType_builder{
				Id:          created.GetId(),
				Metadata:    privatev1.Metadata_builder{Name: created.GetMetadata().GetName()}.Build(),
				Description: "attempted-description",
			}.Build(),
		}.Build(), &updateResponse,
			func(_ context.Context, _ *privatev1.HostType, candidate *privatev1.HostType) error {
				candidate.SetMetadata(nil)
				return nil
			},
		)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))

		getResponse := &privatev1.HostTypesGetResponse{}
		err = server.Get(ctx, privatev1.HostTypesGetRequest_builder{Id: created.GetId()}.Build(), &getResponse)
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetDescription()).To(Equal("original-description"))
		Expect(getResponse.GetObject().GetMetadata().GetTenant()).To(Equal(testTenant))
	})

	It("rejects an update callback that sets a disallowed tenant", func() {
		server, err := NewGenericServer[*privatev1.HostType]().
			SetLogger(logger).
			SetService(privatev1.HostTypes_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		createResponse := &privatev1.HostTypesCreateResponse{}
		err = server.Create(ctx, privatev1.HostTypesCreateRequest_builder{
			Object: privatev1.HostType_builder{
				Metadata:    privatev1.Metadata_builder{Name: "update-disallowed-tenant"}.Build(),
				Description: "original-description",
			}.Build(),
		}.Build(), &createResponse)
		Expect(err).ToNot(HaveOccurred())
		created := createResponse.GetObject()

		updateResponse := &privatev1.HostTypesUpdateResponse{}
		err = server.UpdateWithCandidatePreparation(ctx, privatev1.HostTypesUpdateRequest_builder{
			Object: privatev1.HostType_builder{
				Id:          created.GetId(),
				Metadata:    privatev1.Metadata_builder{Name: created.GetMetadata().GetName()}.Build(),
				Description: "attempted-description",
			}.Build(),
		}.Build(), &updateResponse,
			func(_ context.Context, _ *privatev1.HostType, candidate *privatev1.HostType) error {
				candidate.GetMetadata().SetTenant(auth.SharedTenant)
				return nil
			},
		)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.PermissionDenied))

		getResponse := &privatev1.HostTypesGetResponse{}
		err = server.Get(ctx, privatev1.HostTypesGetRequest_builder{Id: created.GetId()}.Build(), &getResponse)
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetDescription()).To(Equal("original-description"))
		Expect(getResponse.GetObject().GetMetadata().GetTenant()).To(Equal(testTenant))
	})

	It("does not add a row when persistence fails", func() {
		server, err := NewGenericServer[*privatev1.HostType]().
			SetLogger(logger).
			SetService(privatev1.HostTypes_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		request := privatev1.HostTypesCreateRequest_builder{
			Object: privatev1.HostType_builder{Metadata: privatev1.Metadata_builder{Name: "duplicate-create"}.Build()}.Build(),
		}.Build()
		response := &privatev1.HostTypesCreateResponse{}
		err = server.Create(ctx, request, &response)
		Expect(err).ToNot(HaveOccurred())
		tx, err := database.TxFromContext(ctx)
		Expect(err).ToNot(HaveOccurred())
		err = tx.Savepoint(ctx, func(savepointCtx context.Context) error {
			return server.Create(savepointCtx, request, &response)
		})
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.AlreadyExists))
		listResponse := &privatev1.HostTypesListResponse{}
		err = server.List(ctx, privatev1.HostTypesListRequest_builder{}.Build(), &listResponse)
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse.GetTotal()).To(Equal(int32(1)))
	})

	It("runs dry-run callbacks without persisting", func() {
		server, err := NewGenericServer[*privatev1.HostType]().
			SetLogger(logger).
			SetService(privatev1.HostTypes_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		requestObject := privatev1.HostType_builder{
			Metadata:    privatev1.Metadata_builder{Name: "dry-run-callback"}.Build(),
			Description: "request-description",
		}.Build()
		response := &privatev1.HostTypesCreateResponse{}
		err = server.CreateWithCandidatePreparation(dryRunCtx(), privatev1.HostTypesCreateRequest_builder{Object: requestObject}.Build(), &response,
			func(_ context.Context, current *privatev1.HostType, candidate *privatev1.HostType) error {
				Expect(current).To(BeNil())
				candidate.SetDescription("dry-run-description")
				return nil
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetObject().GetDescription()).To(Equal("dry-run-description"))
		Expect(requestObject.GetDescription()).To(Equal("request-description"))
		listResponse := &privatev1.HostTypesListResponse{}
		err = server.List(ctx, privatev1.HostTypesListRequest_builder{}.Build(), &listResponse)
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse.GetTotal()).To(Equal(int32(0)))
	})
})

var _ = Describe("Generic server filtering", func() {
	It("Classifies a filter-translate failure as InvalidArgument, not Internal", func() {
		// Build a server whose filter descriptor is restricted to a message that doesn't have a 'title' field.
		server, err := NewGenericServer[*privatev1.HostType]().
			SetLogger(logger).
			SetService(privatev1.HostTypes_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			SetFilterDesc((*privatev1.Tenant)(nil).ProtoReflect().Descriptor()).
			Build()
		Expect(err).ToNot(HaveOccurred())

		response := &privatev1.HostTypesListResponse{}
		err = server.List(
			ctx,
			privatev1.HostTypesListRequest_builder{
				Filter: new("this.title == 'x'"),
			}.Build(),
			&response,
		)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
	})
})

var _ = Describe("Generic server dry run", func() {
	var server *GenericServer[*privatev1.HostType]

	BeforeEach(func() {
		var err error
		server, err = NewGenericServer[*privatev1.HostType]().
			SetLogger(logger).
			SetService(privatev1.HostTypes_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	It("Assigns creator and tenant to the object", func() {
		response := &privatev1.HostTypesCreateResponse{}
		err := server.Create(
			dryRunCtx(),
			privatev1.HostTypesCreateRequest_builder{
				Object: privatev1.HostType_builder{
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
		response := &privatev1.HostTypesCreateResponse{}
		err := server.Create(
			dryRunCtx(),
			privatev1.HostTypesCreateRequest_builder{
				Object: privatev1.HostType_builder{
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
		response := &privatev1.HostTypesCreateResponse{}
		err := server.Create(
			dryRunCtx(),
			privatev1.HostTypesCreateRequest_builder{
				Object: privatev1.HostType_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "dry-run-no-persist",
					}.Build(),
				}.Build(),
			}.Build(),
			&response,
		)
		Expect(err).ToNot(HaveOccurred())

		listResponse := &privatev1.HostTypesListResponse{}
		err = server.List(ctx,
			privatev1.HostTypesListRequest_builder{}.Build(),
			&listResponse,
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse.GetTotal()).To(Equal(int32(0)))
	})

	It("Does not persist objects", func() {
		response := &privatev1.HostTypesCreateResponse{}
		err := server.Create(
			dryRunCtx(),
			privatev1.HostTypesCreateRequest_builder{
				Object: privatev1.HostType_builder{
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
		srv, err := NewGenericServer[*privatev1.HostType]().
			SetLogger(logger).
			SetService(privatev1.HostTypes_ServiceDesc.ServiceName).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		falseCtx := grpcmetadata.NewIncomingContext(ctx,
			grpcmetadata.Pairs(DryRunMetadataKey, "false"))

		response := &privatev1.HostTypesCreateResponse{}
		err = srv.Create(
			falseCtx,
			privatev1.HostTypesCreateRequest_builder{
				Object: privatev1.HostType_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "not-a-dry-run",
					}.Build(),
				}.Build(),
			}.Build(),
			&response,
		)
		Expect(err).ToNot(HaveOccurred())

		listResponse := &privatev1.HostTypesListResponse{}
		err = srv.List(ctx,
			privatev1.HostTypesListRequest_builder{}.Build(),
			&listResponse,
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse.GetTotal()).To(Equal(int32(1)))
	})
})
