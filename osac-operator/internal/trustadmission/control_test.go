package trustadmission_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/osac-project/osac/osac-operator/internal/trustadmission"
)

var _ = Describe("ControlHandler", func() {
	var (
		now     time.Time
		record  trustadmission.ExpectedBundle
		store   trustadmission.Store
		client  *fake.Clientset
		handler *trustadmission.ControlHandler
		name    string
	)

	BeforeEach(func() {
		now = time.Date(2026, time.September, 27, 12, 0, 0, 0, time.UTC)
		record = expectedBundle(certificatePEM(), now.Add(5*time.Minute))
		var err error
		name, err = trustadmission.PublisherServiceAccountName(clusterOrderUID)
		Expect(err).NotTo(HaveOccurred())
		client = fake.NewSimpleClientset(&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: tenantNamespace, UID: "publisher-sa-uid",
			Annotations: map[string]string{
				trustadmission.TenantAnnotation: tenantName, trustadmission.OwnerReferenceAnnotation: clusterOrderUID,
			},
		}})
		client.PrependReactor("create", "tokenreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
			return true, &authenticationv1.TokenReview{Status: authenticationv1.TokenReviewStatus{
				Authenticated: true,
				User: authenticationv1.UserInfo{
					Username: "system:serviceaccount:" + tenantNamespace + ":" + name,
					UID:      "publisher-sa-uid",
				},
			}}, nil
		})
		store = trustadmission.NewStore()
		handler = &trustadmission.ControlHandler{
			Store: store, Kube: client, Namespace: tenantNamespace, Now: func() time.Time { return now },
		}
	})

	request := func(method string, body any) *httptest.ResponseRecorder {
		encoded, err := json.Marshal(body)
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
		req := httptest.NewRequest(method, trustadmission.ControlPath, bytes.NewReader(encoded))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(trustadmission.ControlTokenHeader, "publisher-token")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}

	It("publishes and revokes only an authorized order's record", func() {
		response := request(http.MethodPost, trustadmission.ControlPublishRequest{Record: record})
		Expect(response.Code).To(Equal(http.StatusNoContent))
		Expect(store.AuthorizeConfigMap(context.Background(), configMapFor(record), now)).To(Succeed())

		response = request(http.MethodDelete, trustadmission.ControlRevokeRequest{Key: record.Key})
		Expect(response.Code).To(Equal(http.StatusNoContent))
		Expect(store.AuthorizeConfigMap(context.Background(), configMapFor(record), now)).NotTo(Succeed())
	})

	It("rejects a publish record without tenant attribution", func() {
		record.Tenant = ""
		response := request(http.MethodPost, trustadmission.ControlPublishRequest{Record: record})
		Expect(response.Code).To(Equal(http.StatusForbidden))
		Expect(store.AuthorizeConfigMap(context.Background(), configMapFor(record), now)).NotTo(Succeed())
	})

	It("rejects a publish record with a mismatched owner reference", func() {
		record.OwnerReference = "another-order"
		response := request(http.MethodPost, trustadmission.ControlPublishRequest{Record: record})
		Expect(response.Code).To(Equal(http.StatusForbidden))
		Expect(store.AuthorizeConfigMap(context.Background(), configMapFor(record), now)).NotTo(Succeed())
	})

	It("rejects a record for another order even with a valid publisher token", func() {
		record.Key.ClusterOrderUID = "different-order"
		record.OwnerReference = record.Key.ClusterOrderUID
		response := request(http.MethodPost, trustadmission.ControlPublishRequest{Record: record})
		Expect(response.Code).To(Equal(http.StatusForbidden))
	})

	It("rejects a tenant mismatch", func() {
		record.Tenant = "other-tenant"
		response := request(http.MethodPost, trustadmission.ControlPublishRequest{Record: record})
		Expect(response.Code).To(Equal(http.StatusForbidden))
	})

	It("rejects tokens for a different service account UID", func() {
		client.PrependReactor("create", "tokenreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, &authenticationv1.TokenReview{Status: authenticationv1.TokenReviewStatus{
				Authenticated: true,
				User: authenticationv1.UserInfo{
					Username: "system:serviceaccount:" + tenantNamespace + ":" + name,
					UID:      "another-sa-uid",
				},
			}}, nil
		})
		response := request(http.MethodPost, trustadmission.ControlPublishRequest{Record: record})
		Expect(response.Code).To(Equal(http.StatusForbidden))
	})

	It("rejects an unbounded record lifetime", func() {
		record.ExpiresAt = now.Add(11 * time.Minute)
		response := request(http.MethodPost, trustadmission.ControlPublishRequest{Record: record})
		Expect(response.Code).To(Equal(http.StatusBadRequest))
	})

	It("rejects a tokenless request without changing the store", func() {
		encoded, err := json.Marshal(trustadmission.ControlPublishRequest{Record: record})
		Expect(err).NotTo(HaveOccurred())
		req := httptest.NewRequest(http.MethodPost, trustadmission.ControlPath, bytes.NewReader(encoded))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		Expect(response.Code).To(Equal(http.StatusForbidden))
		Expect(store.AuthorizeConfigMap(context.Background(), configMapFor(record), now)).NotTo(Succeed())
	})
})
