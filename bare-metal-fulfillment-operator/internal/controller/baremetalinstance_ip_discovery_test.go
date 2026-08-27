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

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	opv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/aap"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

// newMockAAPServer creates an HTTP test server that mocks AAP API responses.
// Returns the server and an aap.Client configured to use it.
func newMockAAPServer(jobID string, artifacts json.RawMessage) (*httptest.Server, *aap.Client) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mock the GetJob endpoint: GET /v2/jobs/{id}/
		expectedPath := fmt.Sprintf("/v2/jobs/%s/", jobID)
		if r.Method == http.MethodGet && r.URL.Path == expectedPath {
			job := map[string]interface{}{
				"id":        1,
				"status":    "successful",
				"artifacts": artifacts,
			}
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(job); err != nil {
				http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
			}
			return
		}
		// Return 404 for unexpected requests
		http.Error(w, fmt.Sprintf("Not found: %s %s", r.Method, r.URL.Path), http.StatusNotFound)
	}))

	// Create aap.Client pointing to the test server
	client := aap.NewClient(server.URL, "fake-token", true)
	return server, client
}

var _ = Describe("BareMetalInstance IP Discovery", func() {
	var (
		ctx        context.Context
		reconciler *BareMetalInstanceReconciler
		bmi        *v1alpha1.BareMetalInstance
	)

	bmiForIPDiscovery := func(attachments []v1alpha1.BareMetalNetworkAttachment) *v1alpha1.BareMetalInstance {
		return &v1alpha1.BareMetalInstance{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "test-bmi-ipd-",
				Namespace:    "default",
			},
			Spec: v1alpha1.BareMetalInstanceSpec{
				HostType:           "test-host",
				ExternalHostID:     "host-456",
				HostClass:          "openstack",
				TemplateID:         "noop",
				NetworkAttachments: attachments,
			},
		}
	}

	BeforeEach(func() {
		ctx = context.Background()
	})

	Describe("reconcileIPDiscovery", func() {
		Context("when no network attachments are configured", func() {
			BeforeEach(func() {
				bmi = bmiForIPDiscovery(nil)
				Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
				reconciler = &BareMetalInstanceReconciler{
					Client:              k8sClient,
					Scheme:              k8sClient.Scheme(),
					IPDiscoveryProvider: &mockProvisioningProvider{},
				}
			})

			AfterEach(func() {
				Expect(k8sClient.Delete(ctx, bmi)).To(Succeed())
			})

			It("should skip with condition set to True/Skipped", func() {
				result, err := reconciler.reconcileIPDiscovery(ctx, bmi)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(BeZero())

				cond := bmi.GetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				Expect(cond.Reason).To(Equal("Skipped"))
			})
		})

		Context("when IPDiscoveryProvider is nil", func() {
			BeforeEach(func() {
				bmi = bmiForIPDiscovery([]v1alpha1.BareMetalNetworkAttachment{
					{SubnetRef: "subnet-1", Interface: "data-0", Primary: true},
				})
				Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
				// Mark network handoff as complete so IP discovery can run
				bmi.SetStatusCondition(
					v1alpha1.HostConditionNetworkHandoffComplete,
					metav1.ConditionTrue,
					"Succeeded",
					"Handoff complete",
				)
				reconciler = &BareMetalInstanceReconciler{
					Client:              k8sClient,
					Scheme:              k8sClient.Scheme(),
					IPDiscoveryProvider: nil,
				}
			})

			AfterEach(func() {
				Expect(k8sClient.Delete(ctx, bmi)).To(Succeed())
			})

			It("should skip with condition set to True/Skipped", func() {
				result, err := reconciler.reconcileIPDiscovery(ctx, bmi)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(BeZero())

				cond := bmi.GetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				Expect(cond.Reason).To(Equal("Skipped"))
			})
		})

		Context("when IP discovery succeeds", func() {
			var mockProvider *mockProvisioningProvider

			BeforeEach(func() {
				bmi = bmiForIPDiscovery([]v1alpha1.BareMetalNetworkAttachment{
					{SubnetRef: "subnet-1", Interface: "data-0", Primary: true},
				})
				Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
				// Mark network handoff as complete so IP discovery can run
				bmi.SetStatusCondition(
					v1alpha1.HostConditionNetworkHandoffComplete,
					metav1.ConditionTrue,
					"Succeeded",
					"Handoff complete",
				)

				mockProvider = &mockProvisioningProvider{}
				reconciler = &BareMetalInstanceReconciler{
					Client:                        k8sClient,
					Scheme:                        k8sClient.Scheme(),
					IPDiscoveryProvider:           mockProvider,
					AAPClient:                     nil, // no AAP client for unit tests
					ProvisionPollIntervalDuration: DefaultProvisionPollIntervalDuration,
				}
			})

			AfterEach(func() {
				Expect(k8sClient.Delete(ctx, bmi)).To(Succeed())
			})

			It("should set IPDiscoveryComplete to True and initialize statuses", func() {
				mockProvider.triggerProvisionFunc = func(_ context.Context, _ client.Object) (*provisioning.ProvisionResult, error) {
					return &provisioning.ProvisionResult{
						JobID:        "ipd-job-1",
						InitialState: opv1alpha1.JobStatePending,
					}, nil
				}
				mockProvider.getProvisionStatusFunc = func(_ context.Context, _ client.Object, _ string) (provisioning.ProvisionStatus, error) {
					return provisioning.ProvisionStatus{
						JobID: "ipd-job-1",
						State: opv1alpha1.JobStateSucceeded,
					}, nil
				}

				var foundCond *metav1.Condition
				for range 10 {
					_, _ = reconciler.reconcileIPDiscovery(ctx, bmi)
					foundCond = bmi.GetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete)
					if foundCond != nil && foundCond.Status == metav1.ConditionTrue {
						break
					}
					_ = k8sClient.Status().Update(ctx, bmi)
					Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), bmi)).To(Succeed())
				}

				Expect(foundCond).NotTo(BeNil())
				Expect(foundCond.Status).To(Equal(metav1.ConditionTrue))
				Expect(foundCond.Reason).To(Equal("Succeeded"))

				Expect(bmi.Status.NetworkAttachmentStatuses).To(HaveLen(1))
				Expect(bmi.Status.NetworkAttachmentStatuses[0].SubnetRef).To(Equal("subnet-1"))
				Expect(bmi.Status.NetworkAttachmentStatuses[0].Interface).To(Equal("data-0"))
				Expect(bmi.Status.NetworkAttachmentStatuses[0].Primary).To(BeTrue())
			})

			It("should track IP discovery jobs in status", func() {
				mockProvider.triggerProvisionFunc = func(_ context.Context, _ client.Object) (*provisioning.ProvisionResult, error) {
					return &provisioning.ProvisionResult{
						JobID:        "ipd-job-2",
						InitialState: opv1alpha1.JobStatePending,
					}, nil
				}
				mockProvider.getProvisionStatusFunc = func(_ context.Context, _ client.Object, _ string) (provisioning.ProvisionStatus, error) {
					return provisioning.ProvisionStatus{
						JobID: "ipd-job-2",
						State: opv1alpha1.JobStateSucceeded,
					}, nil
				}

				for range 10 {
					_, _ = reconciler.reconcileIPDiscovery(ctx, bmi)
					_ = k8sClient.Status().Update(ctx, bmi)
					Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), bmi)).To(Succeed())
				}

				Expect(bmi.Status.IPDiscoveryJobs).NotTo(BeEmpty())
				Expect(bmi.Status.IPDiscoveryJobs[0].JobID).To(Equal("ipd-job-2"))
			})

			It("populates tenant IP address after network handoff completes", func() {
				const tenantIP = "10.20.30.40"
				const jobID = "ipd-job-3"

				// Setup: BMI with attachments, fully handed off (NetworkHandoffComplete=True)
				bmi.SetStatusCondition(
					v1alpha1.HostConditionProvisionTemplateComplete,
					metav1.ConditionTrue,
					"Succeeded",
					"Provisioning completed",
				)
				bmi.SetStatusCondition(
					v1alpha1.HostConditionNetworkAttachmentsReady,
					metav1.ConditionTrue,
					"Succeeded",
					"Network attachments ready",
				)

				// Create mock AAP server that returns artifacts with tenant IP
				leaseResult := DHCPLeaseResult{
					Leases: []DHCPLease{
						{
							SubnetRef:  "subnet-1",
							Interface:  "data-0",
							IPAddress:  tenantIP,
							MACAddress: "52:54:00:aa:bb:cc",
						},
					},
				}
				artifactsJSON, _ := json.Marshal(leaseResult)
				server, aapClient := newMockAAPServer(jobID, artifactsJSON)
				defer server.Close()

				// Wire the AAP client into the reconciler
				reconciler.AAPClient = aapClient

				// Mock IP discovery provider to trigger job and report success
				mockProvider.triggerProvisionFunc = func(_ context.Context, _ client.Object) (*provisioning.ProvisionResult, error) {
					return &provisioning.ProvisionResult{
						JobID:        jobID,
						InitialState: opv1alpha1.JobStatePending,
					}, nil
				}
				mockProvider.getProvisionStatusFunc = func(_ context.Context, _ client.Object, _ string) (provisioning.ProvisionStatus, error) {
					return provisioning.ProvisionStatus{
						JobID:   jobID,
						State:   opv1alpha1.JobStateSucceeded,
						Message: "IP discovery completed",
					}, nil
				}

				// Reconcile IP discovery until complete
				var ipDiscoveryCond *metav1.Condition
				for range 10 {
					_, _ = reconciler.reconcileIPDiscovery(ctx, bmi)
					ipDiscoveryCond = bmi.GetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete)
					if ipDiscoveryCond != nil && ipDiscoveryCond.Status == metav1.ConditionTrue {
						break
					}
					_ = k8sClient.Status().Update(ctx, bmi)
					Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), bmi)).To(Succeed())
				}

				// Assert IP discovery completed
				Expect(ipDiscoveryCond).NotTo(BeNil())
				Expect(ipDiscoveryCond.Status).To(Equal(metav1.ConditionTrue))
				Expect(ipDiscoveryCond.Reason).To(Equal("Succeeded"))

				// Assert the tenant IP is actually populated (non-vacuous check)
				Expect(bmi.Status.NetworkAttachmentStatuses).To(HaveLen(1))
				Expect(bmi.Status.NetworkAttachmentStatuses[0].SubnetRef).To(Equal("subnet-1"))
				Expect(bmi.Status.NetworkAttachmentStatuses[0].Interface).To(Equal("data-0"))
				Expect(bmi.Status.NetworkAttachmentStatuses[0].IPAddress).To(Equal(tenantIP),
					"tenant IP must be discovered and populated, not merely that discovery ran")
				Expect(bmi.Status.NetworkAttachmentStatuses[0].Primary).To(BeTrue())
			})
		})
	})

	Describe("applyIPDiscoveryResults", func() {
		It("should return error when job has no artifacts", func() {
			bmi := bmiForIPDiscovery([]v1alpha1.BareMetalNetworkAttachment{
				{SubnetRef: "subnet-1", Interface: "data-0", Primary: true},
			})
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
			defer func() { _ = k8sClient.Delete(ctx, bmi) }()

			server, aapClient := newMockAAPServer("empty-job", nil)
			defer server.Close()

			reconciler = &BareMetalInstanceReconciler{
				Client:    k8sClient,
				Scheme:    k8sClient.Scheme(),
				AAPClient: aapClient,
			}

			err := reconciler.applyIPDiscoveryResults(ctx, bmi, "empty-job")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no artifacts"))
		})

		It("should return error when lease is missing for an attachment", func() {
			// Use a single attachment so the CRD "exactly one primary" rule is satisfied,
			// then return a lease with a mismatched subnetRef so no lease matches.
			bmi := bmiForIPDiscovery([]v1alpha1.BareMetalNetworkAttachment{
				{SubnetRef: "subnet-2", Interface: "data-0", Primary: true},
			})
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
			defer func() { _ = k8sClient.Delete(ctx, bmi) }()

			// Return a lease only for subnet-1, not subnet-2
			leaseResult := DHCPLeaseResult{
				Leases: []DHCPLease{
					{SubnetRef: "subnet-1", Interface: "data-0", IPAddress: "10.0.1.5", MACAddress: "aa:bb:cc:dd:ee:ff"},
				},
			}
			artifactsJSON, _ := json.Marshal(leaseResult)
			server, aapClient := newMockAAPServer("partial-job", artifactsJSON)
			defer server.Close()

			reconciler = &BareMetalInstanceReconciler{
				Client:    k8sClient,
				Scheme:    k8sClient.Scheme(),
				AAPClient: aapClient,
			}

			err := reconciler.applyIPDiscoveryResults(ctx, bmi, "partial-job")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("subnet-2"))
			Expect(err.Error()).To(ContainSubstring("1/1"))

			// Verify attachment exists but has no IP (lease was for subnet-1, not subnet-2)
			Expect(bmi.Status.NetworkAttachmentStatuses).To(HaveLen(1))
			Expect(bmi.Status.NetworkAttachmentStatuses[0].IPAddress).To(BeEmpty())
		})

		It("should succeed when all leases are found", func() {
			bmi := bmiForIPDiscovery([]v1alpha1.BareMetalNetworkAttachment{
				{SubnetRef: "subnet-1", Interface: "data-0", Primary: true},
			})
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
			defer func() { _ = k8sClient.Delete(ctx, bmi) }()

			leaseResult := DHCPLeaseResult{
				Leases: []DHCPLease{
					{SubnetRef: "subnet-1", Interface: "data-0", IPAddress: "10.0.1.5", MACAddress: "aa:bb:cc:dd:ee:ff"},
				},
			}
			artifactsJSON, _ := json.Marshal(leaseResult)
			server, aapClient := newMockAAPServer("full-job", artifactsJSON)
			defer server.Close()

			reconciler = &BareMetalInstanceReconciler{
				Client:    k8sClient,
				Scheme:    k8sClient.Scheme(),
				AAPClient: aapClient,
			}

			err := reconciler.applyIPDiscoveryResults(ctx, bmi, "full-job")
			Expect(err).NotTo(HaveOccurred())
			Expect(bmi.Status.NetworkAttachmentStatuses).To(HaveLen(1))
			Expect(bmi.Status.NetworkAttachmentStatuses[0].IPAddress).To(Equal("10.0.1.5"))
		})
	})

	Describe("buildSubnetMACMap", func() {
		It("maps each attachment's subnet to the MAC of its interface", func() {
			attachments := []v1alpha1.BareMetalNetworkAttachment{
				{SubnetRef: "subnet-a", Interface: "eth9"},
				{SubnetRef: "subnet-b", Interface: "eth0"},
			}
			ifaceMACs := map[string]string{
				"eth9": "52:54:00:16:04:83",
				"eth0": "52:54:00:AA:BB:CC",
			}

			Expect(buildSubnetMACMap(attachments, ifaceMACs)).To(Equal(map[string]string{
				"subnet-a": "52:54:00:16:04:83",
				"subnet-b": "52:54:00:AA:BB:CC",
			}))
		})

		It("uses the single host interface when the attachment has no interface set", func() {
			attachments := []v1alpha1.BareMetalNetworkAttachment{
				{SubnetRef: "subnet-a"},
			}
			ifaceMACs := map[string]string{"eth9": "52:54:00:16:04:83"}

			Expect(buildSubnetMACMap(attachments, ifaceMACs)).To(Equal(map[string]string{
				"subnet-a": "52:54:00:16:04:83",
			}))
		})

		It("omits attachments whose interface has no known MAC", func() {
			attachments := []v1alpha1.BareMetalNetworkAttachment{
				{SubnetRef: "subnet-a", Interface: "eth9"},
				{SubnetRef: "subnet-b", Interface: "eth1"},
			}
			ifaceMACs := map[string]string{"eth9": "52:54:00:16:04:83"}

			Expect(buildSubnetMACMap(attachments, ifaceMACs)).To(Equal(map[string]string{
				"subnet-a": "52:54:00:16:04:83",
			}))
		})

		It("does not guess when the attachment has no interface and the host has multiple", func() {
			attachments := []v1alpha1.BareMetalNetworkAttachment{
				{SubnetRef: "subnet-a"},
			}
			ifaceMACs := map[string]string{
				"eth9": "52:54:00:16:04:83",
				"eth0": "52:54:00:AA:BB:CC",
			}

			Expect(buildSubnetMACMap(attachments, ifaceMACs)).To(BeEmpty())
		})
	})
})
