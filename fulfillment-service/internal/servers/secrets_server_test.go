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
	"go.uber.org/mock/gomock"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
)

type mockPrivateSecretsServer struct {
	privatev1.UnimplementedSecretsServer

	createFunc func(context.Context, *privatev1.SecretsCreateRequest) (*privatev1.SecretsCreateResponse, error)
	listFunc   func(context.Context, *privatev1.SecretsListRequest) (*privatev1.SecretsListResponse, error)
	getFunc    func(context.Context, *privatev1.SecretsGetRequest) (*privatev1.SecretsGetResponse, error)
	updateFunc func(context.Context, *privatev1.SecretsUpdateRequest) (*privatev1.SecretsUpdateResponse, error)
	deleteFunc func(context.Context, *privatev1.SecretsDeleteRequest) (*privatev1.SecretsDeleteResponse, error)
}

func (m *mockPrivateSecretsServer) Create(ctx context.Context,
	req *privatev1.SecretsCreateRequest) (*privatev1.SecretsCreateResponse, error) {
	if m.createFunc != nil {
		return m.createFunc(ctx, req)
	}
	return m.UnimplementedSecretsServer.Create(ctx, req)
}

func (m *mockPrivateSecretsServer) List(ctx context.Context,
	req *privatev1.SecretsListRequest) (*privatev1.SecretsListResponse, error) {
	if m.listFunc != nil {
		return m.listFunc(ctx, req)
	}
	return m.UnimplementedSecretsServer.List(ctx, req)
}

func (m *mockPrivateSecretsServer) Get(ctx context.Context,
	req *privatev1.SecretsGetRequest) (*privatev1.SecretsGetResponse, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx, req)
	}
	return m.UnimplementedSecretsServer.Get(ctx, req)
}

func (m *mockPrivateSecretsServer) Update(ctx context.Context,
	req *privatev1.SecretsUpdateRequest) (*privatev1.SecretsUpdateResponse, error) {
	if m.updateFunc != nil {
		return m.updateFunc(ctx, req)
	}
	return m.UnimplementedSecretsServer.Update(ctx, req)
}

func (m *mockPrivateSecretsServer) Delete(ctx context.Context,
	req *privatev1.SecretsDeleteRequest) (*privatev1.SecretsDeleteResponse, error) {
	if m.deleteFunc != nil {
		return m.deleteFunc(ctx, req)
	}
	return m.UnimplementedSecretsServer.Delete(ctx, req)
}

func newTestSecretsServer(mock privatev1.SecretsServer) *SecretsServer {
	inMapper, err := NewGenericMapper[*publicv1.Secret, *privatev1.Secret]().
		SetLogger(logger).
		SetStrict(true).
		Build()
	Expect(err).ToNot(HaveOccurred())

	outMapper, err := NewGenericMapper[*privatev1.Secret, *publicv1.Secret]().
		SetLogger(logger).
		SetStrict(false).
		Build()
	Expect(err).ToNot(HaveOccurred())

	return &SecretsServer{
		logger:    logger,
		private:   mock,
		inMapper:  inMapper,
		outMapper: outMapper,
	}
}

var _ = Describe("Secrets Server", func() {
	Describe("Builder", func() {
		var (
			ctrl         *gomock.Controller
			tenancyLogic *auth.MockTenancyLogic
		)

		BeforeEach(func() {
			ctrl = gomock.NewController(GinkgoT())
			tenancyLogic = auth.NewMockTenancyLogic(ctrl)
		})

		AfterEach(func() {
			ctrl.Finish()
		})
		It("builds successfully with all required parameters", func() {
			attributionLogic := auth.NewMockAttributionLogic(ctrl)
			server, err := NewSecretsServer().
				SetLogger(logger).
				SetTenancyLogic(tenancyLogic).
				SetAttributionLogic(attributionLogic).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("fails if logger is not set", func() {
			server, err := NewSecretsServer().
				SetTenancyLogic(tenancyLogic).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("fails if tenancy logic is not set", func() {
			server, err := NewSecretsServer().
				SetLogger(logger).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Interface compliance", func() {
		It("implements SecretsServer interface", func() {
			var _ publicv1.SecretsServer = (*SecretsServer)(nil)
		})
	})

	Describe("Create", func() {
		It("sets backend to VAULT on the private request", func() {
			var capturedRequest *privatev1.SecretsCreateRequest

			mock := &mockPrivateSecretsServer{
				createFunc: func(_ context.Context,
					req *privatev1.SecretsCreateRequest) (*privatev1.SecretsCreateResponse, error) {
					capturedRequest = req
					resp := &privatev1.SecretsCreateResponse{}
					resp.SetObject(privatev1.Secret_builder{
						Id: "secret-1",
						Metadata: privatev1.Metadata_builder{
							Name: "my-secret",
						}.Build(),
						Backend: privatev1.SecretBackend_SECRET_BACKEND_VAULT,
					}.Build())
					return resp, nil
				},
			}

			server := newTestSecretsServer(mock)
			_, err := server.Create(ctx, publicv1.SecretsCreateRequest_builder{
				Object: publicv1.Secret_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "my-secret",
					}.Build(),
					Data: map[string][]byte{"key": []byte("value")},
				}.Build(),
			}.Build())

			Expect(err).ToNot(HaveOccurred())
			Expect(capturedRequest).ToNot(BeNil())
			Expect(capturedRequest.GetObject().GetBackend()).
				To(Equal(privatev1.SecretBackend_SECRET_BACKEND_VAULT))
		})

		It("redacts data from response", func() {
			mock := &mockPrivateSecretsServer{
				createFunc: func(_ context.Context,
					_ *privatev1.SecretsCreateRequest) (*privatev1.SecretsCreateResponse, error) {
					resp := &privatev1.SecretsCreateResponse{}
					resp.SetObject(privatev1.Secret_builder{
						Id: "secret-1",
						Metadata: privatev1.Metadata_builder{
							Name: "my-secret",
						}.Build(),
						Data:        map[string][]byte{"key": []byte("value")},
						Backend:     privatev1.SecretBackend_SECRET_BACKEND_VAULT,
						Coordinates: map[string]string{"path": "/secret/data/test"},
					}.Build())
					return resp, nil
				},
			}

			server := newTestSecretsServer(mock)
			resp, err := server.Create(ctx, publicv1.SecretsCreateRequest_builder{
				Object: publicv1.Secret_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "my-secret",
					}.Build(),
					Data: map[string][]byte{"key": []byte("value")},
				}.Build(),
			}.Build())

			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetObject().GetId()).To(Equal("secret-1"))
			Expect(resp.GetObject().GetData()).To(BeEmpty())
		})
	})

	Describe("List", func() {
		It("strips data from all items", func() {
			mock := &mockPrivateSecretsServer{
				listFunc: func(_ context.Context,
					_ *privatev1.SecretsListRequest) (*privatev1.SecretsListResponse, error) {
					resp := &privatev1.SecretsListResponse{}
					resp.SetSize(2)
					resp.SetTotal(2)
					resp.SetItems([]*privatev1.Secret{
						privatev1.Secret_builder{
							Id:      "secret-1",
							Data:    map[string][]byte{"key1": []byte("val1")},
							Backend: privatev1.SecretBackend_SECRET_BACKEND_VAULT,
						}.Build(),
						privatev1.Secret_builder{
							Id:      "secret-2",
							Data:    map[string][]byte{"key2": []byte("val2")},
							Backend: privatev1.SecretBackend_SECRET_BACKEND_VAULT,
						}.Build(),
					})
					return resp, nil
				},
			}

			server := newTestSecretsServer(mock)
			resp, err := server.List(ctx, publicv1.SecretsListRequest_builder{}.Build())

			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetItems()).To(HaveLen(2))
			for _, item := range resp.GetItems() {
				Expect(item.GetData()).To(BeEmpty())
			}
		})

		It("forwards offset, limit, filter, and order to private request", func() {
			var capturedRequest *privatev1.SecretsListRequest

			mock := &mockPrivateSecretsServer{
				listFunc: func(_ context.Context,
					req *privatev1.SecretsListRequest) (*privatev1.SecretsListResponse, error) {
					capturedRequest = req
					resp := &privatev1.SecretsListResponse{}
					return resp, nil
				},
			}

			server := newTestSecretsServer(mock)
			_, err := server.List(ctx, publicv1.SecretsListRequest_builder{
				Offset: new(int32(10)),
				Limit:  new(int32(25)),
				Filter: new("this.metadata.name == 'test'"),
				Order:  new("metadata.name desc"),
			}.Build())

			Expect(err).ToNot(HaveOccurred())
			Expect(capturedRequest).ToNot(BeNil())
			Expect(capturedRequest.GetOffset()).To(Equal(int32(10)))
			Expect(capturedRequest.GetLimit()).To(Equal(int32(25)))
			Expect(capturedRequest.GetFilter()).To(Equal("this.metadata.name == 'test'"))
			Expect(capturedRequest.GetOrder()).To(Equal("metadata.name desc"))
		})
	})

	Describe("Get", func() {
		It("returns data in response", func() {
			mock := &mockPrivateSecretsServer{
				getFunc: func(_ context.Context,
					_ *privatev1.SecretsGetRequest) (*privatev1.SecretsGetResponse, error) {
					resp := &privatev1.SecretsGetResponse{}
					resp.SetObject(privatev1.Secret_builder{
						Id: "secret-1",
						Metadata: privatev1.Metadata_builder{
							Name: "my-secret",
						}.Build(),
						Data: map[string][]byte{
							"tls.crt": []byte("cert-data"),
							"tls.key": []byte("key-data"),
						},
						Backend: privatev1.SecretBackend_SECRET_BACKEND_VAULT,
					}.Build())
					return resp, nil
				},
			}

			server := newTestSecretsServer(mock)
			resp, err := server.Get(ctx, publicv1.SecretsGetRequest_builder{
				Id: "secret-1",
			}.Build())

			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetObject().GetData()).To(HaveLen(2))
			Expect(resp.GetObject().GetData()).To(HaveKeyWithValue("tls.crt", []byte("cert-data")))
			Expect(resp.GetObject().GetData()).To(HaveKeyWithValue("tls.key", []byte("key-data")))
		})
	})

	Describe("Update", func() {
		It("returns FAILED_PRECONDITION for hub-backed secret with non-empty data", func() {
			mock := &mockPrivateSecretsServer{
				getFunc: func(_ context.Context,
					_ *privatev1.SecretsGetRequest) (*privatev1.SecretsGetResponse, error) {
					resp := &privatev1.SecretsGetResponse{}
					resp.SetObject(privatev1.Secret_builder{
						Id:      "hub-secret",
						Backend: privatev1.SecretBackend_SECRET_BACKEND_HUB,
					}.Build())
					return resp, nil
				},
			}

			server := newTestSecretsServer(mock)
			_, err := server.Update(ctx, publicv1.SecretsUpdateRequest_builder{
				Object: publicv1.Secret_builder{
					Id:   "hub-secret",
					Data: map[string][]byte{"key": []byte("value")},
				}.Build(),
			}.Build())

			Expect(err).To(HaveOccurred())
			st, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(grpccodes.FailedPrecondition))
			Expect(st.Message()).To(ContainSubstring("hub-backed"))
		})

		It("preserves backend for hub-backed secret with empty data", func() {
			var capturedRequest *privatev1.SecretsUpdateRequest

			mock := &mockPrivateSecretsServer{
				getFunc: func(_ context.Context,
					_ *privatev1.SecretsGetRequest) (*privatev1.SecretsGetResponse, error) {
					resp := &privatev1.SecretsGetResponse{}
					resp.SetObject(privatev1.Secret_builder{
						Id: "hub-secret",
						Metadata: privatev1.Metadata_builder{
							Name: "hub-secret",
						}.Build(),
						Backend: privatev1.SecretBackend_SECRET_BACKEND_HUB,
					}.Build())
					return resp, nil
				},
				updateFunc: func(_ context.Context,
					req *privatev1.SecretsUpdateRequest) (*privatev1.SecretsUpdateResponse, error) {
					capturedRequest = req
					resp := &privatev1.SecretsUpdateResponse{}
					resp.SetObject(privatev1.Secret_builder{
						Id: "hub-secret",
						Metadata: privatev1.Metadata_builder{
							Name: "hub-secret-updated",
						}.Build(),
						Backend: privatev1.SecretBackend_SECRET_BACKEND_HUB,
					}.Build())
					return resp, nil
				},
			}

			server := newTestSecretsServer(mock)
			resp, err := server.Update(ctx, publicv1.SecretsUpdateRequest_builder{
				Object: publicv1.Secret_builder{
					Id: "hub-secret",
					Metadata: publicv1.Metadata_builder{
						Name: "hub-secret-updated",
					}.Build(),
				}.Build(),
			}.Build())

			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetObject().GetMetadata().GetName()).To(Equal("hub-secret-updated"))
			Expect(capturedRequest).ToNot(BeNil())
			Expect(capturedRequest.GetObject().GetBackend()).
				To(Equal(privatev1.SecretBackend_SECRET_BACKEND_HUB))
		})

		It("preserves backend and redacts response for vault-backed secret", func() {
			var capturedRequest *privatev1.SecretsUpdateRequest

			mock := &mockPrivateSecretsServer{
				getFunc: func(_ context.Context,
					_ *privatev1.SecretsGetRequest) (*privatev1.SecretsGetResponse, error) {
					resp := &privatev1.SecretsGetResponse{}
					resp.SetObject(privatev1.Secret_builder{
						Id:      "vault-secret",
						Backend: privatev1.SecretBackend_SECRET_BACKEND_VAULT,
					}.Build())
					return resp, nil
				},
				updateFunc: func(_ context.Context,
					req *privatev1.SecretsUpdateRequest) (*privatev1.SecretsUpdateResponse, error) {
					capturedRequest = req
					resp := &privatev1.SecretsUpdateResponse{}
					resp.SetObject(privatev1.Secret_builder{
						Id:      "vault-secret",
						Data:    map[string][]byte{"key": []byte("new-value")},
						Backend: privatev1.SecretBackend_SECRET_BACKEND_VAULT,
					}.Build())
					return resp, nil
				},
			}

			server := newTestSecretsServer(mock)
			resp, err := server.Update(ctx, publicv1.SecretsUpdateRequest_builder{
				Object: publicv1.Secret_builder{
					Id:   "vault-secret",
					Data: map[string][]byte{"key": []byte("new-value")},
				}.Build(),
			}.Build())

			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetObject().GetId()).To(Equal("vault-secret"))
			Expect(capturedRequest).ToNot(BeNil())
			Expect(capturedRequest.GetObject().GetBackend()).
				To(Equal(privatev1.SecretBackend_SECRET_BACKEND_VAULT))
			Expect(resp.GetObject().GetData()).To(BeEmpty())
		})
	})

	Describe("Delete", func() {
		It("returns FAILED_PRECONDITION for hub-backed secret", func() {
			mock := &mockPrivateSecretsServer{
				getFunc: func(_ context.Context,
					_ *privatev1.SecretsGetRequest) (*privatev1.SecretsGetResponse, error) {
					resp := &privatev1.SecretsGetResponse{}
					resp.SetObject(privatev1.Secret_builder{
						Id:      "hub-secret",
						Backend: privatev1.SecretBackend_SECRET_BACKEND_HUB,
					}.Build())
					return resp, nil
				},
			}

			server := newTestSecretsServer(mock)
			_, err := server.Delete(ctx, publicv1.SecretsDeleteRequest_builder{
				Id: "hub-secret",
			}.Build())

			Expect(err).To(HaveOccurred())
			st, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(grpccodes.FailedPrecondition))
			Expect(st.Message()).To(ContainSubstring("hub-backed"))
		})

		It("delegates to private server for vault-backed secret", func() {
			var deleteCalled bool

			mock := &mockPrivateSecretsServer{
				getFunc: func(_ context.Context,
					_ *privatev1.SecretsGetRequest) (*privatev1.SecretsGetResponse, error) {
					resp := &privatev1.SecretsGetResponse{}
					resp.SetObject(privatev1.Secret_builder{
						Id:      "vault-secret",
						Backend: privatev1.SecretBackend_SECRET_BACKEND_VAULT,
					}.Build())
					return resp, nil
				},
				deleteFunc: func(_ context.Context,
					_ *privatev1.SecretsDeleteRequest) (*privatev1.SecretsDeleteResponse, error) {
					deleteCalled = true
					return &privatev1.SecretsDeleteResponse{}, nil
				},
			}

			server := newTestSecretsServer(mock)
			_, err := server.Delete(ctx, publicv1.SecretsDeleteRequest_builder{
				Id: "vault-secret",
			}.Build())

			Expect(err).ToNot(HaveOccurred())
			Expect(deleteCalled).To(BeTrue())
		})
	})
})
