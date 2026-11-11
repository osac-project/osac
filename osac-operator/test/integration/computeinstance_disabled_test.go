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
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// The deployed operator with provider networking disabled (what CI installs)
// must keep ComputeInstance provisioning and deletion available on the
// cluster default pod network, without a tenant Subnet namespace or a
// NetworkAttachment (VMaaS design, "Provider networking disabled"). The
// operator's AAP launch is captured by a recording double, like the
// SUITE=compute-storage fixture; no real AAP or KubeVirt VM runs here.

const (
	aapRecorderName       = "osac-aap-recorder"
	aapRecorderPort int32 = 8080
	// aapRecorderImage is pinned by digest so the Kind suite does not depend
	// on a mutable external tag.
	aapRecorderImage = "registry.access.redhat.com/ubi9/python-312@" +
		"sha256:19e1e927138b40454858f297884c2c4e2f13f5ba95b3f55faea174d3cbb57067"
	// computeInstanceIDLabel is the resource label the operator finds the job
	// by; vmComputeInstanceIDLabel is the label inside vm_network.
	computeInstanceIDLabel   = "osac.openshift.io/computeinstance-uuid"
	vmComputeInstanceIDLabel = "osac.openshift.io/compute-instance-id"
	provisionTemplate        = "osac-create-compute-instance"
	deprovisionTemplate      = "osac-delete-compute-instance"
	// aapLaunchTimeout exceeds networktask.LostLaunchTimeout (2m) so a launch
	// the engine treats as lost can be retried before the test gives up.
	aapLaunchTimeout = 3 * time.Minute
)

//go:embed testdata/aap_recorder.py
var aapRecorderScript string

type aapCapture struct {
	ID       int    `json:"id"`
	Template string `json:"template"`
	Status   string `json:"status"`
	Payload  struct {
		ExtraVars struct {
			JobVars struct {
				VMNetwork struct {
					Namespace  string            `json:"namespace"`
					Networks   []map[string]any  `json:"networks"`
					Interfaces []map[string]any  `json:"interfaces"`
					Labels     map[string]string `json:"labels"`
				} `json:"vm_network"`
				Resource struct {
					Metadata struct {
						Labels map[string]string `json:"labels"`
					} `json:"metadata"`
				} `json:"resource"`
			} `json:"osac_job_vars"`
		} `json:"extra_vars"`
	} `json:"payload"`
}

type aapCaptures struct {
	Jobs       []aapCapture `json:"jobs"`
	Unexpected []string     `json:"unexpected"`
}

// applyObjects serializes typed Kubernetes objects into one v1.List for
// kubectl apply, so the fixture schema stays type-checked.
func applyObjects(objects ...any) string {
	list := &corev1.List{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "List"}}
	for _, object := range objects {
		raw, err := json.Marshal(object)
		Expect(err).NotTo(HaveOccurred(), "serialize the AAP recorder object")
		list.Items = append(list.Items, runtime.RawExtension{Raw: raw})
	}
	manifest, err := json.Marshal(list)
	Expect(err).NotTo(HaveOccurred(), "serialize the AAP recorder list")
	return string(manifest)
}

// installAAPRecorder deploys the capture-only AAP server the operator points
// at through operator.aap.url. It waits until the service proxy answers
// /healthz before a ComputeInstance can launch a job.
func installAAPRecorder() {
	// A previous interrupted run may have left the fixed-name objects behind;
	// recreate the recorder so it always runs the embedded script.
	for _, resource := range []string{"pod", "service", "configmap"} {
		_, _ = runRecorderKubectl("", "delete", resource, aapRecorderName,
			"-n", operatorNamespace, "--ignore-not-found", "--wait=true")
	}

	objects := []any{
		&corev1.ConfigMap{
			TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
			ObjectMeta: metav1.ObjectMeta{Name: aapRecorderName, Namespace: operatorNamespace},
			Data:       map[string]string{"recorder.py": aapRecorderScript},
		},
		&corev1.Service{
			TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
			ObjectMeta: metav1.ObjectMeta{Name: aapRecorderName, Namespace: operatorNamespace},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": aapRecorderName},
				Ports: []corev1.ServicePort{{
					Name:       "http",
					Port:       aapRecorderPort,
					TargetPort: intstr.FromInt32(aapRecorderPort),
				}},
			},
		},
		&corev1.Pod{
			TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
			ObjectMeta: metav1.ObjectMeta{
				Name:      aapRecorderName,
				Namespace: operatorNamespace,
				Labels:    map[string]string{"app": aapRecorderName},
			},
			Spec: corev1.PodSpec{
				AutomountServiceAccountToken: new(false),
				SecurityContext: &corev1.PodSecurityContext{
					RunAsNonRoot:   new(true),
					RunAsUser:      new(int64(1000)),
					SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
				},
				Containers: []corev1.Container{{
					Name:    "recorder",
					Image:   aapRecorderImage,
					Command: []string{"python3", "-B", "/fixture/recorder.py"},
					Ports:   []corev1.ContainerPort{{ContainerPort: aapRecorderPort}},
					VolumeMounts: []corev1.VolumeMount{{
						Name:      "fixture",
						MountPath: "/fixture",
						ReadOnly:  true,
					}},
					SecurityContext: &corev1.SecurityContext{
						AllowPrivilegeEscalation: new(false),
						ReadOnlyRootFilesystem:   new(true),
						Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
					},
					ReadinessProbe: &corev1.Probe{
						ProbeHandler: corev1.ProbeHandler{
							HTTPGet: &corev1.HTTPGetAction{
								Path: "/healthz",
								Port: intstr.FromInt32(aapRecorderPort),
							},
						},
					},
				}},
				Volumes: []corev1.Volume{{
					Name: "fixture",
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{Name: aapRecorderName},
						},
					},
				}},
			},
		},
	}

	_, err := runRecorderKubectl(applyObjects(objects...), "apply", "-f", "-")
	Expect(err).NotTo(HaveOccurred(), "apply the AAP recorder fixture")

	Eventually(func(g Gomega) {
		output, err := runRecorderKubectl("", "get", "pod", aapRecorderName, "-n", operatorNamespace,
			"-o", `jsonpath={.status.conditions[?(@.type=="Ready")].status}`)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(strings.TrimSpace(string(output))).To(Equal("True"))
	}, 5*time.Minute, 2*time.Second).Should(Succeed())

	Eventually(func(g Gomega) {
		output, err := runRecorderKubectl("", "get", "endpoints", aapRecorderName, "-n", operatorNamespace,
			"-o", "jsonpath={.subsets[*].addresses[*].ip}")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(strings.TrimSpace(string(output))).NotTo(BeEmpty())
	}, 5*time.Minute, 2*time.Second).Should(Succeed())

	Eventually(func(g Gomega) {
		script := `import urllib.request; print(urllib.request.urlopen(` +
			`"http://osac-aap-recorder:8080/healthz", timeout=5).read().decode())`
		output, err := runRecorderKubectl("", "exec", "-n", operatorNamespace, aapRecorderName,
			"--", "python3", "-c", script)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(string(output)).To(ContainSubstring("ready"))
	}, 5*time.Minute, 2*time.Second).Should(Succeed())

	DeferCleanup(func() {
		for _, resource := range []string{"pod", "service", "configmap"} {
			_, _ = runRecorderKubectl("", "delete", resource, aapRecorderName,
				"-n", operatorNamespace, "--ignore-not-found", "--wait=false")
		}
	})
}

// aapRecorderPath is the Kubernetes service-proxy path for one recorder endpoint.
func aapRecorderPath(suffix string) string {
	return fmt.Sprintf("/api/v1/namespaces/%s/services/http:%s:%d/proxy/%s",
		operatorNamespace, aapRecorderName, aapRecorderPort, suffix)
}

// runRecorderKubectl runs a recorder command without logging its arguments or
// output; both can carry captured launch payloads and their credentials.
func runRecorderKubectl(stdin string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), networkingCommandWait)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	return cmd.CombinedOutput()
}

// readAAPCaptures reads the recorder's captured jobs without logging the raw
// payloads, which can contain storage credentials.
func readAAPCaptures() (aapCaptures, error) {
	output, err := runRecorderKubectl("", "get", "--raw", aapRecorderPath("captures"))
	if err != nil {
		return aapCaptures{}, err
	}
	var captures aapCaptures
	if err := json.Unmarshal(output, &captures); err != nil {
		return aapCaptures{}, err
	}
	return captures, nil
}

// captureSummary returns only safe job metadata, never the recorded payloads.
func captureSummary(jobs []aapCapture) string {
	summaries := make([]string, 0, len(jobs))
	for _, job := range jobs {
		summaries = append(summaries, fmt.Sprintf("%d/%s/%s", job.ID, job.Template, job.Status))
	}
	return strings.Join(summaries, ", ")
}

// waitForAAPLaunch waits until exactly one launch of template carries the
// ComputeInstance API ID that the operator put in the job's resource labels.
func waitForAAPLaunch(template, id string) aapCapture {
	var found aapCapture
	Eventually(func(g Gomega) {
		captures, err := readAAPCaptures()
		g.Expect(err).NotTo(HaveOccurred(), "read the AAP recorder captures")
		g.Expect(captures.Unexpected).To(BeEmpty(), "the operator must launch no other AAP operation")
		var matches []aapCapture
		for _, job := range captures.Jobs {
			if job.Template == template && job.Payload.ExtraVars.JobVars.Resource.Metadata.Labels[computeInstanceIDLabel] == id {
				matches = append(matches, job)
			}
		}
		g.Expect(matches).To(HaveLen(1), "captured jobs: %s", captureSummary(captures.Jobs))
		found = matches[0]
	}, aapLaunchTimeout, time.Second).Should(Succeed())
	return found
}

// createTenantStorageClass creates a StorageClass labeled for the Tenant and
// tier so the ComputeInstance storage gate resolves without a catalog.
func createTenantStorageClass(tenantName string) {
	name := tenantName + "-standard"
	manifest := fmt.Sprintf(`apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: %s
  labels:
    osac.openshift.io/tenant: %s
    osac.openshift.io/storage-tier: standard
provisioner: kubernetes.io/no-provisioner
volumeBindingMode: WaitForFirstConsumer
`, name, tenantName)
	output, err := kubectl(manifest, "apply", "-f", "-")
	Expect(err).NotTo(HaveOccurred(), output)
	DeferCleanup(func() {
		_, _ = kubectl("", "delete", "storageclass", name, "--ignore-not-found")
	})
}

// waitForTenantStorageClass waits until the storage controller records the
// labeled StorageClass on the Tenant, so the ComputeInstance does not gate on
// storage.
func waitForTenantStorageClass(tenantName, tier string) {
	Eventually(func(g Gomega) {
		current, err := getObject("Tenant", tenantName)
		g.Expect(err).NotTo(HaveOccurred())
		classes, _, _ := unstructured.NestedSlice(current.Object, "status", "storageClasses")
		found := false
		for _, class := range classes {
			entry, ok := class.(map[string]any)
			if ok && entry["tier"] == tier && entry["name"] != "" {
				found = true
				break
			}
		}
		g.Expect(found).To(BeTrue(), "Tenant storageClasses: %v", classes)
	}, readyTimeout, time.Second).Should(Succeed())
}

// createReadyTenantForDisabledVM creates a Tenant and waits for the operator's
// tenant controller to mark it Ready.
func createReadyTenantForDisabledVM(name string) {
	tenant := newObject("Tenant", name, nil)
	mustCreate(tenant)
	Eventually(func(g Gomega) {
		current, err := getObject("Tenant", name)
		g.Expect(err).NotTo(HaveOccurred())
		phase, _, _ := unstructured.NestedString(current.Object, "status", "phase")
		g.Expect(phase).To(Equal("Ready"), "Tenant is not Ready")
	}, readyTimeout, time.Second).Should(Succeed())
}

var _ = Describe("ComputeInstance with provider networking disabled",
	Ordered, Label("computeinstance", "disabled"), func() {
		var tenantName, instanceName, instanceID string

		BeforeAll(func() {
			if os.Getenv("IT_TEST_SUITE") != "operator" {
				Skip("requires the SUITE=operator AAP recorder phase; standalone integration-tests deploy the AWX URL")
			}
			Expect(operatorDeploymentEnv("OSAC_AAP_URL")).To(ContainSubstring(":8080/api"),
				"the operator phase must point operator.aap.url at the AAP recorder")
			Expect(operatorDeploymentEnv("OSAC_PROVISIONING_ENABLED")).To(Equal("false"),
				"CI deploys global.networking.provisioningEnabled=false")

			installAAPRecorder()

			suffix := uniqueSuffix()
			tenantName = "vmaas-disabled-" + suffix
			instanceName = "vmaas-disabled-" + uniqueSuffix()
			instanceID = "ci-" + uniqueSuffix()
			createReadyTenantForDisabledVM(tenantName)
			createTenantStorageClass(tenantName)
			waitForTenantStorageClass(tenantName, "standard")

			instance := newObject("ComputeInstance", instanceName, map[string]any{
				"templateID": "test_template",
				"image": map[string]any{
					"sourceType": "registry",
					"sourceRef":  "quay.io/fedora/fedora-coreos:stable",
				},
				"vcpus":     int64(2),
				"memoryGiB": int64(4),
				"bootDisk": map[string]any{
					"sizeGiB":     int64(20),
					"storageTier": "standard",
				},
				"runStrategy": "Always",
			})
			instance.SetAnnotations(map[string]string{tenantAnnotation: tenantName})
			instance.SetLabels(map[string]string{computeInstanceIDLabel: instanceID})
			mustCreate(instance)
		})

		It("provisions and deprovisions on the default pod network in the Tenant namespace", func() {
			provision := waitForAAPLaunch(provisionTemplate, instanceID)
			Expect(provision.Payload.ExtraVars.JobVars.VMNetwork.Namespace).To(Equal(tenantName),
				"the disabled fallback uses the Tenant namespace, never a Subnet namespace")
			Expect(provision.Payload.ExtraVars.JobVars.VMNetwork.Networks).To(Equal(
				[]map[string]any{{"name": "default", "pod": map[string]any{}}}))
			Expect(provision.Payload.ExtraVars.JobVars.VMNetwork.Interfaces).To(Equal(
				[]map[string]any{{"name": "default", "masquerade": map[string]any{}}}))
			Expect(provision.Payload.ExtraVars.JobVars.VMNetwork.Labels).To(
				HaveKeyWithValue(vmComputeInstanceIDLabel, instanceID))

			By("checking that provider networking disabled creates no NetworkAttachment")
			absent, err := objectAbsent("NetworkAttachment", "computeinstance-"+instanceID)
			Expect(err).NotTo(HaveOccurred())
			Expect(absent).To(BeTrue())

			By("checking the operator reports NetworkReady=True with ProvisioningDisabled")
			Eventually(func(g Gomega) {
				current, err := getObject("ComputeInstance", instanceName)
				g.Expect(err).NotTo(HaveOccurred())
				ready := findNetworkingCondition(current, "NetworkReady")
				g.Expect(ready).NotTo(BeNil(), "ComputeInstance has no NetworkReady condition")
				g.Expect(ready["status"]).To(Equal("True"))
				g.Expect(ready["reason"]).To(Equal("ProvisioningDisabled"))
			}, readyTimeout, time.Second).Should(Succeed())

			By("deleting the ComputeInstance while the provision job runs")
			_, err = kubectl("", "delete", resourceOf("ComputeInstance"), instanceName,
				"-n", operatorNamespace, "--wait=false")
			Expect(err).NotTo(HaveOccurred())

			deprovision := waitForAAPLaunch(deprovisionTemplate, instanceID)
			Expect(deprovision.Payload.ExtraVars.JobVars.VMNetwork.Namespace).To(Equal(tenantName),
				"the disabled deprovision keeps the Tenant namespace placement")
			Expect(deprovision.Payload.ExtraVars.JobVars.VMNetwork.Interfaces).To(Equal(
				[]map[string]any{{"name": "default", "masquerade": map[string]any{}}}))
		})
	})
