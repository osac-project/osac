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

package multicluster

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	"github.com/osac-project/osac/osac-operator/test/utils"
)

const (
	operatorNamespace    = "osac"
	workloadClusterName  = "osac-workload"
	kubeconfigSecretName = "remote-cluster-kubeconfig"
	kubeconfigMountPath  = "/etc/osac/remote-kubeconfig"
	kubectlTimeout       = 30 * time.Second
)

// workloadHostKubeconfig is the path to a kubeconfig file that can be used from
// the test host (127.0.0.1:<nodePort>) to reach the workload cluster.
var workloadHostKubeconfig string

// originalDeploymentJSON holds the operator Deployment JSON captured before
// the multicluster patch is applied, so it can be fully restored in AfterSuite.
var originalDeploymentJSON []byte

var _ = BeforeSuite(func() {
	By("deleting any stale workload cluster from a previous run")
	_, _ = runShell("kind", "delete", "cluster", "--name", workloadClusterName)

	By("deleting any stale KubeVirt CRDs from the hub cluster")
	// A previous run (or another suite) may have left these behind. Remove
	// them so the "should NOT have KubeVirt CRDs on the hub cluster"
	// assertion passes reliably.
	_, _ = runKubectlHub("delete", "crd", "virtualmachines.kubevirt.io", "--ignore-not-found")
	_, _ = runKubectlHub("delete", "crd", "virtualmachineinstances.kubevirt.io", "--ignore-not-found")

	By("waiting for KubeVirt CRDs to be fully removed from the hub cluster")
	Eventually(func() string {
		out, _ := runKubectlHub("get", "crd", "virtualmachines.kubevirt.io",
			"--ignore-not-found", "-o", "name")
		return strings.TrimSpace(string(out))
	}, 60*time.Second, time.Second).Should(BeEmpty(),
		"virtualmachines.kubevirt.io CRD should be fully removed from the hub")
	Eventually(func() string {
		out, _ := runKubectlHub("get", "crd", "virtualmachineinstances.kubevirt.io",
			"--ignore-not-found", "-o", "name")
		return strings.TrimSpace(string(out))
	}, 60*time.Second, time.Second).Should(BeEmpty(),
		"virtualmachineinstances.kubevirt.io CRD should be fully removed from the hub")

	// kind create cluster changes the current kubectl context to the new
	// cluster. Capture the hub context now so we can restore it after.
	By("capturing current kubectl context (hub cluster)")
	hubContextOut, err := runShell("kubectl", "config", "current-context")
	Expect(err).NotTo(HaveOccurred(), "capture hub context")
	hubContext := strings.TrimSpace(string(hubContextOut))

	By("creating workload Kind cluster")
	output, err := runShell("kind", "create", "cluster",
		"--name", workloadClusterName,
		"--wait", "5m")
	Expect(err).NotTo(HaveOccurred(), "create workload cluster: %s", output)

	// Restore the hub context so runKubectlHub (which uses the default
	// context) continues to target the hub, not the workload cluster.
	By("switching kubectl context back to hub cluster")
	_, err = runShell("kubectl", "config", "use-context", hubContext)
	Expect(err).NotTo(HaveOccurred(), "restore hub context")

	By("exporting host-reachable kubeconfig for workload cluster")
	tmp, err := os.CreateTemp("", "osac-workload-kubeconfig-*")
	Expect(err).NotTo(HaveOccurred())
	workloadHostKubeconfig = tmp.Name()
	_ = tmp.Close()
	output, err = runShell("kind", "get", "kubeconfig", "--name", workloadClusterName)
	Expect(err).NotTo(HaveOccurred(), "get workload kubeconfig: %s", output)
	Expect(os.WriteFile(workloadHostKubeconfig, output, 0600)).To(Succeed())

	By("installing fake KubeVirt CRDs on the workload cluster")
	testdataDir := findTestdataDir()
	_, err = runKubectlWorkload("apply", "-f", filepath.Join(testdataDir, "kubevirt-crds.yaml"))
	Expect(err).NotTo(HaveOccurred())

	By("waiting for KubeVirt CRDs to be established on workload cluster")
	Eventually(func() error {
		_, err := runKubectlWorkload("get", "crd", "virtualmachines.kubevirt.io")
		return err
	}, 30*time.Second, time.Second).Should(Succeed())

	By("generating internal kubeconfig for the workload cluster (reachable from hub pods)")
	internalKubeconfig, err := buildInternalKubeconfig()
	Expect(err).NotTo(HaveOccurred())

	By("creating Secret with internal kubeconfig on hub cluster")
	// Delete if it already exists from a previous run.
	_, _ = runKubectlHub("delete", "secret", kubeconfigSecretName,
		"-n", operatorNamespace, "--ignore-not-found")
	// Write kubeconfig to a temp file and use --from-file to avoid logging
	// the kubeconfig contents (utils.Run logs the full command line).
	kubeconfigTmpFile, err := os.CreateTemp("", "osac-internal-kubeconfig-*")
	Expect(err).NotTo(HaveOccurred())
	defer os.Remove(kubeconfigTmpFile.Name())
	Expect(os.WriteFile(kubeconfigTmpFile.Name(), internalKubeconfig, 0600)).To(Succeed())
	_ = kubeconfigTmpFile.Close()
	createCmd := exec.Command("kubectl", "create", "secret", "generic",
		kubeconfigSecretName,
		"--from-file=kubeconfig="+kubeconfigTmpFile.Name(),
		"-n", operatorNamespace)
	_, err = utils.Run(createCmd)
	Expect(err).NotTo(HaveOccurred())

	By("patching operator deployment with remote cluster configuration")
	patchOperatorForRemoteCluster()

	By("waiting for operator to become healthy with remote cluster config")
	waitForOperatorHealthy()
})

var _ = AfterSuite(func() {
	By("cleaning up ComputeInstance test resources on hub")
	_, _ = runKubectlHub("delete", "computeinstance", "--all",
		"-n", operatorNamespace, "--wait=false", "--ignore-not-found")
	// Remove finalizers so deletion completes without AAP.
	removeAllFinalizers("computeinstance", operatorNamespace)

	By("restoring operator deployment to single-cluster mode")
	restoreOperatorDeployment()

	By("cleaning up kubeconfig Secret")
	_, _ = runKubectlHub("delete", "secret", kubeconfigSecretName,
		"-n", operatorNamespace, "--ignore-not-found")

	By("deleting workload Kind cluster")
	_, _ = runShell("kind", "delete", "cluster", "--name", workloadClusterName)

	if workloadHostKubeconfig != "" {
		_ = os.Remove(workloadHostKubeconfig)
	}
})

func TestMulticluster(t *testing.T) {
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting osac-operator multicluster integration suite\n")
	RunSpecs(t, "multicluster integration suite")
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// runShell executes a command and returns its combined output.
func runShell(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir, _ = utils.GetProjectDir()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, out)
	}
	return out, nil
}

// runKubectlHub runs kubectl against the hub cluster (default context).
func runKubectlHub(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), kubectlTimeout)
	defer cancel()
	return utils.Run(exec.CommandContext(ctx, "kubectl", args...))
}

// runKubectlWorkload runs kubectl against the workload cluster.
func runKubectlWorkload(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), kubectlTimeout)
	defer cancel()
	fullArgs := append([]string{"--kubeconfig", workloadHostKubeconfig}, args...)
	return utils.Run(exec.CommandContext(ctx, "kubectl", fullArgs...))
}

// findTestdataDir returns the path to testdata/ relative to the test binary.
func findTestdataDir() string {
	// The test binary runs from the project root (set by utils.Run).
	return "test/integration/multicluster/testdata"
}

// buildInternalKubeconfig generates a kubeconfig that uses the workload
// cluster's Docker-network IP (reachable from inside the hub Kind cluster)
// instead of localhost.
func buildInternalKubeconfig() ([]byte, error) {
	// Try kind get kubeconfig --internal first (available in Kind >= 0.11).
	out, err := runShell("kind", "get", "kubeconfig",
		"--name", workloadClusterName, "--internal")
	if err == nil {
		return out, nil
	}

	// Fallback: get the Docker container IP and rewrite the kubeconfig.
	containerName := workloadClusterName + "-control-plane"
	ipOut, err := runShell("docker", "inspect", "-f",
		"{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", containerName)
	if err != nil {
		return nil, fmt.Errorf("get workload container IP: %w", err)
	}
	containerIP := strings.TrimSpace(string(ipOut))
	if containerIP == "" {
		return nil, fmt.Errorf("empty container IP for %s", containerName)
	}

	kubeconfig, err := os.ReadFile(workloadHostKubeconfig)
	if err != nil {
		return nil, err
	}

	// Replace the host-facing 127.0.0.1:<randomPort> server URL with the
	// Docker-network IP on the standard Kind API server port (6443).
	re := regexp.MustCompile(`server: https://127\.0\.0\.1:\d+`)
	result := re.ReplaceAllString(
		string(kubeconfig),
		fmt.Sprintf("server: https://%s:6443", containerIP),
	)
	// Add insecure-skip-tls-verify since the cert may not include the Docker IP.
	result = strings.Replace(result,
		"certificate-authority-data:",
		"insecure-skip-tls-verify: true\n    # certificate-authority-data:",
		1,
	)
	return []byte(result), nil
}

// patchOperatorForRemoteCluster patches the operator Deployment to add the
// remote cluster kubeconfig volume, mount, and environment variables.
func patchOperatorForRemoteCluster() {
	deploymentName := getOperatorDeploymentName()

	// Save the original deployment so restoreOperatorDeployment can fully
	// restore it (env vars, volumes, volumeMounts, annotations).
	var err error
	originalDeploymentJSON, err = runKubectlHub("get", "deployment", deploymentName,
		"-n", operatorNamespace, "-o", "json")
	Expect(err).NotTo(HaveOccurred(), "save original deployment")

	patch := fmt.Sprintf(`{
  "spec": {
    "template": {
      "metadata": {
        "annotations": {
          "multicluster-test": "patched"
        }
      },
      "spec": {
        "volumes": [
          {
            "name": "fulfillment-ca-bundle",
            "configMap": {
              "name": "ca-bundle",
              "items": [{"key": "bundle.pem", "path": "bundle.pem"}]
            }
          },
          {
            "name": "remote-kubeconfig",
            "secret": {
              "secretName": "%s",
              "items": [{"key": "kubeconfig", "path": "kubeconfig"}]
            }
          }
        ],
        "containers": [{
          "name": "manager",
          "volumeMounts": [
            {"name": "fulfillment-ca-bundle", "mountPath": "/etc/ca-bundle", "readOnly": true},
            {"name": "remote-kubeconfig", "mountPath": "/etc/osac", "readOnly": true}
          ],
          "env": [
            {"name": "OSAC_REMOTE_CLUSTER_KUBECONFIG", "value": "/etc/osac/kubeconfig"},
            {"name": "OSAC_ENABLE_COMPUTE_INSTANCE_CONTROLLER", "value": "true"},
            {"name": "OSAC_ENABLE_TENANT_CONTROLLER", "value": "true"},
            {"name": "OSAC_ENABLE_CLUSTER_CONTROLLER", "value": "false"},
            {"name": "OSAC_ENABLE_BAREMETAL_INSTANCE_CONTROLLER", "value": "false"},
            {"name": "OSAC_ENABLE_NETWORKING_CONTROLLER", "value": "false"},
            {"name": "OSAC_ENABLE_STORAGE_CONTROLLER", "value": "false"},
            {"name": "OSAC_ENABLE_VOLUME_CONTROLLER", "value": "false"}
          ]
        }]
      }
    }
  }
}`, kubeconfigSecretName)

	_, err = runKubectlHub("patch", "deployment", deploymentName,
		"-n", operatorNamespace,
		"--type=strategic", "-p", patch)
	Expect(err).NotTo(HaveOccurred(), "patch operator deployment")
}

// restoreOperatorDeployment fully restores the operator Deployment to its
// pre-patch state by replacing it with the copy saved in
// patchOperatorForRemoteCluster. This removes all multicluster-test
// additions: annotation, env vars, volumes, and volumeMounts.
// If restore fails the suite still cleans up the cluster; the CI Makefile
// redeploys from scratch on every run.
func restoreOperatorDeployment() {
	if len(originalDeploymentJSON) == 0 {
		return
	}

	tmpFile, err := os.CreateTemp("", "operator-deploy-restore-*.json")
	if err != nil {
		return
	}
	defer os.Remove(tmpFile.Name())
	_ = tmpFile.Close()

	if err := os.WriteFile(tmpFile.Name(), originalDeploymentJSON, 0600); err != nil {
		return
	}
	_, _ = runKubectlHub("replace", "--force", "-f", tmpFile.Name())
}

// getOperatorDeploymentName returns the name of the operator Deployment.
func getOperatorDeploymentName() string {
	out, err := runKubectlHub("get", "deployment",
		"-l", "app.kubernetes.io/name=operator",
		"-n", operatorNamespace,
		"-o", "jsonpath={.items[0].metadata.name}")
	Expect(err).NotTo(HaveOccurred(), "get operator deployment name")
	name := strings.TrimSpace(string(out))
	Expect(name).NotTo(BeEmpty(), "operator deployment not found")
	return name
}

// waitForOperatorHealthy waits for the operator pod to be Running with all
// containers ready after the Deployment patch triggers a rollout.
func waitForOperatorHealthy() {
	Eventually(func() error {
		cmd := exec.Command("kubectl", "get", "pods",
			"-l", "control-plane=controller-manager,app.kubernetes.io/name=operator",
			"-n", operatorNamespace, "-o", "json")
		output, err := utils.Run(cmd)
		if err != nil {
			return err
		}

		var podList struct {
			Items []struct {
				Metadata struct {
					Name              string            `json:"name"`
					DeletionTimestamp *time.Time        `json:"deletionTimestamp"`
					Annotations       map[string]string `json:"annotations"`
				} `json:"metadata"`
				Status struct {
					Phase             string `json:"phase"`
					ContainerStatuses []struct {
						Ready bool `json:"ready"`
						State struct {
							Waiting *struct {
								Reason string `json:"reason"`
							} `json:"waiting"`
						} `json:"state"`
					} `json:"containerStatuses"`
				} `json:"status"`
			} `json:"items"`
		}
		if err := json.Unmarshal(output, &podList); err != nil {
			return fmt.Errorf("parse pod list: %w", err)
		}

		var running int
		for _, pod := range podList.Items {
			if pod.Metadata.DeletionTimestamp != nil {
				continue
			}
			// Only count pods from the patched deployment.
			if pod.Metadata.Annotations["multicluster-test"] != "patched" {
				continue
			}
			if pod.Status.Phase != "Running" {
				return fmt.Errorf("pod %s in %s phase", pod.Metadata.Name, pod.Status.Phase)
			}
			for _, cs := range pod.Status.ContainerStatuses {
				if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff" {
					return fmt.Errorf("pod %s is crash-looping", pod.Metadata.Name)
				}
				if !cs.Ready {
					return fmt.Errorf("pod %s has unready container", pod.Metadata.Name)
				}
			}
			running++
		}
		if running != 1 {
			return fmt.Errorf("expected 1 running patched operator pod, found %d", running)
		}
		return nil
	}, 5*time.Minute, 5*time.Second).Should(Succeed())
}

func removeFinalizers(kind, name, namespace string) {
	cmd := exec.Command("kubectl", "patch", kind, name,
		"-n", namespace, "--type=merge",
		"-p", `{"metadata":{"finalizers":[]}}`)
	_, _ = utils.Run(cmd)
}

func removeAllFinalizers(kind, namespace string) {
	cmd := exec.Command("kubectl", "get", kind, "-n", namespace,
		"-o", "jsonpath={.items[*].metadata.name}")
	output, err := utils.Run(cmd)
	if err != nil {
		return
	}
	for name := range strings.SplitSeq(string(output), " ") {
		if name != "" {
			removeFinalizers(kind, name, namespace)
		}
	}
}
