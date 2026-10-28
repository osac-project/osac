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
	remoteKindClusterName            = "osac-remote"
	remoteKubeconfigSecretName       = "osac-remote-kubeconfig"
	remoteKubeconfigMountDir         = "/etc/osac-remote"
	remoteKubeconfigMountPath        = "/etc/osac-remote/remote-cluster-kubeconfig"
	remoteServiceAccountName         = "osac-remote-access"
	remoteRestrictedSAName           = "osac-remote-no-storage"
	remoteTestTenantName             = "tenant-two-kind"
	remoteTestSecretName             = "vast-tenant-config-tenant-two-kind"
	remoteForbiddenTenantName        = "tenant-two-kind-forbidden"
	remoteForbiddenSecretName        = "vast-tenant-config-tenant-two-kind-forbidden"
	localDecoyStorageClass           = "local-mgmt-only-gold-sc"
	localForbiddenDecoyStorageClass  = "local-mgmt-forbidden-gold-sc"
	remoteWorkloadStorageClass       = "remote-workload-gold-sc"
	remoteWorkloadSilverStorageClass = "remote-workload-silver-sc"
	remoteForbiddenStorageClass      = "remote-forbidden-gold-sc"
)

var _ = Describe("Remote cluster StorageClass reconciliation across two Kind clusters", Ordered, Serial, func() {
	var (
		remoteKubeconfigPath   string
		restorePatchFilePath   string
		remoteClusterCreated   bool
		remoteSecretCreated    bool
		remoteModeReconfigured bool
	)

	BeforeAll(func() {
		_, lookErr := exec.LookPath("kind")
		Expect(lookErr).NotTo(HaveOccurred(), "kind binary must be present on PATH")

		clustersOut, err := runKind("get", "clusters")
		Expect(err).NotTo(HaveOccurred())
		mgmtCluster := mgmtKindClusterName()
		Expect(mgmtCluster).NotTo(
			Equal(remoteKindClusterName),
			"KIND_CLUSTER_NAME must not equal the remote test cluster name %q", remoteKindClusterName,
		)
		Expect(containsLine(string(clustersOut), mgmtCluster)).To(
			BeTrue(), "management Kind cluster %q must exist in `kind get clusters`", mgmtCluster,
		)
		Expect(containsLine(string(clustersOut), remoteKindClusterName)).To(
			BeFalse(), "remote Kind cluster %q must not already exist before test setup", remoteKindClusterName,
		)

		By("snapshotting the management cluster operator Deployment template to a restore patch file")
		restorePatchFilePath = filepath.Join(GinkgoT().TempDir(), "local-mode-restore-patch.json")
		writeLocalModeRestorePatchFile(restorePatchFilePath)

		remoteKubeconfigPath = filepath.Join(GinkgoT().TempDir(), "osac-remote.kubeconfig")

		DeferCleanup(func() {
			By("cleaning up test resources and restoring the operator Deployment to single-cluster mode")
			restorePatchApplied := !remoteModeReconfigured
			defer func() {
				if !restorePatchApplied {
					Fail("skipping remote Secret and Kind cluster deletion because operator Deployment restore patch failed")
					return
				}
				var secretErr, kindErr error
				if remoteSecretCreated {
					_, secretErr = runMgmtKubectl(
						"delete", "secret", remoteKubeconfigSecretName,
						"-n", operatorNamespace, "--ignore-not-found",
					)
				}
				if remoteClusterCreated {
					_, kindErr = runKind(
						"delete", "cluster", "--name", remoteKindClusterName,
						"--kubeconfig", remoteKubeconfigPath,
					)
				}
				Expect(secretErr).NotTo(HaveOccurred())
				Expect(kindErr).NotTo(HaveOccurred())
			}()

			var cleanupErrs []error
			for _, tName := range []string{remoteTestTenantName, remoteForbiddenTenantName} {
				_, delErr := runMgmtKubectl(
					"delete", "tenant", tName,
					"-n", operatorNamespace, "--ignore-not-found", "--wait=false",
				)
				if delErr != nil {
					cleanupErrs = append(cleanupErrs, delErr)
				}
				if finErr := removeMgmtTenantFinalizers(tName); finErr != nil {
					cleanupErrs = append(cleanupErrs, finErr)
				}
			}
			for _, secName := range []string{remoteTestSecretName, remoteForbiddenSecretName} {
				_, secErr := runMgmtKubectl(
					"delete", "secret", secName,
					"-n", operatorNamespace, "--ignore-not-found",
				)
				if secErr != nil {
					cleanupErrs = append(cleanupErrs, secErr)
				}
			}
			for _, scName := range []string{localDecoyStorageClass, localForbiddenDecoyStorageClass} {
				_, scErr := runMgmtKubectl("delete", "sc", scName, "--ignore-not-found")
				if scErr != nil {
					cleanupErrs = append(cleanupErrs, scErr)
				}
			}

			var restoreErr error
			if remoteModeReconfigured {
				restorePatchApplied, restoreErr = restoreOperatorDeploymentToLocalMode(restorePatchFilePath)
			}
			Expect(restoreErr).NotTo(HaveOccurred())
			Expect(cleanupErrs).To(BeEmpty())
		})

		By("creating the second Kind workload cluster (" + remoteKindClusterName + ")")
		remoteClusterCreated = true
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
		writeRemoteInClusterKubeconfig(remoteKubeconfigPath, inClusterKubeconfigFile, remoteServiceAccountName)

		_, err = runMgmtKubectl("delete", "secret", remoteKubeconfigSecretName, "-n", operatorNamespace, "--ignore-not-found")
		Expect(err).NotTo(HaveOccurred())
		_, err = runMgmtKubectl(
			"create", "secret", "generic", remoteKubeconfigSecretName,
			"-n", operatorNamespace,
			"--from-file=kubeconfig="+inClusterKubeconfigFile,
		)
		Expect(err).NotTo(HaveOccurred())
		remoteSecretCreated = true

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

		By("creating a second-tier StorageClass on the remote Kind cluster and verifying live watch updates")
		_, err = runKubectlWithStdin(
			remoteKubeconfigPath,
			storageClassYAML(remoteWorkloadSilverStorageClass, remoteTestTenantName, "silver"),
			"apply", "-f", "-",
		)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func(g Gomega) {
			status := fetchTenantStatus(g, remoteTestTenantName)
			g.Expect(status.StorageClasses).To(Equal([]tenantResolvedStorageClass{
				{Name: remoteWorkloadStorageClass, Tier: "gold"},
				{Name: remoteWorkloadSilverStorageClass, Tier: "silver"},
			}))
		}, 60*time.Second, 2*time.Second).Should(Succeed())
	})

	It(
		"rotates the remote kubeconfig Secret via watchRemoteKubeconfig, fails closed on Forbidden RBAC, and recovers",
		func() {
			By("provisioning a restricted ServiceAccount on the remote Kind cluster without StorageClass RBAC")
			_, err := runKubectlWithStdin(
				remoteKubeconfigPath, remoteRestrictedRBACManifest(false), "apply", "-f", "-",
			)
			Expect(err).NotTo(HaveOccurred())

			restrictedIdentity := fmt.Sprintf("system:serviceaccount:%s:%s", operatorNamespace, remoteRestrictedSAName)
			nsOut, err := runKubectlWithKubeconfig(
				remoteKubeconfigPath,
				"auth", "can-i", "-A", "list", "namespaces", "--as="+restrictedIdentity,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(lastNonEmptyLine(string(nsOut))).To(Equal("yes"))

			for _, verb := range []string{"get", "list", "watch"} {
				scOut, scErr := runKubectlWithKubeconfig(
					remoteKubeconfigPath,
					"auth", "can-i", "-A", verb, "storageclasses.storage.k8s.io", "--as="+restrictedIdentity,
				)
				Expect(scErr).To(HaveOccurred(), "expected restricted SA %s on storageclasses to exit non-zero", verb)
				Expect(lastNonEmptyLine(string(scOut))).To(Equal("no"))
			}

			By("rotating the remote kubeconfig Secret on the management cluster without modifying the Deployment")
			podName, initialRestarts := currentOperatorPodRestartCount()
			restrictedKubeconfigFile := filepath.Join(GinkgoT().TempDir(), "restricted-remote.kubeconfig")
			writeRemoteInClusterKubeconfig(remoteKubeconfigPath, restrictedKubeconfigFile, remoteRestrictedSAName)
			updateRemoteKubeconfigSecretAndTriggerSync(restrictedKubeconfigFile, podName)

			By("verifying watchRemoteKubeconfig detects the mounted Secret digest change and restarts the manager container")
			waitForOperatorContainerRestart(podName, initialRestarts)

			By("creating remote and local decoy StorageClasses and a second Tenant while StorageClass RBAC is forbidden")
			_, err = runKubectlWithStdin(
				remoteKubeconfigPath,
				storageClassYAML(remoteForbiddenStorageClass, remoteForbiddenTenantName, "gold"),
				"apply", "-f", "-",
			)
			Expect(err).NotTo(HaveOccurred())

			_, err = runKubectlWithStdin(
				"", storageClassYAML(localForbiddenDecoyStorageClass, remoteForbiddenTenantName, "gold"), "apply", "-f", "-",
			)
			Expect(err).NotTo(HaveOccurred())

			_, err = runKubectlWithStdin(
				"",
				tenantStorageSecretYAML(remoteForbiddenSecretName, operatorNamespace, remoteForbiddenTenantName),
				"apply", "-f", "-",
			)
			Expect(err).NotTo(HaveOccurred())

			_, err = runKubectlWithStdin("", tenantCRYAML(remoteForbiddenTenantName, operatorNamespace), "apply", "-f", "-")
			Expect(err).NotTo(HaveOccurred())

			By("verifying the operator logs the remote StorageClass Forbidden error and fails closed without local fallback")
			Eventually(func(g Gomega) {
				logsOut, logErr := runMgmtKubectl("logs", podName, "-n", operatorNamespace, "-c", "manager")
				g.Expect(logErr).NotTo(HaveOccurred())
				g.Expect(string(logsOut)).To(ContainSubstring("storageclasses.storage.k8s.io is forbidden"))
			}, 60*time.Second, 2*time.Second).Should(Succeed())

			Consistently(func(g Gomega) {
				forbiddenStatus := fetchTenantStatus(g, remoteForbiddenTenantName)
				g.Expect(forbiddenStatus.StorageClasses).To(
					BeEmpty(), "StorageReconciler must fail closed and never fall back to local StorageClasses",
				)
				clusterCond := findCondition(forbiddenStatus.Conditions, "ClusterStorageReady")
				if clusterCond != nil {
					g.Expect(clusterCond.Status).NotTo(Equal("True"))
				}

				existingStatus := fetchTenantStatus(g, remoteTestTenantName)
				g.Expect(existingStatus.Phase).To(Equal("Ready"))
				existingClusterCond := findCondition(existingStatus.Conditions, "ClusterStorageReady")
				g.Expect(existingClusterCond).NotTo(BeNil())
				g.Expect(existingClusterCond.Status).To(Equal("True"))
				g.Expect(existingStatus.StorageClasses).To(Equal([]tenantResolvedStorageClass{
					{Name: remoteWorkloadStorageClass, Tier: "gold"},
					{Name: remoteWorkloadSilverStorageClass, Tier: "silver"},
				}))
			}, 8*time.Second, time.Second).Should(Succeed())

			By("granting read-only StorageClass RBAC to the restricted ServiceAccount on the remote Kind cluster")
			_, err = runKubectlWithStdin(
				remoteKubeconfigPath, remoteRestrictedRBACManifest(true), "apply", "-f", "-",
			)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the operator recovers automatically and resolves the remote StorageClass")
			Eventually(func(g Gomega) {
				status := fetchTenantStatus(g, remoteForbiddenTenantName)
				g.Expect(status.Phase).To(Equal("Ready"))

				backendCond := findCondition(status.Conditions, "StorageBackendReady")
				g.Expect(backendCond).NotTo(BeNil())
				g.Expect(backendCond.Status).To(Equal("True"))

				clusterCond := findCondition(status.Conditions, "ClusterStorageReady")
				g.Expect(clusterCond).NotTo(BeNil())
				g.Expect(clusterCond.Status).To(Equal("True"))
				g.Expect(clusterCond.Reason).To(Equal("Found"))
				g.Expect(clusterCond.Message).To(ContainSubstring(remoteForbiddenStorageClass))
				g.Expect(clusterCond.Message).NotTo(ContainSubstring(localForbiddenDecoyStorageClass))

				g.Expect(status.ClusterStorage).To(ContainElement(tenantClusterStorageEntry{
					ClusterName: "remote",
					Ready:       true,
					Reason:      "Found",
				}))
				g.Expect(status.StorageClasses).To(Equal([]tenantResolvedStorageClass{
					{Name: remoteForbiddenStorageClass, Tier: "gold"},
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

// mgmtKindClusterName returns the management Kind cluster name from
// KIND_CLUSTER_NAME, defaulting to "osac-dev".
func mgmtKindClusterName() string {
	if name := strings.TrimSpace(os.Getenv("KIND_CLUSTER_NAME")); name != "" {
		return name
	}
	return "osac-dev"
}

// mgmtKubeContext returns the kubectl context name for the management Kind cluster.
func mgmtKubeContext() string {
	return "kind-" + mgmtKindClusterName()
}

// runMgmtKubectl executes kubectl against the management Kind cluster context.
func runMgmtKubectl(args ...string) ([]byte, error) {
	args = append([]string{"--context=" + mgmtKubeContext()}, args...)
	return runKubectl(args...)
}

// removeMgmtTenantFinalizers clears finalizers on a Tenant in the management
// cluster, ignoring NotFound when the Tenant has already been deleted.
func removeMgmtTenantFinalizers(name string) error {
	out, err := runMgmtKubectl(
		"patch", "tenant", name, "-n", operatorNamespace,
		"--type=merge", "-p", `{"metadata":{"finalizers":[]}}`,
	)
	if err != nil && !strings.Contains(string(out), "NotFound") {
		return err
	}
	return nil
}

// fetchTenantStatus reads and unmarshals the Tenant status from the management cluster.
func fetchTenantStatus(g Gomega, name string) tenantStatusSnapshot {
	out, err := runMgmtKubectl("get", "tenant", name, "-n", operatorNamespace, "-o", "json")
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

// runKubectlWithKubeconfig runs kubectl against the specified kubeconfig path,
// or against the management Kind cluster context when kubeconfigPath is empty.
func runKubectlWithKubeconfig(kubeconfigPath string, args ...string) ([]byte, error) {
	if kubeconfigPath != "" {
		args = append([]string{"--kubeconfig", kubeconfigPath}, args...)
		return runKubectl(args...)
	}
	return runMgmtKubectl(args...)
}

// runKubectlWithStdin runs kubectl with stdin populated from manifest, binding
// to the management Kind cluster context when kubeconfigPath is empty.
func runKubectlWithStdin(kubeconfigPath, manifest string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), kubectlCommandTimeout)
	defer cancel()
	if kubeconfigPath != "" {
		args = append([]string{"--kubeconfig", kubeconfigPath}, args...)
	} else {
		args = append([]string{"--context=" + mgmtKubeContext()}, args...)
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
func writeRemoteInClusterKubeconfig(remoteKubeconfigPath, destPath, serviceAccountName string) {
	ipOut, err := runKubectlWithKubeconfig(
		remoteKubeconfigPath,
		"get", "nodes", "-o", `jsonpath={.items[0].status.addresses[?(@.type=="InternalIP")].address}`,
	)
	Expect(err).NotTo(HaveOccurred())
	nodeIP := strings.TrimSpace(string(ipOut))
	Expect(nodeIP).NotTo(BeEmpty())

	credOut, err := runKubectlWithKubeconfig(
		remoteKubeconfigPath,
		"create", "token", serviceAccountName, "-n", operatorNamespace, "--duration=1h",
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
		serviceAccountName: {
			Token: bearerValue,
		},
	}
	kubeCfg.Contexts = map[string]*clientcmdapi.Context{
		"remote": {
			Cluster:   clusterEntryName,
			AuthInfo:  serviceAccountName,
			Namespace: operatorNamespace,
		},
	}
	kubeCfg.CurrentContext = "remote"

	Expect(clientcmd.WriteToFile(*kubeCfg, destPath)).To(Succeed())
}

// updateRemoteKubeconfigSecretAndTriggerSync updates the remote kubeconfig Secret
// on the management cluster and annotates the running operator Pod so kubelet
// immediately syncs the projected Secret volume on disk.
func updateRemoteKubeconfigSecretAndTriggerSync(kubeconfigFile, podName string) {
	renderedSecret, err := runMgmtKubectl(
		"create", "secret", "generic", remoteKubeconfigSecretName,
		"-n", operatorNamespace,
		"--from-file=kubeconfig="+kubeconfigFile,
		"--dry-run=client", "-o", "yaml",
	)
	Expect(err).NotTo(HaveOccurred())

	_, err = runKubectlWithStdin("", string(renderedSecret), "apply", "-f", "-")
	Expect(err).NotTo(HaveOccurred())

	syncStamp := fmt.Sprintf("osac.openshift.io/kubeconfig-sync=%d", time.Now().UnixNano())
	_, err = runMgmtKubectl(
		"annotate", "pod", podName, "-n", operatorNamespace, syncStamp, "--overwrite",
	)
	Expect(err).NotTo(HaveOccurred())
}

// currentOperatorPodRestartCount returns the name and current manager container
// restartCount of the running operator controller-manager Pod.
func currentOperatorPodRestartCount() (string, int) {
	output, err := runMgmtKubectl(
		"get", "pods",
		"-l", "control-plane=controller-manager,app.kubernetes.io/name=operator",
		"-n", operatorNamespace, "-o", "json",
	)
	Expect(err).NotTo(HaveOccurred())

	var podList struct {
		Items []struct {
			Metadata struct {
				Name              string     `json:"name"`
				DeletionTimestamp *time.Time `json:"deletionTimestamp"`
			} `json:"metadata"`
			Status struct {
				ContainerStatuses []struct {
					Name         string `json:"name"`
					RestartCount int    `json:"restartCount"`
				} `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
	Expect(json.Unmarshal(output, &podList)).To(Succeed())

	for _, pod := range podList.Items {
		if pod.Metadata.DeletionTimestamp != nil {
			continue
		}
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.Name == "manager" {
				return pod.Metadata.Name, cs.RestartCount
			}
		}
	}
	Fail("running operator controller-manager pod not found")
	return "", 0
}

// waitForOperatorContainerRestart waits for watchRemoteKubeconfig to restart the
// manager container in podName and verifies the previous container log records
// the kubeconfig digest change.
func waitForOperatorContainerRestart(podName string, initialRestarts int) {
	Eventually(func(g Gomega) {
		output, err := runMgmtKubectl("get", "pod", podName, "-n", operatorNamespace, "-o", "json")
		g.Expect(err).NotTo(HaveOccurred())

		var pod struct {
			Status struct {
				Phase             string `json:"phase"`
				ContainerStatuses []struct {
					Name         string `json:"name"`
					Ready        bool   `json:"ready"`
					RestartCount int    `json:"restartCount"`
				} `json:"containerStatuses"`
			} `json:"status"`
		}
		g.Expect(json.Unmarshal(output, &pod)).To(Succeed())
		g.Expect(pod.Status.Phase).To(Equal("Running"))

		var managerStatus *struct {
			Name         string `json:"name"`
			Ready        bool   `json:"ready"`
			RestartCount int    `json:"restartCount"`
		}
		for i := range pod.Status.ContainerStatuses {
			if pod.Status.ContainerStatuses[i].Name == "manager" {
				managerStatus = &pod.Status.ContainerStatuses[i]
				break
			}
		}
		g.Expect(managerStatus).NotTo(BeNil())
		g.Expect(managerStatus.RestartCount).To(BeNumerically(">", initialRestarts))
		g.Expect(managerStatus.Ready).To(BeTrue())

		prevLogs, logErr := runMgmtKubectl(
			"logs", podName, "-n", operatorNamespace, "-c", "manager", "--previous",
		)
		g.Expect(logErr).NotTo(HaveOccurred())
		g.Expect(string(prevLogs)).To(ContainSubstring("remote cluster kubeconfig changed; restarting operator"))
	}, 60*time.Second, 2*time.Second).Should(Succeed())
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

	_, err := runMgmtKubectl(
		"patch", "deployment", deployName, "-n", operatorNamespace,
		"--type=strategic", "--patch-file="+patchFile,
	)
	Expect(err).NotTo(HaveOccurred())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	_, err = utils.Run(exec.CommandContext(
		ctx, "kubectl", "--context="+mgmtKubeContext(),
		"rollout", "status", "deployment/"+deployName, "-n", operatorNamespace, "--timeout=90s",
	))
	Expect(err).NotTo(HaveOccurred())
}

// writeLocalModeRestorePatchFile snapshots the operator Deployment's original
// pod template spec into patchFilePath so it can be restored via --patch-file
// without passing template JSON through command-line arguments.
func writeLocalModeRestorePatchFile(patchFilePath string) {
	savedListJSON, err := runMgmtKubectl(
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
// It returns patchApplied=true once the Deployment patch succeeds so callers
// can distinguish a patch failure from a subsequent rollout timeout.
func restoreOperatorDeploymentToLocalMode(restorePatchFilePath string) (bool, error) {
	deployName := operatorDeploymentName()
	if _, err := runMgmtKubectl(
		"patch", "deployment", deployName, "-n", operatorNamespace,
		"--type=merge", "--patch-file="+restorePatchFilePath,
	); err != nil {
		return false, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if _, err := utils.Run(exec.CommandContext(
		ctx, "kubectl", "--context="+mgmtKubeContext(),
		"rollout", "status", "deployment/"+deployName, "-n", operatorNamespace, "--timeout=90s",
	)); err != nil {
		return true, err
	}
	waitForSingleReadyOperatorPod("")
	return true, nil
}

// operatorDeploymentName returns the name of the deployed osac-operator Deployment.
func operatorDeploymentName() string {
	out, err := runMgmtKubectl(
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
		output, err := runMgmtKubectl(
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

// remoteRestrictedRBACManifest returns the ServiceAccount, ClusterRole, and
// ClusterRoleBinding manifest for remoteRestrictedSAName, optionally including
// read-only StorageClass permissions when allowStorageClasses is true.
func remoteRestrictedRBACManifest(allowStorageClasses bool) string {
	storageClassRule := ""
	if allowStorageClasses {
		storageClassRule = `- apiGroups: ["storage.k8s.io"]
  resources: ["storageclasses"]
  verbs: ["get", "list", "watch"]
`
	}
	return fmt.Sprintf(`apiVersion: v1
kind: ServiceAccount
metadata:
  name: %s
  namespace: %s
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: osac-remote-no-storage-role
rules:
%s- apiGroups: [""]
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
  name: osac-remote-no-storage-rolebinding
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: osac-remote-no-storage-role
subjects:
- kind: ServiceAccount
  name: %s
  namespace: %s
`, remoteRestrictedSAName, operatorNamespace, storageClassRule, remoteRestrictedSAName, operatorNamespace)
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
