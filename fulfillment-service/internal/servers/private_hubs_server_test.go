/*
Copyright (c) 2025 Red Hat Inc.

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

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/events"
)

var _ = Describe("Private hubs server", func() {
	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewPrivateHubsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewPrivateHubsServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if attribution logic is not set", func() {
			server, err := NewPrivateHubsServer().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("attribution logic is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewPrivateHubsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		var server *PrivateHubsServer

		BeforeEach(func() {
			var err error

			// Create the server:
			server, err = NewPrivateHubsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Creates object", func() {
			response, err := server.Create(ctx, privatev1.HubsCreateRequest_builder{
				Object: privatev1.Hub_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.HubSpec_builder{
						Kubeconfig: []byte("my_config"),
						Namespace:  "my_ns",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			object := response.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).ToNot(BeEmpty())
		})

		It("List objects", func() {
			// Create a few objects:
			const count = 10
			for i := range count {
				_, err := server.Create(ctx, privatev1.HubsCreateRequest_builder{
					Object: privatev1.Hub_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Spec: privatev1.HubSpec_builder{
							Kubeconfig: []byte(fmt.Sprintf("my_config_%d", i)),
							Namespace:  fmt.Sprintf("my_ns_%d", i),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects:
			response, err := server.List(ctx, privatev1.HubsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			items := response.GetItems()
			Expect(items).To(HaveLen(count))
		})

		It("List objects with limit", func() {
			// Create a few objects:
			const count = 10
			for i := range count {
				_, err := server.Create(ctx, privatev1.HubsCreateRequest_builder{
					Object: privatev1.Hub_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Spec: privatev1.HubSpec_builder{
							Kubeconfig: []byte(fmt.Sprintf("my_config_%d", i)),
							Namespace:  fmt.Sprintf("my_ns_%d", i),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects:
			response, err := server.List(ctx, privatev1.HubsListRequest_builder{
				Limit: new(int32(1)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(BeNumerically("==", 1))
		})

		It("List objects with offset", func() {
			// Create a few objects:
			const count = 10
			for i := range count {
				_, err := server.Create(ctx, privatev1.HubsCreateRequest_builder{
					Object: privatev1.Hub_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Spec: privatev1.HubSpec_builder{
							Kubeconfig: []byte(fmt.Sprintf("my_config_%d", i)),
							Namespace:  fmt.Sprintf("my_ns_%d", i),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects:
			response, err := server.List(ctx, privatev1.HubsListRequest_builder{
				Offset: new(int32(1)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(BeNumerically("==", count-1))
		})

		It("List objects with filter", func() {
			// Create a few objects:
			const count = 10
			var objects []*privatev1.Hub
			for i := range count {
				response, err := server.Create(ctx, privatev1.HubsCreateRequest_builder{
					Object: privatev1.Hub_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Spec: privatev1.HubSpec_builder{
							Kubeconfig: []byte(fmt.Sprintf("my_config_%d", i)),
							Namespace:  fmt.Sprintf("my_ns_%d", i),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				objects = append(objects, response.GetObject())
			}

			// List the objects:
			for _, object := range objects {
				response, err := server.List(ctx, privatev1.HubsListRequest_builder{
					Filter: new(fmt.Sprintf("this.id == '%s'", object.GetId())),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetSize()).To(BeNumerically("==", 1))
				Expect(response.GetItems()[0].GetId()).To(Equal(object.GetId()))
			}
		})

		It("Get object", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, privatev1.HubsCreateRequest_builder{
				Object: privatev1.Hub_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.HubSpec_builder{
						Kubeconfig: []byte("my_config"),
						Namespace:  "my_ns",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Get it:
			getResponse, err := server.Get(ctx, privatev1.HubsGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(createResponse.GetObject(), getResponse.GetObject())).To(BeTrue())
		})

		It("Update object", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, privatev1.HubsCreateRequest_builder{
				Object: privatev1.Hub_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.HubSpec_builder{
						Kubeconfig: []byte("my_config"),
						Namespace:  "my_ns",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()
			name := object.GetMetadata().GetName()
			// Update the object:
			updateResponse, err := server.Update(ctx, privatev1.HubsUpdateRequest_builder{
				Object: privatev1.Hub_builder{
					Id:       object.GetId(),
					Metadata: privatev1.Metadata_builder{Name: name}.Build(),
					Spec: privatev1.HubSpec_builder{
						Kubeconfig: []byte("your_config"),
						Namespace:  "your_ns",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetSpec().GetKubeconfig()).To(Equal([]byte("your_config")))
			Expect(updateResponse.GetObject().GetSpec().GetNamespace()).To(Equal("your_ns"))

			// Get and verify:
			getResponse, err := server.Get(ctx, privatev1.HubsGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetSpec().GetKubeconfig()).To(Equal([]byte("your_config")))
			Expect(getResponse.GetObject().GetSpec().GetNamespace()).To(Equal("your_ns"))
		})

		Describe("Kubeconfig secret reference", func() {
			var secretsDao *dao.GenericDAO[*privatev1.Secret]

			BeforeEach(func() {
				var err error
				secretsDao, err = dao.NewGenericDAO[*privatev1.Secret]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())

				_, err = secretsDao.Create().SetObject(privatev1.Secret_builder{
					Id: "my-secret-id",
					Metadata: privatev1.Metadata_builder{
						Name:   "my-secret-name",
						Tenant: auth.SharedTenant,
					}.Build(),
				}.Build()).Do(ctx)
				Expect(err).ToNot(HaveOccurred())
			})

			It("Creates a hub with kubeconfig_secret reference by id", func() {
				response, err := server.Create(ctx, privatev1.HubsCreateRequest_builder{
					Object: privatev1.Hub_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Spec: privatev1.HubSpec_builder{
							Namespace: "my_ns",
							KubeconfigSecret: privatev1.SecretLocalReference_builder{
								Id: "my-secret-id",
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				ref := response.GetObject().GetSpec().GetKubeconfigSecret()
				Expect(ref.GetId()).To(Equal("my-secret-id"))
				Expect(ref.GetName()).To(Equal("my-secret-name"))
			})

			It("Creates a hub with kubeconfig_secret reference by name", func() {
				response, err := server.Create(ctx, privatev1.HubsCreateRequest_builder{
					Object: privatev1.Hub_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Spec: privatev1.HubSpec_builder{
							Namespace: "my_ns",
							KubeconfigSecret: privatev1.SecretLocalReference_builder{
								Name: "my-secret-name",
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				ref := response.GetObject().GetSpec().GetKubeconfigSecret()
				Expect(ref.GetId()).To(Equal("my-secret-id"))
				Expect(ref.GetName()).To(Equal("my-secret-name"))
			})

			It("Rejects create when kubeconfig and kubeconfig_secret are both set", func() {
				_, err := server.Create(ctx, privatev1.HubsCreateRequest_builder{
					Object: privatev1.Hub_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Spec: privatev1.HubSpec_builder{
							Kubeconfig: []byte("my_config"),
							Namespace:  "my_ns",
							KubeconfigSecret: privatev1.SecretLocalReference_builder{
								Id: "my-secret-id",
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				Expect(grpcstatus.Convert(err).Message()).To(ContainSubstring("mutually exclusive"))
			})

			It("Rejects create when kubeconfig_secret references a non-existent secret", func() {
				_, err := server.Create(ctx, privatev1.HubsCreateRequest_builder{
					Object: privatev1.Hub_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Spec: privatev1.HubSpec_builder{
							Namespace: "my_ns",
							KubeconfigSecret: privatev1.SecretLocalReference_builder{
								Id: "missing-secret-id",
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				Expect(grpcstatus.Convert(err).Message()).To(ContainSubstring("no secret"))
			})

			It("Updates a hub with kubeconfig_secret reference", func() {
				createResponse, err := server.Create(ctx, privatev1.HubsCreateRequest_builder{
					Object: privatev1.Hub_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Spec: privatev1.HubSpec_builder{
							Namespace: "my_ns",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				updateMask, err := fieldmaskpb.New(createResponse.GetObject(), "spec.kubeconfig_secret")
				Expect(err).ToNot(HaveOccurred())

				updateResponse, err := server.Update(ctx, privatev1.HubsUpdateRequest_builder{
					Object: privatev1.Hub_builder{
						Id: createResponse.GetObject().GetId(),
						Spec: privatev1.HubSpec_builder{
							KubeconfigSecret: privatev1.SecretLocalReference_builder{
								Id: "my-secret-id",
							}.Build(),
						}.Build(),
					}.Build(),
					UpdateMask: updateMask,
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				ref := updateResponse.GetObject().GetSpec().GetKubeconfigSecret()
				Expect(ref.GetId()).To(Equal("my-secret-id"))
				Expect(ref.GetName()).To(Equal("my-secret-name"))
			})

			It("Rejects create when kubeconfig_secret specifies neither id nor name", func() {
				_, err := server.Create(ctx, privatev1.HubsCreateRequest_builder{
					Object: privatev1.Hub_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Spec: privatev1.HubSpec_builder{
							Namespace:        "my_ns",
							KubeconfigSecret: privatev1.SecretLocalReference_builder{}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				Expect(grpcstatus.Convert(err).Message()).To(ContainSubstring("must specify id or name"))
			})

			It("Rejects update when kubeconfig and kubeconfig_secret are both set", func() {
				createResponse, err := server.Create(ctx, privatev1.HubsCreateRequest_builder{
					Object: privatev1.Hub_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Spec: privatev1.HubSpec_builder{
							Namespace: "my_ns",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				updateMask, err := fieldmaskpb.New(createResponse.GetObject(), "spec")
				Expect(err).ToNot(HaveOccurred())

				_, err = server.Update(ctx, privatev1.HubsUpdateRequest_builder{
					Object: privatev1.Hub_builder{
						Id: createResponse.GetObject().GetId(),
						Spec: privatev1.HubSpec_builder{
							Kubeconfig: []byte("inline-kubeconfig"),
							KubeconfigSecret: privatev1.SecretLocalReference_builder{
								Id: "my-secret-id",
							}.Build(),
						}.Build(),
					}.Build(),
					UpdateMask: updateMask,
				}.Build())
				Expect(err).To(HaveOccurred())
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				Expect(grpcstatus.Convert(err).Message()).To(ContainSubstring("mutually exclusive"))
			})

			It("Rejects update when kubeconfig conflicts with existing kubeconfig_secret in DB", func() {
				createResponse, err := server.Create(ctx, privatev1.HubsCreateRequest_builder{
					Object: privatev1.Hub_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Spec: privatev1.HubSpec_builder{
							Namespace: "my_ns",
							KubeconfigSecret: privatev1.SecretLocalReference_builder{
								Id: "my-secret-id",
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				updateMask, err := fieldmaskpb.New(createResponse.GetObject(), "spec.kubeconfig")
				Expect(err).ToNot(HaveOccurred())

				_, err = server.Update(ctx, privatev1.HubsUpdateRequest_builder{
					Object: privatev1.Hub_builder{
						Id: createResponse.GetObject().GetId(),
						Spec: privatev1.HubSpec_builder{
							Kubeconfig: []byte("inline-kubeconfig"),
						}.Build(),
					}.Build(),
					UpdateMask: updateMask,
				}.Build())
				Expect(err).To(HaveOccurred())
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				Expect(grpcstatus.Convert(err).Message()).To(ContainSubstring("mutually exclusive"))
			})

			It("Rejects update when kubeconfig_secret conflicts with existing kubeconfig in DB", func() {
				createResponse, err := server.Create(ctx, privatev1.HubsCreateRequest_builder{
					Object: privatev1.Hub_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Spec: privatev1.HubSpec_builder{
							Kubeconfig: []byte("existing-kubeconfig"),
							Namespace:  "my_ns",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				updateMask, err := fieldmaskpb.New(createResponse.GetObject(), "spec.kubeconfig_secret")
				Expect(err).ToNot(HaveOccurred())

				_, err = server.Update(ctx, privatev1.HubsUpdateRequest_builder{
					Object: privatev1.Hub_builder{
						Id: createResponse.GetObject().GetId(),
						Spec: privatev1.HubSpec_builder{
							KubeconfigSecret: privatev1.SecretLocalReference_builder{
								Id: "my-secret-id",
							}.Build(),
						}.Build(),
					}.Build(),
					UpdateMask: updateMask,
				}.Build())
				Expect(err).To(HaveOccurred())
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				Expect(grpcstatus.Convert(err).Message()).To(ContainSubstring("mutually exclusive"))
			})
		})

		It("Delete object", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, privatev1.HubsCreateRequest_builder{
				Object: privatev1.Hub_builder{
					Metadata: privatev1.Metadata_builder{
						Name:       "test-hub",
						Finalizers: []string{"a"},
					}.Build(),
					Spec: privatev1.HubSpec_builder{
						Kubeconfig: []byte("your_config"),
						Namespace:  "your_ns",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Delete the object:
			_, err = server.Delete(ctx, privatev1.HubsDeleteRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Get and verify:
			getResponse, err := server.Get(ctx, privatev1.HubsGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetMetadata().GetDeletionTimestamp()).ToNot(BeNil())
		})
	})

	It("Redacts event payload", func() {
		// Create a mock notifier that captures the event:
		var event *privatev1.Event
		notifier := events.NewMockNotifier(ctrl)
		notifier.EXPECT().
			Notify(gomock.Any(), gomock.Any()).
			DoAndReturn(
				func(ctx context.Context, payload proto.Message) error {
					event = payload.(*privatev1.Event)
					return nil
				},
			)

		// Create the server configured with the mock notifier:
		server, err := NewPrivateHubsServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			SetNotifier(notifier).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Create the object:
		response, err := server.Create(
			ctx, privatev1.HubsCreateRequest_builder{
				Object: privatev1.Hub_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.HubSpec_builder{
						Kubeconfig: []byte("my_config"),
					}.Build(),
				}.Build(),
			}.Build(),
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())

		// Verify the event:
		Expect(event).ToNot(BeNil())
		Expect(event.GetType()).To(Equal(privatev1.EventType_EVENT_TYPE_OBJECT_CREATED))
		object := event.GetHub()
		Expect(object).ToNot(BeNil())
		Expect(object.GetSpec().GetKubeconfig()).To(BeEmpty())
	})
})
