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
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"

	"github.com/gophercloud/gophercloud/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/bare-metal-fulfillment-operator/internal/management"
)

const (
	testNodeID = "test-node-123"
)

// mockIronicServer creates a test HTTP server that mocks Ironic API responses
func mockIronicServer(handler http.HandlerFunc) *httptest.Server {
	return httptest.NewServer(handler)
}

// newMockOpenStackClient creates an OpenStack management client with a mocked service endpoint
func newMockOpenStackClient(server *httptest.Server) *management.OpenStackClient {
	// Create a minimal service client for testing
	serviceClient := &gophercloud.ServiceClient{
		ProviderClient: &gophercloud.ProviderClient{},
		Endpoint:       server.URL + "/v1/",
	}

	client := &management.OpenStackClient{}
	client.SetServiceClientForTest(serviceClient)
	return client
}

var _ = Describe("OpenStack Management Backend", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	Describe("TriggerRestart", func() {
		It("sends reboot command to Ironic API", func() {
			var capturedRequest *http.Request
			var capturedBody string

			server := mockIronicServer(func(w http.ResponseWriter, r *http.Request) {
				capturedRequest = r

				// Capture the request body
				if r.Body != nil {
					bodyBytes, err := io.ReadAll(r.Body)
					Expect(err).NotTo(HaveOccurred())
					capturedBody = string(bodyBytes)
				}

				// Return success response
				w.WriteHeader(http.StatusAccepted)
				_, err := w.Write([]byte(`{"target": "reboot"}`))
				Expect(err).NotTo(HaveOccurred())
			})
			defer server.Close()

			client := newMockOpenStackClient(server)

			err := client.TriggerRestart(ctx, testNodeID)
			Expect(err).NotTo(HaveOccurred())

			// Verify the API call was made correctly
			expectedPath := "/v1/nodes/" + testNodeID + "/states/power"
			Expect(capturedRequest.URL.Path).To(Equal(expectedPath))
			Expect(capturedRequest.Method).To(Equal("PUT"))
			Expect(capturedBody).To(MatchJSON(`{"target": "reboot"}`))
		})

		It("returns ErrTransitioning on 409 Conflict response", func() {
			server := mockIronicServer(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusConflict)
				_, err := w.Write([]byte(`{"error_message": {"faultstring": "Node is locked"}}`))
				Expect(err).NotTo(HaveOccurred())
			})
			defer server.Close()

			client := newMockOpenStackClient(server)

			err := client.TriggerRestart(ctx, testNodeID)
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, management.ErrTransitioning)).To(BeTrue())
		})

		It("handles authentication errors with reconnect attempt", func() {
			callCount := 0
			server := mockIronicServer(func(w http.ResponseWriter, r *http.Request) {
				callCount++
				if callCount == 1 {
					// First call returns 401
					w.WriteHeader(http.StatusUnauthorized)
					_, err := w.Write([]byte(`{"error_message": {"faultstring": "Unauthorized"}}`))
					Expect(err).NotTo(HaveOccurred())
				} else {
					// Second call succeeds
					w.WriteHeader(http.StatusAccepted)
					_, err := w.Write([]byte(`{"target": "reboot"}`))
					Expect(err).NotTo(HaveOccurred())
				}
			})
			defer server.Close()

			client := newMockOpenStackClient(server)

			err := client.TriggerRestart(ctx, testNodeID)
			Expect(err).NotTo(HaveOccurred())
			Expect(callCount).To(Equal(2))
		})

		It("returns error on server failure", func() {
			server := mockIronicServer(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, err := w.Write([]byte(`{"error_message": {"faultstring": "Internal server error"}}`))
				Expect(err).NotTo(HaveOccurred())
			})
			defer server.Close()

			client := newMockOpenStackClient(server)

			err := client.TriggerRestart(ctx, testNodeID)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to trigger restart on node"))
		})

		It("returns error when reconnect fails after auth error", func() {
			server := mockIronicServer(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				_, err := w.Write([]byte(`{"error_message": {"faultstring": "Unauthorized"}}`))
				Expect(err).NotTo(HaveOccurred())
			})
			defer server.Close()

			client := newMockOpenStackClient(server)
			// Override the newServiceClient to simulate reconnect failure
			client.NewServiceClientForTest(func(ctx context.Context) (*gophercloud.ServiceClient, error) {
				return nil, fmt.Errorf("keystone unavailable")
			})

			err := client.TriggerRestart(ctx, testNodeID)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("reconnect failed"))
			Expect(err.Error()).To(ContainSubstring("keystone unavailable"))
		})

		It("returns ErrTransitioning when retry after reconnect gets 409", func() {
			callCount := 0
			server := mockIronicServer(func(w http.ResponseWriter, r *http.Request) {
				callCount++
				if callCount == 1 {
					// First call returns 401
					w.WriteHeader(http.StatusUnauthorized)
					_, err := w.Write([]byte(`{"error_message": {"faultstring": "Unauthorized"}}`))
					Expect(err).NotTo(HaveOccurred())
				} else {
					// Second call after reconnect returns 409
					w.WriteHeader(http.StatusConflict)
					_, err := w.Write([]byte(`{"error_message": {"faultstring": "Node is locked"}}`))
					Expect(err).NotTo(HaveOccurred())
				}
			})
			defer server.Close()

			client := newMockOpenStackClient(server)

			err := client.TriggerRestart(ctx, testNodeID)
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, management.ErrTransitioning)).To(BeTrue())
			Expect(callCount).To(Equal(2))
		})
	})

	Describe("IsRestartComplete", func() {
		It("returns true when node is not transitioning", func() {
			server := mockIronicServer(func(w http.ResponseWriter, r *http.Request) {
				// Mock node in stable power state (not transitioning)
				nodeResponse := `{
					"uuid": "` + testNodeID + `",
					"power_state": "power on",
					"target_power_state": null
				}`
				w.WriteHeader(http.StatusOK)
				_, err := w.Write([]byte(nodeResponse))
				Expect(err).NotTo(HaveOccurred())
			})
			defer server.Close()

			client := newMockOpenStackClient(server)

			complete, err := client.IsRestartComplete(ctx, testNodeID)
			Expect(err).NotTo(HaveOccurred())
			Expect(complete).To(BeTrue())
		})

		It("returns false when node is transitioning", func() {
			server := mockIronicServer(func(w http.ResponseWriter, r *http.Request) {
				// Mock node in transitioning state
				nodeResponse := `{
					"uuid": "` + testNodeID + `",
					"power_state": "power on",
					"target_power_state": "reboot"
				}`
				w.WriteHeader(http.StatusOK)
				_, err := w.Write([]byte(nodeResponse))
				Expect(err).NotTo(HaveOccurred())
			})
			defer server.Close()

			client := newMockOpenStackClient(server)

			complete, err := client.IsRestartComplete(ctx, testNodeID)
			Expect(err).NotTo(HaveOccurred())
			Expect(complete).To(BeFalse())
		})

		It("returns error when node does not exist", func() {
			server := mockIronicServer(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				_, err := w.Write([]byte(`{"error_message": {"faultstring": "Node not found"}}`))
				Expect(err).NotTo(HaveOccurred())
			})
			defer server.Close()

			client := newMockOpenStackClient(server)

			_, err := client.IsRestartComplete(ctx, "nonexistent-node")
			Expect(err).To(HaveOccurred())
		})

		It("handles authentication errors with reconnect attempt", func() {
			callCount := 0
			server := mockIronicServer(func(w http.ResponseWriter, r *http.Request) {
				callCount++
				if callCount == 1 {
					// First call returns 401
					w.WriteHeader(http.StatusUnauthorized)
					_, err := w.Write([]byte(`{"error_message": {"faultstring": "Unauthorized"}}`))
					Expect(err).NotTo(HaveOccurred())
				} else {
					// Second call succeeds
					nodeResponse := `{
						"uuid": "` + testNodeID + `",
						"power_state": "power on",
						"target_power_state": null
					}`
					w.WriteHeader(http.StatusOK)
					_, err := w.Write([]byte(nodeResponse))
					Expect(err).NotTo(HaveOccurred())
				}
			})
			defer server.Close()

			client := newMockOpenStackClient(server)

			complete, err := client.IsRestartComplete(ctx, testNodeID)
			Expect(err).NotTo(HaveOccurred())
			Expect(complete).To(BeTrue())
			Expect(callCount).To(Equal(2))
		})

		It("returns error on server failure", func() {
			server := mockIronicServer(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, err := w.Write([]byte(`{"error_message": {"faultstring": "Internal server error"}}`))
				Expect(err).NotTo(HaveOccurred())
			})
			defer server.Close()

			client := newMockOpenStackClient(server)

			_, err := client.IsRestartComplete(ctx, testNodeID)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("Registration", func() {
		It("openstack backend is registered", func() {
			client, err := management.NewClient(context.Background(), &management.Config{
				Type: "openstack",
			})
			// Backend should be registered (client created successfully or with auth error, not "unsupported backend" error)
			if err != nil {
				Expect(err.Error()).NotTo(ContainSubstring(`unsupported management backend type: "openstack"`))
			} else {
				Expect(client).NotTo(BeNil())
			}
		})
	})
})
