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
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
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
)

type ontapBackendProbeFunc func(context.Context, string, string, string) error

func (f ontapBackendProbeFunc) Probe(ctx context.Context, endpoint, username, password string) error {
	return f(ctx, endpoint, username, password)
}

var _ = Describe("ONTAP backend registration", func() {
	var backendServer *PrivateStorageBackendsServer
	var secretsServer *PrivateSecretsServer
	var store *memorySecretStore
	var endpoint *httptest.Server
	var calls atomic.Int32
	BeforeEach(func() {
		calls.Store(0)
		endpoint = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			user, password, ok := r.BasicAuth()
			if !ok || user != "discovery" || (password != testPassword && password != testNewPassword) {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if r.URL.Path == "/api/cluster" {
				fmt.Fprint(w, `{"version":{"full":"9.17.1"}}`)
			} else {
				fmt.Fprint(w, `{"records":[],"num_records":0}`)
			}
		}))
		DeferCleanup(endpoint.Close)
		store = newMemorySecretStore()
		var err error
		secretsServer, err = NewPrivateSecretsServer().SetLogger(logger).SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).SetSecretStore(store).Build()
		Expect(err).NotTo(HaveOccurred())
		backendServer, err = NewPrivateStorageBackendsServer().SetLogger(logger).SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).SetSecretsServer(secretsServer).
			SetRegistrationProbe(NewOntapRegistrationProbe(endpoint.Client())).Build()
		Expect(err).NotTo(HaveOccurred())
	})
	object := func() *privatev1.StorageBackend {
		return privatev1.StorageBackend_builder{
			Metadata: privatev1.Metadata_builder{Name: "netapp"}.Build(),
			Spec: privatev1.StorageBackendSpec_builder{Provider: "ontap", Endpoint: endpoint.URL,
				Credentials: privatev1.StorageBackendCredentials_builder{Username: "discovery", Password: testPassword}.Build(),
			}.Build(),
		}.Build()
	}
	create := func(obj *privatev1.StorageBackend) (*privatev1.StorageBackendsCreateResponse, error) {
		return backendServer.Create(ctx, privatev1.StorageBackendsCreateRequest_builder{Object: obj}.Build())
	}
	createSecret := func() *privatev1.Secret {
		response, err := secretsServer.Create(ctx, privatev1.SecretsCreateRequest_builder{Object: privatev1.Secret_builder{
			Metadata: privatev1.Metadata_builder{Name: "discovery-password", Tenant: auth.SharedTenant}.Build(),
			Backend:  privatev1.SecretBackend_SECRET_BACKEND_VAULT, Type: privatev1.SecretType_SECRET_TYPE_VALUE,
			Data: map[string][]byte{"value": []byte(testPassword)},
		}.Build()}.Build())
		Expect(err).NotTo(HaveOccurred())
		return response.GetObject()
	}
	It("persists READY only after successful discovery", func() {
		response, err := create(object())
		Expect(err).NotTo(HaveOccurred())
		Expect(calls.Load()).To(Equal(int32(7)))
		Expect(response.GetObject().GetStatus().GetState()).To(Equal(privatev1.StorageBackendState_STORAGE_BACKEND_STATE_READY))
		obj := object()
		obj.GetSpec().GetCredentials().SetPassword(testOtherPassword)
		_, err = create(obj)
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		list, err := backendServer.List(ctx, &privatev1.StorageBackendsListRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(list.GetItems()).To(HaveLen(1))
	})
	It("probes an authorized VALUE Secret without persisting its value", func() {
		secret := createSecret()
		obj := object()
		creds := obj.GetSpec().GetCredentials()
		creds.SetPassword("")
		creds.SetPasswordSecret(privatev1.SecretLocalReference_builder{Name: secret.GetMetadata().GetName()}.Build())
		response, err := create(obj)
		Expect(err).NotTo(HaveOccurred())
		Expect(calls.Load()).To(Equal(int32(7)))
		Expect(response.GetObject().GetSpec().GetCredentials().GetPassword()).To(BeEmpty())
		Expect(response.GetObject().GetSpec().GetCredentials().GetPasswordSecret().GetId()).To(Equal(secret.GetId()))
	})
	It("honors platform Secret read authorization before probing", func() {
		secret := createSecret()
		restricted := auth.NewMockTenancyLogic(ctrl)
		restricted.EXPECT().DetermineAssignableTenants(gomock.Any()).Return(collections.NewSet(testTenant), nil).AnyTimes()
		restricted.EXPECT().DetermineVisibility(gomock.Any()).Return(auth.TotalVisibility(), nil).AnyTimes()
		reader, err := NewPrivateSecretsServer().SetLogger(logger).SetAttributionLogic(attribution).
			SetTenancyLogic(restricted).SetSecretStore(store).Build()
		Expect(err).NotTo(HaveOccurred())
		backendServer, err = NewPrivateStorageBackendsServer().SetLogger(logger).SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).SetSecretsServer(reader).SetRegistrationProbe(NewOntapRegistrationProbe(endpoint.Client())).Build()
		Expect(err).NotTo(HaveOccurred())
		obj := object()
		obj.GetSpec().GetCredentials().SetPassword("")
		obj.GetSpec().GetCredentials().SetPasswordSecret(privatev1.SecretLocalReference_builder{Id: secret.GetId()}.Build())
		_, err = create(obj)
		Expect(status.Code(err)).To(Equal(codes.PermissionDenied))
		Expect(calls.Load()).To(BeZero())
	})
	It("probes effective masked credentials and leaves the record unchanged on failure", func() {
		response, err := create(object())
		Expect(err).NotTo(HaveOccurred())
		saved := response.GetObject()
		partial := privatev1.StorageBackend_builder{Id: saved.GetId(), Spec: privatev1.StorageBackendSpec_builder{
			Credentials: privatev1.StorageBackendCredentials_builder{Password: testOtherPassword}.Build(),
		}.Build()}.Build()
		request := privatev1.StorageBackendsUpdateRequest_builder{Object: partial,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.credentials.password"}},
		}.Build()
		_, err = backendServer.Update(ctx, request)
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		current, err := backendServer.Get(ctx, privatev1.StorageBackendsGetRequest_builder{Id: saved.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(proto.Equal(saved, current.GetObject())).To(BeTrue())
		partial.GetSpec().GetCredentials().SetPassword(testNewPassword)
		updated, err := backendServer.Update(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(updated.GetObject().GetSpec().GetCredentials().GetUsername()).To(Equal("discovery"))
		Expect(calls.Load()).To(Equal(int32(15)))
	})
	It("permits description and no-op updates without another probe", func() {
		response, err := create(object())
		Expect(err).NotTo(HaveOccurred())
		partial := privatev1.StorageBackend_builder{Id: response.GetObject().GetId(),
			Spec: privatev1.StorageBackendSpec_builder{Description: "Updated"}.Build(),
		}.Build()
		_, err = backendServer.Update(ctx, privatev1.StorageBackendsUpdateRequest_builder{Object: partial,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.description"}},
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		_, err = backendServer.Update(ctx, privatev1.StorageBackendsUpdateRequest_builder{Object: partial,
			UpdateMask: &fieldmaskpb.FieldMask{},
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(calls.Load()).To(Equal(int32(7)))
	})
	It("rejects changing the registered ONTAP endpoint", func() {
		response, err := create(object())
		Expect(err).NotTo(HaveOccurred())
		obj := proto.Clone(response.GetObject()).(*privatev1.StorageBackend)
		obj.GetSpec().SetEndpoint("https://other.example.com")
		_, err = backendServer.Update(ctx, privatev1.StorageBackendsUpdateRequest_builder{Object: obj,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec"}},
		}.Build())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(calls.Load()).To(Equal(int32(7)))
	})
	DescribeTable("rejects unusable password references without array calls", func(problem string, code codes.Code) {
		secret := createSecret()
		ref := privatev1.SecretLocalReference_builder{Id: secret.GetId()}.Build()
		switch problem {
		case "missing":
			ref.SetId("missing")
		case "empty-reference":
			ref.SetId("")
		case "empty-value":
			Expect(store.Store(ctx, auth.SharedTenant, "", secret.GetMetadata().GetName(), map[string][]byte{})).To(Succeed())
		default:
			secretsDAO, err := dao.NewGenericDAO[*privatev1.Secret]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
			Expect(err).NotTo(HaveOccurred())
			if problem == "wrong-type" {
				secret.SetType(privatev1.SecretType_SECRET_TYPE_OPAQUE)
			}
			if problem == "deleted" {
				secret.GetMetadata().SetFinalizers([]string{"test.osac.openshift.io/hold"})
				_, err = secretsDAO.Update().SetObject(secret).Do(ctx)
				Expect(err).NotTo(HaveOccurred())
				_, err = secretsDAO.Delete().SetId(secret.GetId()).Do(ctx)
				Expect(err).NotTo(HaveOccurred())
				deleting, getErr := secretsDAO.Get().SetId(secret.GetId()).Do(ctx)
				Expect(getErr).NotTo(HaveOccurred())
				Expect(deleting.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeTrue())
			} else {
				_, err = secretsDAO.Update().SetObject(secret).Do(ctx)
			}
			Expect(err).NotTo(HaveOccurred())
		}
		obj := object()
		obj.GetSpec().GetCredentials().SetPassword("")
		obj.GetSpec().GetCredentials().SetPasswordSecret(ref)
		_, err := create(obj)
		Expect(status.Code(err)).To(Equal(code))
		Expect(calls.Load()).To(BeZero())
		list, err := backendServer.List(ctx, &privatev1.StorageBackendsListRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(list.GetItems()).To(BeEmpty())
	},
		Entry("missing", "missing", codes.InvalidArgument),
		Entry("empty reference", "empty-reference", codes.InvalidArgument),
		Entry("empty value", "empty-value", codes.InvalidArgument),
		Entry("wrong type", "wrong-type", codes.InvalidArgument),
		Entry("deleted", "deleted", codes.InvalidArgument),
	)
	It("switches credential sources only when the old source is cleared", func() {
		secret := createSecret()
		created, err := create(object())
		Expect(err).NotTo(HaveOccurred())
		partial := privatev1.StorageBackend_builder{Id: created.GetObject().GetId(),
			Spec: privatev1.StorageBackendSpec_builder{Credentials: privatev1.StorageBackendCredentials_builder{
				PasswordSecret: privatev1.SecretLocalReference_builder{Id: secret.GetId()}.Build(),
			}.Build()}.Build(),
		}.Build()
		request := privatev1.StorageBackendsUpdateRequest_builder{Object: partial,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.credentials.password_secret"}},
		}.Build()
		_, err = backendServer.Update(ctx, request)
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(calls.Load()).To(Equal(int32(7)))
		request.SetUpdateMask(&fieldmaskpb.FieldMask{Paths: []string{"spec.credentials.password", "spec.credentials.password_secret"}})
		updated, err := backendServer.Update(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(updated.GetObject().GetSpec().GetCredentials().GetPassword()).To(BeEmpty())
		Expect(updated.GetObject().GetSpec().GetCredentials().GetPasswordSecret().GetName()).To(Equal(secret.GetMetadata().GetName()))
		partial.GetSpec().SetCredentials(privatev1.StorageBackendCredentials_builder{Username: "discovery", Password: testNewPassword}.Build())
		request.SetUpdateMask(&fieldmaskpb.FieldMask{Paths: []string{"spec.credentials"}})
		updated, err = backendServer.Update(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(updated.GetObject().GetSpec().GetCredentials().GetPasswordSecret()).To(BeNil())
		Expect(calls.Load()).To(Equal(int32(21)))
	})
	It("checks the requested version before a credential probe", func() {
		created, err := create(object())
		Expect(err).NotTo(HaveOccurred())
		obj := proto.Clone(created.GetObject()).(*privatev1.StorageBackend)
		obj.GetMetadata().SetVersion(obj.GetMetadata().GetVersion() + 1)
		obj.GetSpec().GetCredentials().SetPassword(testNewPassword)
		_, err = backendServer.Update(ctx, privatev1.StorageBackendsUpdateRequest_builder{Object: obj, Lock: true,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.credentials"}},
		}.Build())
		Expect(status.Code(err)).To(Equal(codes.Aborted))
		Expect(calls.Load()).To(Equal(int32(7)))
	})
	DescribeTable("allows concurrent requests while discovery is stalled and rejects stale persistence",
		func(operation string, expectedCode codes.Code) {
			created, err := create(object())
			Expect(err).NotTo(HaveOccurred())
			saved := created.GetObject()
			// Commit preparation so each public handler can run in its own request
			// transaction, matching the gRPC interceptor's real concurrency boundary.
			Expect(suiteTx.End(ctx)).To(Succeed())
			suiteTx, err = tm.Begin(ctx)
			Expect(err).NotTo(HaveOccurred())
			ctx = database.TxIntoContext(ctx, suiteTx)

			started := make(chan struct{})
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			DeferCleanup(unblock)
			backendServer.registrationProbe = ontapBackendProbeFunc(
				func(probeCtx context.Context, _, _, _ string) error {
					close(started)
					select {
					case <-release:
						return nil
					case <-probeCtx.Done():
						return probeCtx.Err()
					}
				})
			partial := privatev1.StorageBackend_builder{Id: saved.GetId(),
				Spec: privatev1.StorageBackendSpec_builder{
					Credentials: privatev1.StorageBackendCredentials_builder{Password: testNewPassword}.Build(),
				}.Build(),
			}.Build()
			request := privatev1.StorageBackendsUpdateRequest_builder{Object: partial,
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.credentials.password"}},
				Lock:       false,
			}.Build()
			finished := make(chan error, 1)
			go func() {
				defer GinkgoRecover()
				finished <- tm.Run(ctx, func(requestCtx context.Context) error {
					_, updateErr := backendServer.Update(requestCtx, request)
					return updateErr
				})
			}()
			Eventually(started).Should(BeClosed())

			concurrentCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			err = tm.Run(concurrentCtx, func(requestCtx context.Context) error {
				if operation == "delete" {
					_, deleteErr := backendServer.Delete(requestCtx,
						privatev1.StorageBackendsDeleteRequest_builder{Id: saved.GetId()}.Build())
					return deleteErr
				}
				_, updateErr := backendServer.Update(requestCtx, privatev1.StorageBackendsUpdateRequest_builder{
					Object: privatev1.StorageBackend_builder{Id: saved.GetId(),
						Spec: privatev1.StorageBackendSpec_builder{Description: "Concurrent update"}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.description"}},
				}.Build())
				return updateErr
			})
			Expect(err).NotTo(HaveOccurred())
			unblock()
			var updateErr error
			Eventually(finished).Should(Receive(&updateErr))
			Expect(status.Code(updateErr)).To(Equal(expectedCode))
			current, err := backendServer.Get(ctx, privatev1.StorageBackendsGetRequest_builder{Id: saved.GetId()}.Build())
			if operation == "delete" {
				Expect(status.Code(err)).To(Equal(codes.NotFound))
			} else {
				Expect(err).NotTo(HaveOccurred())
				Expect(current.GetObject().GetSpec().GetCredentials().GetPassword()).To(Equal(testPassword))
				Expect(current.GetObject().GetSpec().GetDescription()).To(Equal("Concurrent update"))
			}
		},
		Entry("description update", "update", codes.Aborted),
		Entry("delete", "delete", codes.NotFound),
	)
})
