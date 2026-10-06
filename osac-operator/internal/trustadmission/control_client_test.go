package trustadmission_test

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	"k8s.io/client-go/rest"

	"github.com/osac-project/osac/osac-operator/internal/trustadmission"
)

var _ = Describe("ControlClient", func() {
	It("publishes and revokes through the tenant API service proxy using a distinct control header", func() {
		calls := 0
		server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			defer GinkgoRecover()
			calls++
			Expect(request.URL.Path).To(Equal("/api/v1/namespaces/osac-csi/services/https:osac-fulfillment-trust-admission:https/proxy/control/v1/records"))
			Expect(request.Header.Get("Authorization")).To(Equal("Bearer publisher-token"))
			Expect(request.Header.Get(trustadmission.ControlTokenHeader)).To(Equal("publisher-token"))
			if calls == 1 {
				Expect(request.Method).To(Equal(http.MethodPost))
				var payload trustadmission.ControlPublishRequest
				Expect(json.NewDecoder(request.Body).Decode(&payload)).To(Succeed())
				Expect(payload.Record.Key.ClusterOrderUID).To(Equal(clusterOrderUID))
			} else {
				Expect(request.Method).To(Equal(http.MethodDelete))
				var payload trustadmission.ControlRevokeRequest
				Expect(json.NewDecoder(request.Body).Decode(&payload)).To(Succeed())
				Expect(payload.Key.ClusterOrderUID).To(Equal(clusterOrderUID))
			}
			writer.WriteHeader(http.StatusNoContent)
		}))
		defer server.Close()
		client, err := trustadmission.NewControlClient(&rest.Config{Host: server.URL,
			BearerToken: "publisher-token", TLSClientConfig: rest.TLSClientConfig{
				CAData: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}),
			}}, tenantNamespace)
		Expect(err).NotTo(HaveOccurred())
		record := expectedBundle(certificatePEM(), time.Now().Add(5*time.Minute))
		Expect(client.Publish(context.Background(), record)).To(Succeed())
		Expect(client.Revoke(context.Background(), record.Key)).To(Succeed())
		Expect(calls).To(Equal(2))
	})

	It("fails closed on an unauthorized control response without exposing credentials", func() {
		server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusForbidden)
			_, _ = writer.Write([]byte("publisher-token"))
		}))
		defer server.Close()
		client, err := trustadmission.NewControlClient(&rest.Config{Host: server.URL,
			BearerToken: "publisher-token", TLSClientConfig: rest.TLSClientConfig{
				CAData: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}),
			}}, tenantNamespace)
		Expect(err).NotTo(HaveOccurred())
		record := expectedBundle(certificatePEM(), time.Now().Add(5*time.Minute))
		err = client.Publish(context.Background(), record)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("HTTP 403"))
		Expect(err.Error()).NotTo(ContainSubstring("publisher-token"))
	})
})
