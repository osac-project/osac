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
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os/exec"

	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/osac-project/osac/osac-operator/test/testhelpers"
)

// Helpers shared by the networking specs of the deployed-operator suite. Every
// call goes through kubectl against the Kind cluster the installer deployed
// the operator into, and logs the command and its output to GinkgoWriter.

const (
	osacAPIVersion        = "osac.openshift.io/v1alpha1"
	managementStateKey    = "osac.openshift.io/management-state"
	tenantAnnotation      = "osac.openshift.io/tenant"
	ownerAnnotation       = "osac.openshift.io/owner-reference"
	fabricManagerLabel    = "osac.openshift.io/network-fabric-manager"
	k8sManagerLabel       = "osac.openshift.io/network-k8s-manager"
	networkingTestTenant  = "networking-it"
	networkingCommandWait = 30 * time.Second
	// readyTimeout bounds how long a deployed controller may take to reconcile
	// a networking CR (watch delivery plus the 10 s wait requeue).
	readyTimeout = 2 * time.Minute
	// disabledMessageFormat is the Ready message of a CR whose create task was
	// skipped because provider networking is disabled.
	disabledMessageFormat = "provider networking is disabled: %s not run"
	// networkingDisabledMessage is the Failed message of a CR that never
	// exists with provider networking disabled.
	networkingDisabledMessage = "provider networking is disabled"
	// admissionDeniedMessage is the message of the installer's
	// ValidatingAdmissionPolicy.
	admissionDeniedMessage = "OSAC manages this resource; change it through the fulfillment API"
)

// networkingResources maps a Kind to its fully qualified resource name, so
// kubectl never resolves a short Kind to another API group.
var networkingResources = map[string]string{
	"NetworkClass":         "networkclasses.osac.openshift.io",
	"NetworkAttachment":    "networkattachments.osac.openshift.io",
	"VirtualNetwork":       "virtualnetworks.osac.openshift.io",
	"Subnet":               "subnets.osac.openshift.io",
	"SecurityGroup":        "securitygroups.osac.openshift.io",
	"ExternalIPPool":       "externalippools.osac.openshift.io",
	"ExternalIP":           "externalips.osac.openshift.io",
	"ExternalIPAttachment": "externalipattachments.osac.openshift.io",
	"NATGateway":           "natgateways.osac.openshift.io",
	"FabricDomain":         "fabricdomains.osac.openshift.io",
	"ComputeInstance":      "computeinstances.osac.openshift.io",
	"ClusterOrder":         "clusterorders.osac.openshift.io",
	"BareMetalInstance":    "baremetalinstances.osac.openshift.io",
	"ConfigMap":            "configmaps",
}

func resourceOf(kind string) string {
	resource, ok := networkingResources[kind]
	Expect(ok).To(BeTrue(), "no resource mapping for kind %s", kind)
	return resource
}

// kubectl runs kubectl with a timeout and returns its combined output.
func kubectl(stdin string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), networkingCommandWait)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	output, err := cmd.CombinedOutput()
	_, _ = fmt.Fprintf(GinkgoWriter, "$ kubectl %s\n%s\n", strings.Join(args, " "), strings.TrimSpace(string(output)))
	if err != nil {
		return string(output), fmt.Errorf("kubectl %s: %w: %s", strings.Join(args, " "), err, output)
	}
	return string(output), nil
}

// uniqueSuffix returns 8 lowercase hex characters, usable in Kubernetes names
// and in manager registration names.
func uniqueSuffix() string {
	b := make([]byte, 4)
	_, err := rand.Read(b)
	Expect(err).NotTo(HaveOccurred())
	return fmt.Sprintf("%x", b)
}

// newObject builds an OSAC CR in the operator namespace with the tenant
// annotations every tenant-scoped networking CR carries.
func newObject(kind, name string, spec map[string]any) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": osacAPIVersion,
		"kind":       kind,
		"metadata": map[string]any{
			"name":      name,
			"namespace": operatorNamespace,
			"annotations": map[string]any{
				tenantAnnotation: networkingTestTenant,
				ownerAnnotation:  name,
			},
		},
	}}
	if spec != nil {
		obj.Object["spec"] = spec
	}
	return obj
}

// withAPIID sets the osac.openshift.io/<kind>-uuid label the fulfillment
// reconciler puts on every networking CR; the controllers find parents by it.
func withAPIID(obj *unstructured.Unstructured, id string) *unstructured.Unstructured {
	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels["osac.openshift.io/"+strings.ToLower(obj.GetKind())+"-uuid"] = id
	obj.SetLabels(labels)
	return obj
}

// unmanaged marks a CR so the deployed controllers ignore it; used where a
// spec checks only the API server (CRD validation).
func unmanaged(obj *unstructured.Unstructured) *unstructured.Unstructured {
	annotations := obj.GetAnnotations()
	annotations[managementStateKey] = "unmanaged"
	obj.SetAnnotations(annotations)
	return obj
}

func toJSON(obj *unstructured.Unstructured) string {
	data, err := obj.MarshalJSON()
	Expect(err).NotTo(HaveOccurred())
	return string(data)
}

// createAs creates obj through the API server as identity (empty for the
// suite's own cluster-admin credentials).
func createAs(identity string, obj *unstructured.Unstructured, extra ...string) (string, error) {
	args := append([]string{"create", "-f", "-", "-o", "json"}, extra...)
	if identity != "" {
		args = append(args, "--as="+identity)
	}
	return kubectl(toJSON(obj), args...)
}

// mustCreate creates obj and registers a cleanup that deletes it (clearing
// finalizers if the deletion would otherwise wait for a provider).
func mustCreate(obj *unstructured.Unstructured) *unstructured.Unstructured {
	By(fmt.Sprintf("creating %s %s", obj.GetKind(), obj.GetName()))
	output, err := createAs("", obj)
	Expect(
		err,
	).NotTo(HaveOccurred(), "creating %s %s must be accepted:\n%s\n%s", obj.GetKind(), obj.GetName(), toJSON(obj), output)
	created := &unstructured.Unstructured{}
	Expect(created.UnmarshalJSON([]byte(output))).To(Succeed())
	kind, name := obj.GetKind(), obj.GetName()
	DeferCleanup(func() { forceDelete(kind, name) })
	return created
}

// expectCreateRejected creates obj and expects the API server to reject it
// with a message containing every one of messages.
func expectCreateRejected(obj *unstructured.Unstructured, messages ...string) {
	By(fmt.Sprintf("creating %s %s, expecting rejection %q", obj.GetKind(), obj.GetName(), messages))
	output, err := createAs("", obj)
	if err == nil {
		// Never leave an object behind when the API server wrongly accepted it.
		forceDelete(obj.GetKind(), obj.GetName())
	}
	Expect(err).To(HaveOccurred(), "the API server accepted an invalid %s:\n%s", obj.GetKind(), toJSON(obj))
	for _, message := range messages {
		Expect(output).To(ContainSubstring(message), "rejection of %s must name %q", toJSON(obj), message)
	}
}

// patchAs applies a merge (or json) patch, optionally to a subresource and as
// another identity.
func patchAs(identity, kind, name, patchType, patch, subresource string) (string, error) {
	args := []string{"patch", resourceOf(kind), name, "-n", operatorNamespace, "--type=" + patchType, "-p", patch}
	if subresource != "" {
		args = append(args, "--subresource="+subresource)
	}
	if identity != "" {
		args = append(args, "--as="+identity)
	}
	return kubectl("", args...)
}

// expectPatchRejected expects a merge patch to be rejected with message and the
// stored object to be unchanged.
func expectPatchRejected(kind, name, patch, message string) {
	By(fmt.Sprintf("patching %s %s with %s, expecting rejection %q", kind, name, patch, message))
	before, err := getObject(kind, name)
	Expect(err).NotTo(HaveOccurred())
	output, err := patchAs("", kind, name, "merge", patch, "")
	Expect(err).To(HaveOccurred(), "the API server accepted the patch %s on %s %s", patch, kind, name)
	Expect(output).To(ContainSubstring(message))
	after, err := getObject(kind, name)
	Expect(err).NotTo(HaveOccurred())
	Expect(after.Object["spec"]).To(Equal(before.Object["spec"]), "a rejected patch must not change the stored spec")
}

// expectPatchAccepted expects a merge patch to be accepted.
func expectPatchAccepted(kind, name, patch string) {
	By(fmt.Sprintf("patching %s %s with %s, expecting it to be accepted", kind, name, patch))
	output, err := patchAs("", kind, name, "merge", patch, "")
	Expect(err).NotTo(HaveOccurred(), output)
}

func getObject(kind, name string) (*unstructured.Unstructured, error) {
	return testhelpers.Get(operatorNamespace, resourceOf(kind), name)
}

func objectAbsent(kind, name string) (bool, error) {
	return testhelpers.Absent(operatorNamespace, resourceOf(kind), name)
}

// logObject writes the current YAML of an object to GinkgoWriter.
func logObject(kind, name string) {
	_, _ = kubectl("", "get", resourceOf(kind), name, "-n", operatorNamespace, "-o", "yaml", "--ignore-not-found")
}

// forceDelete deletes an object and clears any finalizer still holding it.
// It is the cleanup of last resort; specs that check the operator's own
// delete use deleteAndWaitGone first.
func forceDelete(kind, name string) {
	testhelpers.DeleteAndWaitForCleanup(operatorNamespace, resourceOf(kind), name)
}

// deleteAndWaitGone deletes an object and waits until the deployed operator
// has removed its finalizer and the API server has removed the object. The
// test never touches the finalizer.
func deleteAndWaitGone(kind, name string) {
	By(fmt.Sprintf("deleting %s %s and waiting for the operator to finish the delete", kind, name))
	_, err := kubectl("", "delete", resourceOf(kind), name, "-n", operatorNamespace, "--wait=false", "--ignore-not-found")
	Expect(err).NotTo(HaveOccurred())
	Eventually(func() (bool, error) { return objectAbsent(kind, name) }, readyTimeout, time.Second).Should(BeTrue(),
		func() string {
			logObject(kind, name)
			return fmt.Sprintf("%s %s must be deleted by the operator without test help", kind, name)
		})
}

// readyCondition returns the Ready condition of a networking CR, or nil.
func readyCondition(obj *unstructured.Unstructured) map[string]any {
	return findNetworkingCondition(obj, "Ready")
}

func findNetworkingCondition(obj *unstructured.Unstructured, conditionType string) map[string]any {
	conditions, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	for _, c := range conditions {
		condition, ok := c.(map[string]any)
		if ok && condition["type"] == conditionType {
			return condition
		}
	}
	return nil
}

// eventuallyReady waits until the Ready condition of kind/name has status,
// reason and message, and returns the object.
func eventuallyReady(kind, name, status, reason, message string) *unstructured.Unstructured {
	By(fmt.Sprintf("waiting for %s %s to report Ready=%s reason %s message %q", kind, name, status, reason, message))
	var current *unstructured.Unstructured
	Eventually(func(g Gomega) {
		obj, err := getObject(kind, name)
		g.Expect(err).NotTo(HaveOccurred())
		current = obj
		ready := readyCondition(obj)
		g.Expect(ready).NotTo(BeNil(), "%s %s has no Ready condition yet: %v", kind, name, obj.Object["status"])
		g.Expect(ready["status"]).To(Equal(status), "Ready condition: %v", ready)
		g.Expect(ready["reason"]).To(Equal(reason), "Ready condition: %v", ready)
		got, _ := ready["message"].(string)
		g.Expect(got).To(Equal(message), "Ready condition: %v", ready)
	}, readyTimeout, time.Second).Should(Succeed(), func() string {
		logObject(kind, name)
		return fmt.Sprintf("%s %s did not reach the expected Ready condition", kind, name)
	})
	_, _ = fmt.Fprintf(GinkgoWriter, "%s %s status: %s\n", kind, name, mustJSON(current.Object["status"]))
	return current
}

func mustJSON(value any) string {
	data, err := json.Marshal(value)
	Expect(err).NotTo(HaveOccurred())
	return string(data)
}

// expectNoJobs asserts that no networking job was recorded and that the
// removed status fields of the old API (phase, networkingJobs) are absent.
func expectNoJobs(obj *unstructured.Unstructured) {
	status, _, _ := unstructured.NestedMap(obj.Object, "status")
	Expect(status).NotTo(HaveKey("jobs"), "%s %s must run no job: %v", obj.GetKind(), obj.GetName(), status)
	Expect(status).NotTo(HaveKey("phase"), "networking CRs no longer have status.phase: %v", status)
	Expect(status).NotTo(HaveKey("networkingJobs"), "networking CRs no longer have status.networkingJobs: %v", status)
}

// taskInput returns the stored status.taskInput of a networking CR.
func taskInput(obj *unstructured.Unstructured) map[string]any {
	input, found, err := unstructured.NestedMap(obj.Object, "status", "taskInput")
	Expect(err).NotTo(HaveOccurred())
	Expect(found).To(BeTrue(), "%s %s must store its task input: %v", obj.GetKind(), obj.GetName(), obj.Object["status"])
	return input
}

// operatorIdentity returns the Kubernetes user name of the deployed operator.
func operatorIdentity() string {
	output, err := kubectl("", "get", "deployments", "-n", operatorNamespace,
		"-l", "app.kubernetes.io/name=operator",
		"-o", "jsonpath={.items[0].spec.template.spec.serviceAccountName}")
	Expect(err).NotTo(HaveOccurred())
	serviceAccount := strings.TrimSpace(output)
	Expect(serviceAccount).NotTo(BeEmpty(), "the operator deployment must name its service account")
	return fmt.Sprintf("system:serviceaccount:%s:%s", operatorNamespace, serviceAccount)
}

// listNames lists the names of kind in the operator namespace.
func listNames(kind string) []string {
	output, err := kubectl("", "get", resourceOf(kind), "-n", operatorNamespace,
		"-o", "jsonpath={.items[*].metadata.name}")
	Expect(err).NotTo(HaveOccurred())
	return strings.Fields(output)
}

// removeFinalizers clears every finalizer of a namespaced object (used by the
// older ComputeInstance specs, which bypass the AAP deprovision boundary).
func removeFinalizers(kind, name, namespace string) {
	_, _ = kubectl("", "patch", kind, name, "-n", namespace, "--type=merge",
		"-p", `{"metadata":{"finalizers":[]}}`)
}

// expectDeleteHeld asserts that kind/name stays present, marked for deletion
// and holding finalizer for the whole duration.
func expectDeleteHeld(kind, name, finalizer string, duration time.Duration, why string) {
	By(fmt.Sprintf("checking that %s %s stays held by %s: %s", kind, name, finalizer, why))
	Eventually(func(g Gomega) {
		obj, err := getObject(kind, name)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(obj.GetDeletionTimestamp()).NotTo(BeNil())
	}, networkingCommandWait, time.Second).Should(Succeed())
	Consistently(func(g Gomega) {
		obj, err := getObject(kind, name)
		g.Expect(err).NotTo(HaveOccurred(), "%s %s was deleted although %s", kind, name, why)
		g.Expect(obj.GetDeletionTimestamp()).NotTo(BeNil())
		g.Expect(obj.GetFinalizers()).
			To(ContainElement(finalizer), "%s %s released %s although %s", kind, name, finalizer, why)
		expectNoJobsG(g, obj)
	}, duration, 2*time.Second).Should(Succeed())
	logObject(kind, name)
}

// expectNoJobsG is expectNoJobs inside an Eventually/Consistently.
func expectNoJobsG(g Gomega, obj *unstructured.Unstructured) {
	status, _, _ := unstructured.NestedMap(obj.Object, "status")
	g.Expect(status).NotTo(HaveKey("jobs"), "%s %s must run no job: %v", obj.GetKind(), obj.GetName(), status)
}
