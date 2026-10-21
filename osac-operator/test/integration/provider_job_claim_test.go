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

package integration

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	providerJobClaimAnnotation       = "osac.openshift.io/provider-job-launch-claim"
	externalIPFinalizer              = "osac.openshift.io/externalip-finalizer"
	externalIPAttachmentFinalizer    = "osac.openshift.io/externalipattachment-finalizer"
	externalIPImplementationStrategy = "osac.openshift.io/implementation-strategy"
)

// Provider launch claims model the persisted intent while the provider request
// is in flight and before its job ID is recorded in status. These tests use the
// real Kind API and deployed manager; controller tests separately count
// provider invocations because the component suite does not deploy AAP.
var _ = Describe("Provider job launch claim lifecycle", func() {
	It("holds ExternalIP deletion through a Create launch and manager restart", func() {
		name := fmt.Sprintf("ip-launch-race-%d", time.Now().UnixNano())
		claim := providerJobLaunchClaimJSON("create-"+name, "provision")
		ip := cleanupObject("ExternalIP", name, map[string]any{"pool": "test-pool"})
		ip.SetFinalizers([]string{externalIPFinalizer, networkCleanupTestFinalizer})
		annotations := ip.GetAnnotations()
		annotations[providerJobClaimAnnotation] = claim
		annotations[externalIPImplementationStrategy] = "metallb-l2"
		ip.SetAnnotations(annotations)
		cleanupCreate(ip)
		DeferCleanup(cleanupDelete, ip)

		By("deleting the IP while its Create job ID is not yet recorded")
		deleteCleanupObject(ip)
		Eventually(func(g Gomega) {
			assertExternalIPCreateClaimHeld(g, ip, claim)
		}, 45*time.Second, time.Second).Should(Succeed())

		By("restarting the deployed manager before the provider launch claim expires")
		restartCleanupManager()
		Consistently(func(g Gomega) {
			assertExternalIPCreateClaimHeld(g, ip, claim)
		}, 35*time.Second, 2*time.Second).Should(Succeed())
	})

	It("keeps an ExternalIPAttachment detach launch claimed across reconciles and restart", func() {
		name := fmt.Sprintf("attachment-detach-race-%d", time.Now().UnixNano())
		claim := providerJobLaunchClaimJSON("detach-"+name, "deprovision")
		attachment := cleanupObject("ExternalIPAttachment", name, map[string]any{
			"externalIP": "test-external-ip", "computeInstance": "test-compute-instance",
		})
		attachment.SetFinalizers([]string{externalIPAttachmentFinalizer, networkCleanupTestFinalizer})
		annotations := attachment.GetAnnotations()
		annotations[providerJobClaimAnnotation] = claim
		annotations[externalIPImplementationStrategy] = "metallb-l2"
		attachment.SetAnnotations(annotations)
		cleanupCreate(attachment)
		DeferCleanup(cleanupDelete, attachment)

		By("deleting the attachment before its detach job ID is persisted")
		deleteCleanupObject(attachment)
		Eventually(func(g Gomega) {
			assertExternalIPAttachmentDetachClaimHeld(g, attachment, claim)
		}, 45*time.Second, time.Second).Should(Succeed())

		By("restarting the deployed manager and observing the persisted claim")
		restartCleanupManager()
		Consistently(func(g Gomega) {
			assertExternalIPAttachmentDetachClaimHeld(g, attachment, claim)
		}, 35*time.Second, 2*time.Second).Should(Succeed())
	})
})

func providerJobLaunchClaimJSON(id, jobType string) string {
	claim := map[string]any{
		"id": id, "jobType": jobType, "claimedAt": time.Now().UTC().Format(time.RFC3339Nano),
	}
	encoded, err := json.Marshal(claim)
	Expect(err).NotTo(HaveOccurred())
	return string(encoded)
}

func deleteCleanupObject(object *unstructured.Unstructured) {
	output, err := exec.Command("kubectl", "delete", object.GetKind(), object.GetName(),
		"-n", operatorNamespace, "--wait=false").CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), string(output))
}

func assertExternalIPCreateClaimHeld(g Gomega, object *unstructured.Unstructured, claim string) {
	current, err := cleanupGet(object)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(current.GetDeletionTimestamp()).NotTo(BeNil())
	g.Expect(current.GetFinalizers()).To(ContainElement(externalIPFinalizer))
	g.Expect(current.GetFinalizers()).To(ContainElement(networkCleanupTestFinalizer))
	g.Expect(current.GetAnnotations()[providerJobClaimAnnotation]).To(Equal(claim))

	phase, found, err := unstructured.NestedString(current.Object, "status", "phase")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(found).To(BeTrue())
	g.Expect(phase).To(Equal("Deleting"), "a late create result must not restore Ready")
	state, _, err := unstructured.NestedString(current.Object, "status", "state")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(state).NotTo(Equal("Allocated"))
	address, _, err := unstructured.NestedString(current.Object, "status", "address")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(address).To(BeEmpty())
	jobs, _, err := unstructured.NestedSlice(current.Object, "status", "provisioningJobs")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(jobs).To(BeEmpty(), "the Create job ID has not yet been persisted")
}

func assertExternalIPAttachmentDetachClaimHeld(g Gomega, object *unstructured.Unstructured, claim string) {
	current, err := cleanupGet(object)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(current.GetDeletionTimestamp()).NotTo(BeNil())
	g.Expect(current.GetFinalizers()).To(ContainElement(externalIPAttachmentFinalizer))
	g.Expect(current.GetFinalizers()).To(ContainElement(networkCleanupTestFinalizer))
	g.Expect(current.GetAnnotations()[providerJobClaimAnnotation]).To(Equal(claim))

	phase, found, err := unstructured.NestedString(current.Object, "status", "phase")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(found).To(BeTrue())
	g.Expect(phase).To(Equal("Deleting"))
	jobs, _, err := unstructured.NestedSlice(current.Object, "status", "provisioningJobs")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(jobs).To(BeEmpty(), "repeated reconcile must not record a second detach job")
}
