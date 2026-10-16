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
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("ONTAP backend registration", func() {
	var backendServer *PrivateStorageBackendsServer
	var secretsServer *PrivateSecretsServer
	var store *memorySecretStore
	var endpoint *httptest.Server
	var calls atomic.Int32
	BeforeEach(func() {
		calls.Store(0)
		endpoint = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		endpoint.Config.ConnState = func(_ net.Conn, state http.ConnState) {
			if state == http.StateNew {
				calls.Add(1)
			}
		}
		endpoint.StartTLS()
		DeferCleanup(endpoint.Close)
		store = newMemorySecretStore()
		var err error
		secretsServer, err = NewPrivateSecretsServer().SetLogger(logger).SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).SetSecretStore(store).Build()
		Expect(err).NotTo(HaveOccurred())
		backendServer, err = NewPrivateStorageBackendsServer().SetLogger(logger).SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).Build()
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
	It("accepts configuration without contacting ONTAP", func() {
		response, err := create(object())
		Expect(err).NotTo(HaveOccurred())
		Expect(response.GetObject().GetStatus().GetState()).To(Equal(privatev1.StorageBackendState_STORAGE_BACKEND_STATE_READY))
		Expect(calls.Load()).To(BeZero())
	})
	It("accepts a VALUE Secret reference without reading or copying its protected value", func() {
		secret := createSecret()
		Expect(store.Delete(ctx, auth.SharedTenant, "", secret.GetMetadata().GetName())).To(Succeed())
		obj := object()
		creds := obj.GetSpec().GetCredentials()
		creds.SetPassword("")
		creds.SetPasswordSecret(privatev1.SecretLocalReference_builder{Name: secret.GetMetadata().GetName()}.Build())
		response, err := create(obj)
		Expect(err).NotTo(HaveOccurred())
		Expect(calls.Load()).To(BeZero())
		Expect(response.GetObject().GetSpec().GetCredentials().GetPassword()).To(BeEmpty())
		Expect(response.GetObject().GetSpec().GetCredentials().GetPasswordSecret().GetId()).To(Equal(secret.GetId()))
	})
	It("persists effective masked credentials without contacting ONTAP", func() {
		response, err := create(object())
		Expect(err).NotTo(HaveOccurred())
		saved := response.GetObject()
		partial := privatev1.StorageBackend_builder{Id: saved.GetId(), Spec: privatev1.StorageBackendSpec_builder{
			Credentials: privatev1.StorageBackendCredentials_builder{Password: testOtherPassword}.Build(),
		}.Build()}.Build()
		request := privatev1.StorageBackendsUpdateRequest_builder{Object: partial,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.credentials.password"}},
		}.Build()
		failed, err := backendServer.Update(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(failed.GetObject().GetStatus().GetState()).To(Equal(privatev1.StorageBackendState_STORAGE_BACKEND_STATE_READY))
		current, err := backendServer.Get(ctx, privatev1.StorageBackendsGetRequest_builder{Id: saved.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(current.GetObject().GetSpec().GetCredentials().GetPassword()).To(Equal(testOtherPassword))
		Expect(current.GetObject().GetStatus().GetMessage()).To(BeEmpty())
		partial.GetSpec().GetCredentials().SetPassword(testNewPassword)
		updated, err := backendServer.Update(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(updated.GetObject().GetSpec().GetCredentials().GetUsername()).To(Equal("discovery"))
		Expect(updated.GetObject().GetStatus().GetState()).To(Equal(privatev1.StorageBackendState_STORAGE_BACKEND_STATE_READY))
		Expect(updated.GetObject().GetStatus().GetMessage()).To(BeEmpty())
		Expect(calls.Load()).To(BeZero())
	})
	It("preserves configuration on description and unchanged updates without contacting ONTAP", func() {
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
		Expect(calls.Load()).To(BeZero())
	})
	It("clears a previous connectivity error on an explicit update without contacting ONTAP", func() {
		created, err := create(object())
		Expect(err).NotTo(HaveOccurred())
		backend := created.GetObject()
		backend.SetStatus(privatev1.StorageBackendStatus_builder{
			State:   privatev1.StorageBackendState_STORAGE_BACKEND_STATE_ERROR,
			Message: proto.String("ONTAP management endpoint is unreachable"),
		}.Build())
		backendsDAO, err := dao.NewGenericDAO[*privatev1.StorageBackend]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).NotTo(HaveOccurred())
		_, err = backendsDAO.Update().SetObject(backend).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		updated, err := backendServer.Update(ctx, privatev1.StorageBackendsUpdateRequest_builder{
			Object:     privatev1.StorageBackend_builder{Id: backend.GetId()}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{},
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(updated.GetObject().GetStatus().GetState()).To(Equal(privatev1.StorageBackendState_STORAGE_BACKEND_STATE_READY))
		Expect(updated.GetObject().GetStatus().GetMessage()).To(BeEmpty())
		Expect(proto.Equal(backend.GetSpec(), updated.GetObject().GetSpec())).To(BeTrue())
		Expect(calls.Load()).To(BeZero())
	})
	DescribeTable("rejects malformed ONTAP endpoints before persistence", func(url string) {
		obj := object()
		obj.GetSpec().SetEndpoint(url)
		_, err := create(obj)
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(calls.Load()).To(BeZero())
		list, err := backendServer.List(ctx, &privatev1.StorageBackendsListRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(list.GetItems()).To(BeEmpty())
	},
		Entry("HTTP", "http://netapp.example.com"),
		Entry("embedded credentials", "https://user:password@netapp.example.com"),
		Entry("invalid port", "https://netapp.example.com:70000"),
		Entry("zero port", "https://netapp.example.com:0"),
		Entry("non-numeric port", "https://netapp.example.com:invalid"),
		Entry("path", "https://netapp.example.com/api"),
		Entry("query", "https://netapp.example.com?fields=*"),
		Entry("fragment", "https://netapp.example.com#fragment"),
		Entry("missing host", "https://"),
	)
	It("rejects changing the registered ONTAP endpoint", func() {
		response, err := create(object())
		Expect(err).NotTo(HaveOccurred())
		obj := proto.Clone(response.GetObject()).(*privatev1.StorageBackend)
		obj.GetSpec().SetEndpoint("https://other.example.com")
		_, err = backendServer.Update(ctx, privatev1.StorageBackendsUpdateRequest_builder{Object: obj,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec"}},
		}.Build())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(calls.Load()).To(BeZero())
	})
	DescribeTable("rejects unusable password references without array calls", func(problem string, code codes.Code) {
		secret := createSecret()
		ref := privatev1.SecretLocalReference_builder{Id: secret.GetId()}.Build()
		switch problem {
		case "missing":
			ref.SetId("missing")
		case "empty-reference":
			ref.SetId("")
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
		Expect(calls.Load()).To(BeZero())
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
		Expect(calls.Load()).To(BeZero())
	})
	It("rejects a stale credential update when optimistic locking is requested", func() {
		created, err := create(object())
		Expect(err).NotTo(HaveOccurred())
		obj := proto.Clone(created.GetObject()).(*privatev1.StorageBackend)
		obj.GetMetadata().SetVersion(obj.GetMetadata().GetVersion() + 1)
		obj.GetSpec().GetCredentials().SetPassword(testNewPassword)
		_, err = backendServer.Update(ctx, privatev1.StorageBackendsUpdateRequest_builder{Object: obj, Lock: true,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.credentials"}},
		}.Build())
		Expect(status.Code(err)).To(Equal(codes.Aborted))
		Expect(calls.Load()).To(BeZero())
	})
})
