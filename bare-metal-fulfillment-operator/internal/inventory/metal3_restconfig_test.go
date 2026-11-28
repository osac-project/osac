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

package inventory

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	"k8s.io/client-go/rest"
)

// These specs pin the remote-mode routing of the registry constructor: with
// RESTConfig set, the backend dials that endpoint, never the ambient
// in-cluster config.
var _ = Describe("Metal3 remote REST config", func() {
	It("returns the operator-supplied override unchanged", func() {
		override := &rest.Config{Host: "https://outer.example.test:6443"}

		resolved, err := resolveRestConfig(override)

		Expect(err).NotTo(HaveOccurred())
		Expect(resolved).To(BeIdenticalTo(override))
	})

	It("runs BareMetalHost CRD discovery against the configured endpoint", func() {
		var discoveryRequests atomic.Int64
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			discoveryRequests.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"kind":"APIResourceList","apiVersion":"v1",` + //nolint:errcheck
				`"groupVersion":"metal3.io/v1alpha1","resources":[]}`))
		}))
		DeferCleanup(server.Close)

		cfg := &Config{
			Type:      testHostClass,
			HostClass: testHostClass,
			Options: map[string]any{
				testHostClass: map[string]any{"namespace": testNamespace},
			},
			RESTConfig: &rest.Config{
				Host:            server.URL,
				TLSClientConfig: rest.TLSClientConfig{Insecure: true},
			},
		}

		remoteClient, err := NewMetal3Client(context.Background(), cfg)

		Expect(err).NotTo(HaveOccurred())
		Expect(remoteClient).NotTo(BeNil())
		Expect(discoveryRequests.Load()).To(BeNumerically(">", 0))
	})
})
