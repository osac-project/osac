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
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/osac-project/osac/osac-operator/test/utils"
)

const (
	remoteKindClusterName      = "osac-remote"
	remoteKubeconfigSecretName = "osac-remote-kubeconfig"
	remoteKubeconfigMountDir   = "/etc/osac-remote"
	remoteKubeconfigMountPath  = "/etc/osac-remote/remote-cluster-kubeconfig"
	remoteServiceAccountName   = "osac-remote-access"
	remoteTestTenantName       = "tenant-two-kind"
	remoteTestSecretName       = "vast-tenant-config-tenant-two-kind"
	localDecoyStorageClass     = "local-mgmt-only-gold-sc"
	remoteWorkloadStorageClass = "remote-workload-gold-sc"
)

var _ = Describe("Remote cluster StorageClass reconciliation across two Kind clusters", Ordered, Serial, func() {
	var (
		remoteKubeconfigPath   string
		restorePatchFilePath   string
		remoteModeReconfigured bool
	)

	BeforeAll(func() {
		if _, err := exec.LookPath("kind"); err != nil {
			Skip("kind binary not found on PATH; skipping two-Kind-cluster integration test")
		}

		clustersOut, err := runKind("get", "clusters")
		Expect(err).NotTo(HaveOccurred())
		mgmtCluster := os.Getenv("KIND_CLUSTER_NAME")
		if mgmtCluster == "" {
			mgmtCluster = "osac-dev"
		}
		if !containsLine(string(clustersOut), mgmtCluster) {
			Skip(fmt.Sprintf(
				"management Kind cluster %q not found in `kind get clusters`; skipping two-Kind-cluster test",
				mgmtCluster,
			))
		}

		By("snapshotting the management cluster operator Deployment template to a restore patch file")
		restorePatchFilePath = filepath.Join(GinkgoT().TempDir(), "local-mode-restore-patch.json")
		writeLocalModeRestorePatchFile(restorePatchFilePath)

		remoteKubeconfigPath = filepath.Join(GinkgoT().TempDir(), "osac-remote.kubeconfig")

		DeferCleanup(func() {
			By("cleaning up test resources and restoring the operator Deployment to single-cluster mode")
			defer func() {
				_, secretErr := runKubectl("delete", "secret", remoteKubeconfigSecretName, "-n", operatorNamespace, "--ignore-not-found")
				_, kindErr := runKind("delete", "cluster", "--name", remoteKindClusterName, "--kubeconfig", remoteKubeconfigPath)
				Expect(secretErr).NotTo(HaveOccurred())
				Expect(kindErr).NotTo(HaveOccurred())
			}()

			removeFinalizers("tenant", remoteTestTenantName, operatorNamespace)
			_, tenantDelErr := runKubectl(
				"delete", "tenant", remoteTestTenantName,
				"-n", operatorNamespace, "--ignore-not-found", "--wait=false",
			)
			removeFinalizers("tenant", remoteTestTenantName, operatorNamespace)
			_, testSecretDelErr := runKubectl("delete", "secret", remoteTestSecretName, "-n", operatorNamespace, "--ignore-not-found")
			_, scDelErr := runKubectl("delete", "sc", localDecoyStorageClass, "--ignore-not-found")

			if remoteModeReconfigured {
				restoreOperatorDeploymentToLocalMode(restorePatchFilePath)
			}
			Expect(tenantDelErr).NotTo(HaveOccurred())
			Expect(testSecretDelErr).NotTo(HaveOccurred())
			Expect(scDelErr).NotTo(HaveOccurred())
		})

		By("creating the second Kind workload cluster (" + remoteKindClusterName + ")")
		_, err = runKind("delete", "cluster", "--name", remoteKindClusterName, "--kubeconfig", remoteKubeconfigPath)
		Expect(err).NotTo(HaveOccurred())
		_, err = runKindWithTimeout(
			3*time.Minute,
			"create", "cluster",
			"--name", remoteKindClusterName,
			"--kubeconfig", remoteKubeconfigPath,
			"--wait", "90s",
		)
		Expect(err).NotTo(HaveOccurred())

		By("installing fake workload CRDs on the remote Kind cluster")
		projectDir, err := utils.GetProjectDir()
		Expect(err).NotTo(HaveOccurred())
		fakeCRDs := []string{
			filepath.Join(projectDir, "config/crd/fakes/k8s.ovn.org_userdefinednetworks.yaml"),
			filepath.Join(projectDir, "config/crd/fakes/kubevirt.io_virtualmachines.yaml"),
			filepath.Join(projectDir, "config/crd/fakes/kubevirt.io_virtualmachineinstances.yaml"),
		}
		for _, crdPath := range fakeCRDs {
			_, err := runKubectlWithKubeconfig(
				remoteKubeconfigPath, "apply", "--server-side", "--force-conflicts", "-f", crdPath,
			)
			Expect(err).NotTo(HaveOccurred())
		}

		By("configuring a least-privilege ServiceAccount on the remote Kind cluster with read-only StorageClass RBAC")
		_, err = runKubectlWithStdin(remoteKubeconfigPath, remoteRBACManifest(), "apply", "-f", "-")
		Expect(err).NotTo(HaveOccurred())

		remoteSAIdentity := fmt.Sprintf("system:serviceaccount:%s:%s", operatorNamespace, remoteServiceAccountName)
		for _, verb := range []string{"get", "list", "watch"} {
			out, err := runKubectlWithKubeconfig(
				remoteKubeconfigPath,
				"auth", "can-i", "-A", verb, "storageclasses.storage.k8s.io", "--as="+remoteSAIdentity,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(lastNonEmptyLine(string(out))).To(
				Equal("yes"), "expected remote SA to allow %s on storageclasses", verb,
			)
		}
		for _, verb := range []string{"create", "update", "patch", "delete"} {
			out, err := runKubectlWithKubeconfig(
				remoteKubeconfigPath,
				"auth", "can-i", "-A", verb, "storageclasses.storage.k8s.io", "--as="+remoteSAIdentity,
			)
			Expect(err).To(HaveOccurred(), "expected kubectl auth can-i %s to exit non-zero", verb)
			Expect(lastNonEmptyLine(string(out))).To(
				Equal("no"), "expected remote SA to forbid %s on storageclasses", verb,
			)
		}

		By("staging the remote cluster kubeconfig Secret on the management Kind cluster")
		inClusterKubeconfigFile := filepath.Join(GinkgoT().TempDir(), "in-cluster-remote.kubeconfig")
		writeRemoteInClusterKubeconfig(remoteKubeconfigPath, inClusterKubeconfigFile)

		_, err = runKubectl("delete", "secret", remoteKubeconfigSecretName, "-n", operatorNamespace, "--ignore-not-found")
		Expect(err).NotTo(HaveOccurred())
		_, err = runKubectl(
			"create", "secret", "generic", remoteKubeconfigSecretName,
			"-n", operatorNamespace,
			"--from-file=kubeconfig="+inClusterKubeconfigFile,
		)
		Expect(err).NotTo(HaveOccurred())

		By("reconfiguring the deployed operator on the management cluster into remote-cluster mode")
		remoteModeReconfigured = true
		configureOperatorDeploymentForRemoteMode()
		waitForSingleReadyOperatorPod(remoteKubeconfigMountPath)
	})

	It("ignores management StorageClasses and reconciles remote StorageClasses via live watch", func() {
		By("creating a decoy StorageClass on the local management cluster that must be ignored in remote mode")
		_, err := runKubectlWithStdin(
			"", storageClassYAML(localDecoyStorageClass, remoteTestTenantName, "gold"), "apply", "-f", "-",
		)
		Expect(err).NotTo(HaveOccurred())

		By("creating the tenant storage hub Secret and Tenant CR on the management cluster")
		_, err = runKubectlWithStdin(
			"", tenantStorageSecretYAML(remoteTestSecretName, operatorNamespace, remoteTestTenantName), "apply", "-f", "-",
		)
		Expect(err).NotTo(HaveOccurred())

		_, err = runKubectlWithStdin("", tenantCRYAML(remoteTestTenantName, operatorNamespace), "apply", "-f", "-")
		Expect(err).NotTo(HaveOccurred())

		By("verifying StorageReconciler ignores the local decoy StorageClass and reports ClusterStorageReady=False")
		Eventually(func(g Gomega) {
			status := fetchTenantStatus(g, remoteTestTenantName)
			g.Expect(status.Phase).To(Equal("Ready"))

			backendCond := findCondition(status.Conditions, "StorageBackendReady")
			g.Expect(backendCond).NotTo(BeNil())
			g.Expect(backendCond.Status).To(Equal("True"))

			clusterCond := findCondition(status.Conditions, "ClusterStorageReady")
			g.Expect(clusterCond).NotTo(BeNil())
			g.Expect(clusterCond.Status).To(Equal("False"))
			g.Expect(clusterCond.Reason).To(Equal("NotFound"))
			g.Expect(status.StorageClasses).To(BeEmpty())
		}, 60*time.Second, 2*time.Second).Should(Succeed())

		By("creating a matching StorageClass on the remote Kind cluster without touching the management Tenant CR")
		_, err = runKubectlWithStdin(
			remoteKubeconfigPath,
			storageClassYAML(remoteWorkloadStorageClass, remoteTestTenantName, "gold"),
			"apply", "-f", "-",
		)
		Expect(err).NotTo(HaveOccurred())

		By("verifying the remote StorageClass watch triggers reconciliation and populates management Tenant.status")
		Eventually(func(g Gomega) {
			status := fetchTenantStatus(g, remoteTestTenantName)

			clusterCond := findCondition(status.Conditions, "ClusterStorageReady")
			g.Expect(clusterCond).NotTo(BeNil())
			g.Expect(clusterCond.Status).To(Equal("True"))
			g.Expect(clusterCond.Reason).To(Equal("Found"))
			g.Expect(clusterCond.Message).To(ContainSubstring(remoteWorkloadStorageClass))
			g.Expect(clusterCond.Message).NotTo(ContainSubstring(localDecoyStorageClass))

			g.Expect(status.ClusterStorage).To(ContainElement(tenantClusterStorageEntry{
				ClusterName: "remote",
				Ready:       true,
				Reason:      "Found",
			}))
			g.Expect(status.StorageClasses).To(Equal([]tenantResolvedStorageClass{
				{Name: remoteWorkloadStorageClass, Tier: "gold"},
			}))
		}, 60*time.Second, 2*time.Second).Should(Succeed())
	})
})

type tenantConditionEntry struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

type tenantClusterStorageEntry struct {
	ClusterName string `json:"clusterName"`
	Ready       bool   `json:"ready"`
	Reason      string `json:"reason"`
}

type tenantResolvedStorageClass struct {
	Name string `json:"name"`
	Tier string `json:"tier"`
}

type tenantStatusSnapshot struct {
	Phase          string                       `json:"phase"`
	Conditions     []tenantConditionEntry       `json:"conditions"`
	ClusterStorage []tenantClusterStorageEntry  `json:"clusterStorage"`
	StorageClasses []tenantResolvedStorageClass `json:"storageClasses"`
}

// fetchTenantStatus reads and unmarshals the Tenant status from the management cluster.
func fetchTenantStatus(g Gomega, name string) tenantStatusSnapshot {
	out, err := runKubectl("get", "tenant", name, "-n", operatorNamespace, "-o", "json")
	g.Expect(err).NotTo(HaveOccurred())

	var wrapper struct {
		Status tenantStatusSnapshot `json:"status"`
	}
	g.Expect(json.Unmarshal(out, &wrapper)).To(Succeed())
	return wrapper.Status
}

// findCondition returns a pointer to the named condition in conds, or nil if absent.
func findCondition(conds []tenantConditionEntry, condType string) *tenantConditionEntry {
	for i := range conds {
		if conds[i].Type == condType {
			return &conds[i]
		}
	}
	return nil
}

// runKind executes a kind CLI command with a 60s timeout.
func runKind(args ...string) ([]byte, error) {
	return runKindWithTimeout(60*time.Second, args...)
}

// runKindWithTimeout executes a kind CLI command with the given timeout and
// container runtime provider environment.
func runKindWithTimeout(timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kind", args...)
	cmd.Env = os.Environ()
	if os.Getenv("KIND_EXPERIMENTAL_PROVIDER") == "" && os.Getenv("CONTAINER_TOOL") == "podman" {
		cmd.Env = append(cmd.Env, "KIND_EXPERIMENTAL_PROVIDER=podman")
	}
	return utils.Run(cmd)
}

// runKubectlWithKubeconfig runs kubectl against the specified kubeconfig path.
func runKubectlWithKubeconfig(kubeconfigPath string, args ...string) ([]byte, error) {
	if kubeconfigPath != "" {
		args = append([]string{"--kubeconfig", kubeconfigPath}, args...)
	}
	return runKubectl(args...)
}

// runKubectlWithStdin runs kubectl with stdin populated from manifest.
func runKubectlWithStdin(kubeconfigPath, manifest string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), kubectlCommandTimeout)
	defer cancel()
	if kubeconfigPath != "" {
		args = append([]string{"--kubeconfig", kubeconfigPath}, args...)
	}
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	cmd.Stdin = strings.NewReader(manifest)
	return utils.Run(cmd)
}

// containsLine reports whether output contains a line equal to target.
func containsLine(output, target string) bool {
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == target {
			return true
		}
	}
	return false
}

// lastNonEmptyLine returns the last non-empty trimmed line of output so stderr
// warnings emitted by kubectl before stdout do not break single-token checks.
func lastNonEmptyLine(output string) string {
	lines := strings.Split(output, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if trimmed := strings.TrimSpace(lines[i]); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// writeRemoteInClusterKubeconfig loads the remote Kind kubeconfig via clientcmd,
// points the cluster server at the control-plane node InternalIP on the shared
// Kind bridge network, sets the ServiceAccount bearer credential in AuthInfos,
// and writes the updated kubeconfig to destPath without logging credentials.
func writeRemoteInClusterKubeconfig(remoteKubeconfigPath, destPath string) {
	ipOut, err := runKubectlWithKubeconfig(
		remoteKubeconfigPath,
		"get", "nodes", "-o", `jsonpath={.items[0].status.addresses[?(@.type=="InternalIP")].address}`,
	)
	Expect(err).NotTo(HaveOccurred())
	nodeIP := strings.TrimSpace(string(ipOut))
	Expect(nodeIP).NotTo(BeEmpty())

	credOut, err := runKubectlWithKubeconfig(
		remoteKubeconfigPath,
		"create", "token", remoteServiceAccountName, "-n", operatorNamespace, "--duration=1h",
	)
	Expect(err).NotTo(HaveOccurred())
	bearerValue := strings.TrimSpace(string(credOut))
	Expect(bearerValue).NotTo(BeEmpty())

	kubeCfg, err := clientcmd.LoadFromFile(remoteKubeconfigPath)
	Expect(err).NotTo(HaveOccurred())
	Expect(kubeCfg.Clusters).NotTo(BeEmpty())

	var clusterEntryName string
	for name, clusterCfg := range kubeCfg.Clusters {
		clusterEntryName = name
		clusterCfg.Server = "https://" + nodeIP + ":6443"
		break
	}
	Expect(clusterEntryName).NotTo(BeEmpty())

	kubeCfg.AuthInfos = map[string]*clientcmdapi.AuthInfo{
		remoteServiceAccountName: {
			Token: bearerValue,
		},
	}
	kubeCfg.Contexts = map[string]*clientcmdapi.Context{
		"remote": {
			Cluster:   clusterEntryName,
			AuthInfo:  remoteServiceAccountName,
			Namespace: operatorNamespace,
		},
	}
	kubeCfg.CurrentContext = "remote"

	Expect(clientcmd.WriteToFile(*kubeCfg, destPath)).To(Succeed())
}

// configureOperatorDeploymentForRemoteMode patches the deployed operator on the
// management cluster to mount the remote kubeconfig Secret and disable the
// incompatible Cluster and BareMetalInstance controllers.
func configureOperatorDeploymentForRemoteMode() {
	deployName := operatorDeploymentName()
	patch := fmt.Sprintf(`{
  "spec": {
    "template": {
      "spec": {
        "volumes": [
          {
            "name": "remote-kubeconfig",
            "secret": {
              "secretName": %q,
              "items": [
                {
                  "key": "kubeconfig",
                  "path": "remote-cluster-kubeconfig"
                }
              ]
            }
          }
        ],
        "containers": [
          {
            "name": "manager",
            "env": [
              {"name": "OSAC_REMOTE_CLUSTER_KUBECONFIG", "value": %q},
              {"name": "OSAC_AAP_URL", "value": ""},
              {"name": "OSAC_ENABLE_CLUSTER_CONTROLLER", "value": "false"},
              {"name": "OSAC_ENABLE_BAREMETAL_INSTANCE_CONTROLLER", "value": "false"},
              {"name": "OSAC_ENABLE_TENANT_CONTROLLER", "value": "true"},
              {"name": "OSAC_ENABLE_STORAGE_CONTROLLER", "value": "true"},
              {"name": "OSAC_ENABLE_COMPUTE_INSTANCE_CONTROLLER", "value": "true"},
              {"name": "OSAC_ENABLE_NETWORKING_CONTROLLER", "value": "true"},
              {"name": "OSAC_ENABLE_VOLUME_CONTROLLER", "value": "true"}
            ],
            "volumeMounts": [
              {
                "name": "remote-kubeconfig",
                "mountPath": %q,
                "readOnly": true
              }
            ]
          }
        ]
      }
    }
  }
}`, remoteKubeconfigSecretName, remoteKubeconfigMountPath, remoteKubeconfigMountDir)

	patchFile := filepath.Join(GinkgoT().TempDir(), "remote-mode-patch.json")
	Expect(os.WriteFile(patchFile, []byte(patch), 0o600)).To(Succeed())

	_, err := runKubectl(
		"patch", "deployment", deployName, "-n", operatorNamespace,
		"--type=strategic", "--patch-file="+patchFile,
	)
	Expect(err).NotTo(HaveOccurred())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	_, err = utils.Run(exec.CommandContext(
		ctx, "kubectl", "rollout", "status", "deployment/"+deployName, "-n", operatorNamespace, "--timeout=90s",
	))
	Expect(err).NotTo(HaveOccurred())
}

// writeLocalModeRestorePatchFile snapshots the operator Deployment's original
// pod template spec into patchFilePath so it can be restored via --patch-file
// without passing template JSON through command-line arguments.
func writeLocalModeRestorePatchFile(patchFilePath string) {
	savedListJSON, err := runKubectl(
		"get", "deployment", "-l", "app.kubernetes.io/name=operator",
		"-n", operatorNamespace, "-o", "json",
	)
	Expect(err).NotTo(HaveOccurred())

	var list struct {
		Items []struct {
			Spec struct {
				Template json.RawMessage `json:"template"`
			} `json:"spec"`
		} `json:"items"`
	}
	Expect(json.Unmarshal(savedListJSON, &list)).To(Succeed())
	Expect(list.Items).NotTo(BeEmpty())

	patchBody, err := json.Marshal(map[string]any{
		"spec": map[string]any{
			"template": list.Items[0].Spec.Template,
		},
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(os.WriteFile(patchFilePath, patchBody, 0o600)).To(Succeed())
}

// restoreOperatorDeploymentToLocalMode restores the operator Deployment's
// original pod template spec from restorePatchFilePath and verifies rollout.
func restoreOperatorDeploymentToLocalMode(restorePatchFilePath string) {
	deployName := operatorDeploymentName()
	_, err := runKubectl(
		"patch", "deployment", deployName, "-n", operatorNamespace,
		"--type=merge", "--patch-file="+restorePatchFilePath,
	)
	Expect(err).NotTo(HaveOccurred())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	_, err = utils.Run(exec.CommandContext(
		ctx, "kubectl", "rollout", "status", "deployment/"+deployName, "-n", operatorNamespace, "--timeout=90s",
	))
	Expect(err).NotTo(HaveOccurred())
	waitForSingleReadyOperatorPod("")
}

// operatorDeploymentName returns the name of the deployed osac-operator Deployment.
func operatorDeploymentName() string {
	out, err := runKubectl(
		"get", "deployments", "-l", "app.kubernetes.io/name=operator",
		"-n", operatorNamespace, "-o", "jsonpath={.items[0].metadata.name}",
	)
	Expect(err).NotTo(HaveOccurred())
	name := strings.TrimSpace(string(out))
	Expect(name).NotTo(BeEmpty())
	return name
}

// waitForSingleReadyOperatorPod waits until exactly one non-terminating operator
// controller-manager pod is Running, Ready, and carries wantRemoteKubeconfig in
// its OSAC_REMOTE_CLUSTER_KUBECONFIG environment variable.
func waitForSingleReadyOperatorPod(wantRemoteKubeconfig string) {
	Eventually(func() error {
		output, err := runKubectl(
			"get", "pods",
			"-l", "control-plane=controller-manager,app.kubernetes.io/name=operator",
			"-n", operatorNamespace, "-o", "json",
		)
		if err != nil {
			return err
		}
		var podList struct {
			Items []struct {
				Metadata struct {
					Name              string     `json:"name"`
					DeletionTimestamp *time.Time `json:"deletionTimestamp"`
				} `json:"metadata"`
				Spec struct {
					Containers []struct {
						Name string `json:"name"`
						Env  []struct {
							Name  string `json:"name"`
							Value string `json:"value"`
						} `json:"env"`
					} `json:"containers"`
				} `json:"spec"`
				Status struct {
					Phase             string `json:"phase"`
					ContainerStatuses []struct {
						Ready bool `json:"ready"`
					} `json:"containerStatuses"`
				} `json:"status"`
			} `json:"items"`
		}
		if err := json.Unmarshal(output, &podList); err != nil {
			return err
		}
		var readyCount int
		for _, pod := range podList.Items {
			if pod.Metadata.DeletionTimestamp != nil {
				continue
			}
			if pod.Status.Phase != "Running" {
				return fmt.Errorf("pod %s is in phase %s", pod.Metadata.Name, pod.Status.Phase)
			}
			for _, cs := range pod.Status.ContainerStatuses {
				if !cs.Ready {
					return fmt.Errorf("pod %s container not ready", pod.Metadata.Name)
				}
			}
			var gotRemoteKubeconfig string
			for _, c := range pod.Spec.Containers {
				if c.Name != "manager" {
					continue
				}
				for _, env := range c.Env {
					if env.Name == "OSAC_REMOTE_CLUSTER_KUBECONFIG" {
						gotRemoteKubeconfig = env.Value
						break
					}
				}
			}
			if gotRemoteKubeconfig != wantRemoteKubeconfig {
				return fmt.Errorf(
					"pod %s has OSAC_REMOTE_CLUSTER_KUBECONFIG=%q, want %q",
					pod.Metadata.Name, gotRemoteKubeconfig, wantRemoteKubeconfig,
				)
			}
			readyCount++
		}
		if readyCount != 1 {
			return fmt.Errorf("expected 1 ready operator pod, found %d", readyCount)
		}
		return nil
	}, 2*time.Minute, 2*time.Second).Should(Succeed())
}

// remoteRBACManifest returns the Namespace, ServiceAccount, ClusterRole, and
// ClusterRoleBinding manifest applied to the remote Kind cluster.
func remoteRBACManifest() string {
	return fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: %s
  namespace: %s
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: osac-remote-storage-role
rules:
- apiGroups: ["storage.k8s.io"]
  resources: ["storageclasses"]
  verbs: ["get", "list", "watch"]
- apiGroups: [""]
  resources: ["namespaces"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
- apiGroups: ["k8s.ovn.org"]
  resources: ["userdefinednetworks"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
- apiGroups: ["kubevirt.io"]
  resources: ["virtualmachines", "virtualmachineinstances"]
  verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: osac-remote-storage-rolebinding
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: osac-remote-storage-role
subjects:
- kind: ServiceAccount
  name: %s
  namespace: %s
`, operatorNamespace, remoteServiceAccountName, operatorNamespace, remoteServiceAccountName, operatorNamespace)
}

// storageClassYAML returns a StorageClass manifest labeled for tenantName and tierName.
func storageClassYAML(scName, tenantName, tierName string) string {
	return fmt.Sprintf(`apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: %s
  labels:
    osac.openshift.io/tenant: %s
    osac.openshift.io/storage-tier: %s
provisioner: kubernetes.io/no-provisioner
volumeBindingMode: WaitForFirstConsumer
`, scName, tenantName, tierName)
}

// tenantStorageSecretYAML returns a tenant storage hub Secret manifest.
func tenantStorageSecretYAML(secretName, namespace, tenantName string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
  labels:
    osac.openshift.io/tenant: %s
    osac.openshift.io/storage-provider: vast
type: Opaque
stringData:
  provider: vast
`, secretName, namespace, tenantName)
}

// tenantCRYAML returns a minimal Tenant CR manifest.
func tenantCRYAML(name, namespace string) string {
	return fmt.Sprintf(`apiVersion: osac.openshift.io/v1alpha1
kind: Tenant
metadata:
  name: %s
  namespace: %s
spec: {}
`, name, namespace)
}
