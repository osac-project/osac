/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package management_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	"k8s.io/client-go/rest"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/management"
)

// These specs pin the remote-mode routing of the registry constructor: with
// RESTConfig set, the backend dials that endpoint, never the ambient
// in-cluster config. The server answers the discovery calls the client's
// REST mapper makes and returns NotFound for the BareMetalHost read.
var _ = Describe("Metal3 management remote REST config", func() {
	newRemoteConfigServer := func(requests *atomic.Int64) *httptest.Server {
		mux := http.NewServeMux()
		mux.HandleFunc("/apis", func(w http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"kind":"APIGroupList","apiVersion":"v1","groups":[{"name":"metal3.io",` + //nolint:errcheck
				`"versions":[{"groupVersion":"metal3.io/v1alpha1","version":"v1alpha1"}],` +
				`"preferredVersion":{"groupVersion":"metal3.io/v1alpha1","version":"v1alpha1"}}]}`))
		})
		mux.HandleFunc("/apis/metal3.io/v1alpha1", func(w http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"kind":"APIResourceList","apiVersion":"v1","groupVersion":"metal3.io/v1alpha1",` + //nolint:errcheck
				`"resources":[{"name":"baremetalhosts","singularName":"baremetalhost","namespaced":true,` +
				`"kind":"BareMetalHost","verbs":["get","list","watch","create","update","patch","delete"]}]}`))
		})
		mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure","message":"not found",` + //nolint:errcheck
				`"reason":"NotFound","code":404}`))
		})
		return httptest.NewTLSServer(mux)
	}

	It("reads BareMetalHosts from the configured endpoint", func() {
		var requests atomic.Int64
		server := newRemoteConfigServer(&requests)
		DeferCleanup(server.Close)

		cfg := &management.Config{
			Type: "metal3",
			Options: map[string]any{
				"metal3": map[string]any{"namespace": metal3TestNamespace},
			},
			RESTConfig: &rest.Config{
				Host:            server.URL,
				TLSClientConfig: rest.TLSClientConfig{Insecure: true},
			},
		}

		metal3Client, err := management.NewMetal3ManagementClient(context.Background(), cfg)
		Expect(err).NotTo(HaveOccurred())

		_, err = metal3Client.GetPowerState(context.Background(), metal3TestNamespace+"/host-remote")

		Expect(err).To(MatchError(ContainSubstring("failed to get BareMetalHost")))
		Expect(requests.Load()).To(BeNumerically(">", 0))
	})
})
