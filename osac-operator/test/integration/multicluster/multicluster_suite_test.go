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
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
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
	kubeconfigMountPath  = "/var/run/secrets/remote/kubeconfig"
	kubectlTimeout       = 30 * time.Second
	testRunLabelKey      = "multicluster-test-run"
)

// testRunID uniquely identifies this test run so that resource cleanup is
// scoped to the resources created by this specific run.
var testRunID = generateRunID()

// testSubnetName is the name of the Subnet CR created on the hub cluster.
// The ComputeInstance controller resolves this Subnet to determine the
// subnet-target-namespace annotation (the workload-cluster namespace where
// AAP creates the VirtualMachine). Using a test-specific name (rather than
// the operator namespace) proves that the controller exercises
// resolveSubnetTargetNamespace() instead of falling back to the tenant name.
var testSubnetName = "mc-subnet-" + testRunID

// workloadHostKubeconfig is the path to a kubeconfig file that can be used from
// the test host (127.0.0.1:<nodePort>) to reach the workload cluster.
var workloadHostKubeconfig string

// originalDeploymentName is the operator Deployment name saved during patch so
// that restoreOperatorDeployment can address it without re-querying.
var originalDeploymentName string

// originalDeploymentRevision is the Deployment's revision annotation before
// the suite's patch, so restoreOperatorDeployment targets the exact revision.
var originalDeploymentRevision string

// operatorPatchSucceeded is set after the Deployment patch completes so
// AfterSuite knows whether cleanup should restore/remove resources.
var operatorPatchSucceeded bool

// operatorDeploymentChanged is set when the patch created a new rollout
// revision. When false, rollback is skipped and the Secret/workload cluster
// are preserved.
var operatorDeploymentChanged bool

var _ = BeforeSuite(func() {
	By("deleting any stale workload cluster from a previous run")
	_, _ = runShell("kind", "delete", "cluster", "--name", workloadClusterName)

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

	By("installing fake OVN UserDefinedNetwork CRDs on the workload cluster")
	_, err = runKubectlWorkload("apply", "-f", filepath.Join(testdataDir, "ovn-udn-crds.yaml"))
	Expect(err).NotTo(HaveOccurred())

	By("waiting for KubeVirt, CDI, and OVN CRDs to be established on workload cluster")
	for _, crd := range []string{
		"virtualmachines.kubevirt.io",
		"virtualmachineinstances.kubevirt.io",
		"datavolumes.cdi.kubevirt.io",
		"userdefinednetworks.k8s.ovn.org",
		"clusteruserdefinednetworks.k8s.ovn.org",
	} {
		crdName := crd // capture for closure
		Eventually(func() error {
			_, err := runKubectlWorkload("get", "crd", crdName)
			return err
		}, 30*time.Second, time.Second).Should(Succeed(),
			"%s CRD should be established on workload cluster", crdName)
	}

	// The workload-cluster namespace where VMs are created is named
	// after the Subnet CR (testSubnetName), not operatorNamespace.  The
	// ComputeInstance controller resolves this via
	// resolveSubnetTargetNamespace → syncSubnetTargetNamespaceAnnotation,
	// which reads the Subnet CR name and stamps the annotation.  The
	// Subnet CR is created in the test; the namespace must exist before
	// AAP attempts to create the VirtualMachine.
	//
	// The networking controller is disabled in this CI profile
	// (operator.controllers.networking=false), so no subnet automation
	// creates this namespace.  A test with full networking coverage
	// would exercise the subnet controller's AAP playbook
	// (cudn_net.create_subnet), which creates the namespace as part of
	// subnet provisioning.
	By("creating subnet-derived target namespace on the workload cluster")
	_, _ = runKubectlWorkload("create", "namespace", testSubnetName)

	By("generating internal kubeconfig for the workload cluster (reachable from hub pods)")
	internalKubeconfig, err := buildInternalKubeconfig()
	Expect(err).NotTo(HaveOccurred())

	By("verifying internal kubeconfig reaches the workload cluster (not the hub)")
	verifyKubeconfigReachesWorkload(internalKubeconfig)

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
	_, err = runKubectlHub("create", "secret", "generic",
		kubeconfigSecretName,
		"--from-file=kubeconfig="+kubeconfigTmpFile.Name(),
		"-n", operatorNamespace)
	Expect(err).NotTo(HaveOccurred())

	By("verifying AWX is accessible from the hub cluster")
	verifyAWXReachable()

	By("updating AWX project to use current branch code")
	updateAWXProjectToCurrentSHA()

	By("waiting for AWX project sync and job templates to be ready")
	waitForAWXProjectAndTemplates()

	By("configuring AWX to mount the remote cluster kubeconfig in job pods")
	configureAWXRemoteKubeconfig()

	By("patching operator deployment with remote cluster configuration")
	patchOperatorForRemoteCluster()

	By("waiting for operator to become healthy with remote cluster config")
	waitForOperatorHealthy()
})

var _ = AfterSuite(func() {
	By("cleaning up ComputeInstance test resources on hub")
	labelSelector := testRunLabelKey + "=" + testRunID
	_, _ = runKubectlHub("delete", "computeinstance", "-l", labelSelector,
		"-n", operatorNamespace, "--wait=false", "--ignore-not-found")
	// Remove finalizers so deletion completes without AAP.
	removeFinalizersByLabel("computeinstance", operatorNamespace, labelSelector)

	By("restoring operator deployment to single-cluster mode")
	restored := restoreOperatorDeployment()

	if restored && operatorPatchSucceeded && operatorDeploymentChanged {
		By("cleaning up kubeconfig Secret")
		_, _ = runKubectlHub("delete", "secret", kubeconfigSecretName,
			"-n", operatorNamespace, "--ignore-not-found")

		By("deleting workload Kind cluster")
		_, _ = runShell("kind", "delete", "cluster", "--name", workloadClusterName)
	} else if !restored {
		_, _ = fmt.Fprintf(GinkgoWriter,
			"WARNING: operator restore failed; preserving kubeconfig Secret "+
				"and workload cluster so the operator keeps running\n")
	} else {
		_, _ = fmt.Fprintf(GinkgoWriter,
			"WARNING: operator deployment was not changed by this suite; "+
				"preserving kubeconfig Secret and workload cluster\n")
	}

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

// buildInternalKubeconfig generates a kubeconfig that uses the
// workload container's Docker-bridge IP directly.  The AWX
// ContainerGroup pod spec sets hostNetwork: true so the runner
// pod shares the Kind node's network namespace, where the Docker
// bridge IP is directly reachable at L2 — no kube-proxy DNAT,
// no MASQUERADE, no return-path ambiguity.
//
// Approach 1 (hostname via kind --internal) failed because
// CoreDNS does not forward to Docker's embedded DNS.
// Approach 2 (raw Docker-bridge IP WITHOUT hostNetwork) failed
// because pod-to-Docker-bridge-IP routing through kube-proxy
// DNAT is unreliable in Kind/CI — the return path from the
// workload container to the pod IP crosses Kind cluster
// boundaries (both clusters share 10.244.0.0/24), so the
// workload node's local cni0 route swallows the response and
// the Ansible module creates the StorageClass on the hub.
// Approach 3 (ClusterIP Service + Endpoints) failed for the
// same reason: kube-proxy DNAT still delivers the packet with
// the pod's source IP, and the return path still breaks.
//
// Approach 4 (current): hostNetwork: true on the AWX runner
// pod + direct Docker-bridge IP.  With hostNetwork the runner
// shares the Kind node's eth0 (172.18.0.2), and 172.18.0.3
// is on the same L2 segment — direct routing, no NAT.
func buildInternalKubeconfig() ([]byte, error) {
	containerName := workloadClusterName + "-control-plane"
	ipOut, err := runShell("docker", "inspect", "-f",
		"{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}",
		containerName)
	if err != nil {
		return nil, fmt.Errorf(
			"get workload container IP: %w", err)
	}
	containerIP := strings.TrimSpace(string(ipOut))
	if containerIP == "" {
		return nil, fmt.Errorf(
			"empty container IP for %s", containerName)
	}

	serverURL := fmt.Sprintf(
		"https://%s:6443", containerIP)

	kubeconfig, err := os.ReadFile(workloadHostKubeconfig)
	if err != nil {
		return nil, err
	}

	// Replace the host-facing 127.0.0.1:<randomPort> server URL
	// with the Docker-bridge IP (reachable from hostNetwork pods).
	re := regexp.MustCompile(
		`server: https://127\.0\.0\.1:\d+`)
	result := re.ReplaceAllString(
		string(kubeconfig),
		"server: "+serverURL,
	)
	// Add insecure-skip-tls-verify since the API server cert
	// does not include the Docker-bridge IP in its SAN list.
	result = strings.Replace(result,
		"certificate-authority-data:",
		"insecure-skip-tls-verify: true\n    # certificate-authority-data:",
		1,
	)

	_, _ = fmt.Fprintf(GinkgoWriter,
		"internal kubeconfig: server=%s "+
			"(hostNetwork direct → %s)\n",
		serverURL, containerIP)
	return []byte(result), nil
}

// verifyKubeconfigReachesWorkload is a preflight check that proves
// the internal kubeconfig (with direct Docker-bridge IP) reaches the
// workload cluster's API server, not the hub's.  It runs from inside
// the hub Kind node container — the same network namespace that
// hostNetwork pods use — and compares the kube-system namespace UID
// returned by the kubeconfig against the hub's kube-system UID.
// Different UIDs prove the kubeconfig targets a different cluster.
func verifyKubeconfigReachesWorkload(
	internalKubeconfig []byte,
) {
	// Get the hub's kube-system UID.
	hubUIDOut, err := runKubectlHub("get", "namespace",
		"kube-system", "-o", "jsonpath={.metadata.uid}")
	Expect(err).NotTo(HaveOccurred(),
		"get hub kube-system UID")
	hubUID := strings.TrimSpace(string(hubUIDOut))
	Expect(hubUID).NotTo(BeEmpty(),
		"hub kube-system UID must not be empty")

	// Write the kubeconfig to a temp file, copy it into the hub
	// Kind node container, and run kubectl from there.  This
	// simulates the network path of a hostNetwork pod.
	hubNodeContainer := "osac-dev-control-plane"
	tmpKC, err := os.CreateTemp("", "preflight-kubeconfig-*")
	Expect(err).NotTo(HaveOccurred())
	defer os.Remove(tmpKC.Name())
	Expect(os.WriteFile(tmpKC.Name(),
		internalKubeconfig, 0600)).To(Succeed())
	_ = tmpKC.Close()

	remotePath := "/tmp/preflight-kubeconfig"
	_, err = runShell("docker", "cp",
		tmpKC.Name(), hubNodeContainer+":"+remotePath)
	Expect(err).NotTo(HaveOccurred(),
		"copy kubeconfig into hub node container")
	defer func() {
		_, _ = runShell("docker", "exec",
			hubNodeContainer, "rm", "-f", remotePath)
	}()

	// From the hub node (hostNetwork context), query the target
	// cluster's kube-system UID via the kubeconfig.
	targetUIDOut, err := runShell("docker", "exec",
		hubNodeContainer,
		"kubectl", "--kubeconfig", remotePath,
		"get", "namespace", "kube-system",
		"-o", "jsonpath={.metadata.uid}")
	Expect(err).NotTo(HaveOccurred(),
		"get target kube-system UID from hub node: %s",
		targetUIDOut)
	targetUID := strings.TrimSpace(string(targetUIDOut))
	Expect(targetUID).NotTo(BeEmpty(),
		"target kube-system UID must not be empty")

	_, _ = fmt.Fprintf(GinkgoWriter,
		"preflight: hub kube-system UID=%s, "+
			"target kube-system UID=%s\n",
		hubUID, targetUID)
	Expect(targetUID).NotTo(Equal(hubUID),
		"kubeconfig must reach the workload cluster, "+
			"not the hub — both returned the same "+
			"kube-system UID, so the kubeconfig "+
			"server URL is routing to the hub API")
}

// updateAWXProjectToCurrentSHA patches the AWX project so it syncs the
// code from the current PR (or branch push) rather than main.
//
// The devstack configure-awx.sh hook creates the project pointing at
// "main", so Ansible changes on a feature branch never reach AWX jobs.
//
// In GitHub Actions PR checkouts the working tree is a depth-1 shallow
// clone of a synthetic merge commit (refs/pull/<N>/merge) whose SHA
// does not exist on any remote branch and whose parents are not fetched
// either (--depth 1).  Walking the Git graph (HEAD^2) therefore fails.
//
// Instead we read the authoritative GITHUB_REF environment variable
// (e.g. "refs/pull/1565/merge") and derive the fetchable PR head ref
// "refs/pull/1565/head".  AWX gets an scm_refspec to fetch that ref
// and scm_branch "pr-head" to check it out.
func updateAWXProjectToCurrentSHA() {
	awxAPI, awxToken := awxEndpoint()

	// Determine the SCM branch and optional refspec from the CI
	// environment.  Prefer GITHUB_REF (authoritative, works with
	// shallow/depth-1 clones) over local Git inspection.
	var branch, refspec string

	githubRef := os.Getenv("GITHUB_REF")
	if m := regexp.MustCompile(
		`^refs/pull/(\d+)/`).FindStringSubmatch(
		githubRef); len(m) > 1 {
		// PR checkout — fetch the PR head ref into a local name
		// that scm_branch can reference.
		prNumber := m[1]
		refspec = fmt.Sprintf(
			"+refs/pull/%s/head:refs/remotes/origin/pr-head",
			prNumber)
		branch = "pr-head"
	} else {
		// Branch push or local run — HEAD is on a real branch
		// that the upstream repo already has.
		headOut, err := runShell("git", "rev-parse", "HEAD")
		Expect(err).NotTo(HaveOccurred(), "resolve HEAD SHA")
		branch = strings.TrimSpace(string(headOut))
	}

	_, _ = fmt.Fprintf(GinkgoWriter,
		"updating AWX project: scm_branch=%s refspec=%q "+
			"(GITHUB_REF=%s)\n",
		branch, refspec, githubRef)

	// Look up the project ID.
	listCmd := fmt.Sprintf(
		`curl -sf "%s/projects/?name=osac-aap" `+
			`-H "Authorization: Bearer %s"`,
		awxAPI, awxToken)
	out, err := runKubectlHub("exec",
		"deploy/awx-task", "-n", operatorNamespace,
		"--", "bash", "-c", listCmd)
	Expect(err).NotTo(HaveOccurred(), "query AWX project")

	var projResp struct {
		Results []struct {
			ID int `json:"id"`
		} `json:"results"`
	}
	Expect(json.Unmarshal(out, &projResp)).To(Succeed())
	Expect(projResp.Results).NotTo(BeEmpty(),
		"AWX project osac-aap must exist")
	projectID := projResp.Results[0].ID

	// Patch scm_branch (and scm_refspec for PR checkouts).
	patchPayload := fmt.Sprintf(
		`{"scm_branch":"%s","scm_refspec":"%s",`+
			`"scm_delete_on_update":true}`,
		branch, refspec)
	patchCmd := fmt.Sprintf(
		`curl -sf -X PATCH "%s/projects/%d/" `+
			`-H "Authorization: Bearer %s" `+
			`-H "Content-Type: application/json" `+
			`-d '%s'`,
		awxAPI, projectID, awxToken, patchPayload)
	_, err = runKubectlHub("exec",
		"deploy/awx-task", "-n", operatorNamespace,
		"--", "bash", "-c", patchCmd)
	Expect(err).NotTo(HaveOccurred(),
		"patch AWX project branch")

	// Trigger a project update (re-sync from Git at the new ref).
	syncCmd := fmt.Sprintf(
		`curl -sf -X POST "%s/projects/%d/update/" `+
			`-H "Authorization: Bearer %s"`,
		awxAPI, projectID, awxToken)
	_, err = runKubectlHub("exec",
		"deploy/awx-task", "-n", operatorNamespace,
		"--", "bash", "-c", syncCmd)
	Expect(err).NotTo(HaveOccurred(),
		"trigger AWX project re-sync")
	_, _ = fmt.Fprintf(GinkgoWriter,
		"triggered AWX project re-sync (branch=%s)\n", branch)
}

// verifyAWXReachable confirms that the AWX service is accessible from the hub
// cluster and that the awx-token Secret exists. This is a prerequisite for the
// operator to dispatch provisioning jobs.
func verifyAWXReachable() {
	// Verify the awx-token Secret exists (created by configure-awx.sh).
	Eventually(func() error {
		_, err := runKubectlHub("get", "secret", "awx-token",
			"-n", operatorNamespace)
		return err
	}, 2*time.Minute, 5*time.Second).Should(Succeed(),
		"awx-token Secret must exist — AWX may not be configured")

	// Verify the AWX service endpoint is resolvable.
	Eventually(func() error {
		_, err := runKubectlHub("get", "service", "awx-service",
			"-n", operatorNamespace)
		return err
	}, 30*time.Second, 5*time.Second).Should(Succeed(),
		"awx-service must exist in the osac namespace")
}

// waitForAWXProjectAndTemplates waits for the AWX project sync to succeed and
// for the compute-instance job templates to exist. The devstack configure-awx.sh
// hook creates the project and templates, but template creation fails silently
// when the project hasn't synced yet (AWX validates playbook paths against the
// project content). This function polls until both preconditions are met.
func waitForAWXProjectAndTemplates() {
	awxAPI, awxToken := awxEndpoint()

	// Wait for at least one AWX project to reach "successful" status.
	// Track whether we already triggered a re-sync so we don't queue
	// repeated /update/ POSTs while the previous one is still running
	// (which keeps the project stuck in "running" indefinitely).
	resyncAttempted := false
	Eventually(func() error {
		listProjects := fmt.Sprintf(
			`curl -sf "%s/projects/?name=osac-aap" `+
				`-H "Authorization: Bearer %s"`,
			awxAPI, awxToken)
		out, err := runKubectlHub("exec",
			"deploy/awx-task", "-n", operatorNamespace, "--",
			"bash", "-c", listProjects)
		if err != nil {
			return fmt.Errorf("list AWX projects: %w\n%s", err, out)
		}
		var resp struct {
			Results []struct {
				ID     int    `json:"id"`
				Status string `json:"status"`
			} `json:"results"`
		}
		if err := json.Unmarshal(out, &resp); err != nil {
			return fmt.Errorf("parse project list: %w", err)
		}
		if len(resp.Results) == 0 {
			return fmt.Errorf("no AWX project named osac-aap found")
		}
		status := resp.Results[0].Status
		if status == "successful" {
			return nil
		}
		if status == "failed" || status == "error" ||
			status == "canceled" {
			// Fetch the last project update stdout for
			// diagnostics (the Git error message lives here).
			projID := resp.Results[0].ID
			diagCmd := fmt.Sprintf(
				`curl -sf "%s/project_updates/`+
					`?project=%d&order_by=-id&page_size=1" `+
					`-H "Authorization: Bearer %s"`,
				awxAPI, projID, awxToken)
			diagOut, _ := runKubectlHub("exec",
				"deploy/awx-task", "-n",
				operatorNamespace, "--",
				"bash", "-c", diagCmd)
			_, _ = fmt.Fprintf(GinkgoWriter,
				"AWX project update diag: %s\n",
				diagOut)

			if !resyncAttempted {
				resyncAttempted = true
				resync := fmt.Sprintf(
					`curl -sf -X POST `+
						`"%s/projects/%d/update/" `+
						`-H "Authorization: `+
						`Bearer %s"`,
					awxAPI, projID, awxToken)
				_, _ = runKubectlHub("exec",
					"deploy/awx-task", "-n",
					operatorNamespace, "--",
					"bash", "-c", resync)
				return fmt.Errorf(
					"AWX project status is %q; "+
						"triggered re-sync",
					status)
			}
		}
		return fmt.Errorf(
			"AWX project status is %q "+
				"(waiting for successful)",
			status)
	}, 5*time.Minute, 10*time.Second).Should(Succeed(),
		"AWX project must sync successfully before templates can be created")

	// Ensure compute-instance job templates exist. The devstack
	// configure-awx.sh hook creates them, but template creation fails
	// silently when the project sync hasn't finished (AWX validates
	// playbook paths). If the hook already exited before sync
	// completed, the templates don't exist and we must create them
	// now that the project is confirmed synced.
	//
	// Look up the project ID and inventory ID that configure-awx.sh
	// created — they are needed for template creation.
	projectID, inventoryID := lookupAWXProjectAndInventory(
		awxAPI, awxToken)

	type tmplDef struct {
		Name     string
		Playbook string
	}
	requiredTemplates := []tmplDef{
		{
			"osac-create-compute-instance",
			"osac-aap/playbook_osac_create_compute_instance.yml",
		},
		{
			"osac-delete-compute-instance",
			"osac-aap/playbook_osac_delete_compute_instance.yml",
		},
	}

	// Post-#1392 configure-awx.sh no longer sets tenant_storage_classes
	// on compute templates; the operator supplies resolved classes via
	// osac_job_vars.  Create templates without extra_vars if missing.
	for _, td := range requiredTemplates {
		getTemplate := fmt.Sprintf(
			`curl -sf "%s/job_templates/?name=%s" `+
				`-H "Authorization: Bearer %s"`,
			awxAPI, td.Name, awxToken)
		out, err := runKubectlHub("exec",
			"deploy/awx-task", "-n", operatorNamespace,
			"--", "bash", "-c", getTemplate)
		Expect(err).NotTo(HaveOccurred(),
			"query template %s", td.Name)

		var resp struct {
			Count int `json:"count"`
		}
		Expect(json.Unmarshal(out, &resp)).To(Succeed(),
			"parse template query for %s", td.Name)

		if resp.Count > 0 {
			_, _ = fmt.Fprintf(GinkgoWriter,
				"template %s already exists\n", td.Name)
			continue
		}

		// Template missing — create it now (project is synced).
		_, _ = fmt.Fprintf(GinkgoWriter,
			"creating missing AWX job template %s\n", td.Name)
		createTmpl := fmt.Sprintf(
			`curl -sf -X POST "%s/job_templates/" `+
				`-H "Authorization: Bearer %s" `+
				`-H "Content-Type: application/json" `+
				`-d '{"name":"%s","organization":1,`+
				`"inventory":%d,"project":%d,`+
				`"playbook":"%s",`+
				`"ask_variables_on_launch":true}'`,
			awxAPI, awxToken, td.Name,
			inventoryID, projectID, td.Playbook)
		createOut, err := runKubectlHub("exec",
			"deploy/awx-task", "-n", operatorNamespace,
			"--", "bash", "-c", createTmpl)
		Expect(err).NotTo(HaveOccurred(),
			"create template %s: %s", td.Name, createOut)
	}
}

// lookupAWXProjectAndInventory returns the IDs of the osac-aap
// project and the OSAC Dev inventory.  configure-awx.sh normally
// creates both, but there is a race with the AWX migration job
// that can cause the inventory to be missing by the time the
// test suite runs.  When the inventory is absent this function
// creates it (and adds a localhost host) so the test is robust.
func lookupAWXProjectAndInventory(
	awxAPI, awxToken string,
) (projectID, inventoryID int) {
	// Project.
	cmd := fmt.Sprintf(
		`curl -sf "%s/projects/?name=osac-aap" `+
			`-H "Authorization: Bearer %s"`,
		awxAPI, awxToken)
	out, err := runKubectlHub("exec",
		"deploy/awx-task", "-n", operatorNamespace,
		"--", "bash", "-c", cmd)
	Expect(err).NotTo(HaveOccurred(), "lookup AWX project")
	var projResp struct {
		Results []struct {
			ID int `json:"id"`
		} `json:"results"`
	}
	Expect(json.Unmarshal(out, &projResp)).To(Succeed())
	Expect(projResp.Results).NotTo(BeEmpty(),
		"AWX project osac-aap must exist")
	projectID = projResp.Results[0].ID

	// Inventory — look up, create if missing.
	inventoryID = lookupOrCreateAWXInventory(awxAPI, awxToken)
	return projectID, inventoryID
}

// lookupOrCreateAWXInventory returns the ID of the "OSAC Dev"
// inventory, creating it (with a localhost host) when absent.
// This mirrors the create-or-reuse logic in
// osac-installer/charts/osac-devstack/files/configure-awx.sh.
func lookupOrCreateAWXInventory(
	awxAPI, awxToken string,
) int {
	const inventoryName = "OSAC Dev"

	// Try to find the existing inventory.
	getCmd := fmt.Sprintf(
		`curl -sf "%s/inventories/?name=OSAC+Dev" `+
			`-H "Authorization: Bearer %s"`,
		awxAPI, awxToken)
	out, err := runKubectlHub("exec",
		"deploy/awx-task", "-n", operatorNamespace,
		"--", "bash", "-c", getCmd)
	Expect(err).NotTo(HaveOccurred(), "query AWX inventory")

	var listResp struct {
		Results []struct {
			ID int `json:"id"`
		} `json:"results"`
	}
	Expect(json.Unmarshal(out, &listResp)).To(Succeed())
	if len(listResp.Results) > 0 {
		_, _ = fmt.Fprintf(GinkgoWriter,
			"AWX inventory %q found (id %d)\n",
			inventoryName, listResp.Results[0].ID)
		return listResp.Results[0].ID
	}

	// Inventory missing — create it with the same payload
	// configure-awx.sh uses.
	_, _ = fmt.Fprintf(GinkgoWriter,
		"AWX inventory %q not found; creating it\n",
		inventoryName)
	createCmd := fmt.Sprintf(
		`curl -sf -X POST "%s/inventories/" `+
			`-H "Authorization: Bearer %s" `+
			`-H "Content-Type: application/json" `+
			`-d '{"name":"%s","organization":1}'`,
		awxAPI, awxToken, inventoryName)
	out, err = runKubectlHub("exec",
		"deploy/awx-task", "-n", operatorNamespace,
		"--", "bash", "-c", createCmd)
	Expect(err).NotTo(HaveOccurred(),
		"create AWX inventory: %s", out)

	var createResp struct {
		ID int `json:"id"`
	}
	Expect(json.Unmarshal(out, &createResp)).To(Succeed())
	Expect(createResp.ID).NotTo(BeZero(),
		"AWX inventory creation must return an ID")
	_, _ = fmt.Fprintf(GinkgoWriter,
		"created AWX inventory %q (id %d)\n",
		inventoryName, createResp.ID)

	// Add a localhost host so AAP jobs can run locally.
	hostCmd := fmt.Sprintf(
		`curl -sf -X POST "%s/inventories/%d/hosts/" `+
			`-H "Authorization: Bearer %s" `+
			`-H "Content-Type: application/json" `+
			`-d '{"name":"localhost",`+
			`"variables":"ansible_connection: local"}'`,
		awxAPI, createResp.ID, awxToken)
	_, _ = runKubectlHub("exec",
		"deploy/awx-task", "-n", operatorNamespace,
		"--", "bash", "-c", hostCmd)

	return createResp.ID
}

// awxEndpoint returns the in-cluster AWX API base URL and a decoded bearer
// token from the awx-token Secret. Fails the test if the Secret is missing.
func awxEndpoint() (apiURL, token string) {
	apiURL = fmt.Sprintf(
		"http://awx-service.%s.svc.cluster.local:80/api/v2",
		operatorNamespace)

	tokenOut, err := runKubectlHub("get", "secret", "awx-token",
		"-n", operatorNamespace,
		"-o", "jsonpath={.data.token}")
	Expect(err).NotTo(HaveOccurred(), "get awx-token Secret")
	b64 := strings.TrimSpace(string(tokenOut))
	Expect(b64).NotTo(BeEmpty(), "awx-token Secret has no token data")
	decoded, err := base64.StdEncoding.DecodeString(b64)
	Expect(err).NotTo(HaveOccurred(), "decode awx-token base64")
	return apiURL, string(decoded)
}

// configureAWXRemoteKubeconfig creates a ContainerGroup in AWX whose pod spec
// mounts the remote-cluster-kubeconfig Secret, then reassigns the compute-instance
// job templates to that group. This mirrors what config-as-code does in production
// via the compute-instance-operations-ig.j2 template when
// REMOTE_CLUSTER_KUBECONFIG_SECRET_NAME is set. Without this, AWX job pods have
// no OSAC_REMOTE_CLUSTER_KUBECONFIG env var and the AAP playbook targets the hub
// cluster instead of the workload cluster.
//
// Precondition: waitForAWXProjectAndTemplates must have confirmed the templates
// exist before this function runs.
func configureAWXRemoteKubeconfig() {
	awxAPI, decodedToken := awxEndpoint()

	// Pod spec for the ContainerGroup mirrors production's
	// compute-instance-operations-ig.j2 with two volume mounts:
	//
	// 1. kube-api-access (projected): hub-cluster service-account token,
	//    CA cert, and namespace at the standard in-cluster path. Without
	//    this, kubernetes.core modules have no configuration to reach the
	//    hub API and fail with "No configuration found." The production
	//    template also includes the OpenShift service-ca.crt ConfigMap;
	//    Kind (vanilla Kubernetes) does not have that, so we omit it.
	//
	// 2. remote-kubeconfig: workload-cluster kubeconfig from the Secret
	//    created in BeforeSuite, exposed via OSAC_REMOTE_CLUSTER_KUBECONFIG.
	//
	// This gives the playbook dual-cluster access: finalizer operations
	// (ComputeInstance CRD on the hub) use in-cluster config, while
	// VM/KubeVirt operations use the remote kubeconfig.
	podSpec := fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  labels:
    ansible_job: ""
spec:
  hostNetwork: true
  dnsPolicy: ClusterFirstWithHostNet
  serviceAccountName: awx-runner
  containers:
    - image: quay.io/ansible/awx-ee:latest
      name: worker
      args:
        - ansible-runner
        - worker
        - "--private-data-dir=/runner"
      env:
        - name: OSAC_REMOTE_CLUSTER_KUBECONFIG
          value: /var/run/secrets/remote/kubeconfig
        - name: ANSIBLE_JINJA2_NATIVE
          value: "true"
      volumeMounts:
        - name: kube-api-access
          mountPath: /var/run/secrets/kubernetes.io/serviceaccount
          readOnly: true
        - name: remote-kubeconfig
          mountPath: /var/run/secrets/remote
          readOnly: true
  volumes:
    - name: kube-api-access
      projected:
        sources:
          - serviceAccountToken:
              path: token
              expirationSeconds: 3600
          - configMap:
              name: kube-root-ca.crt
              items:
                - key: ca.crt
                  path: ca.crt
          - downwardAPI:
              items:
                - path: namespace
                  fieldRef:
                    apiVersion: v1
                    fieldPath: metadata.namespace
        defaultMode: 420
    - name: remote-kubeconfig
      secret:
        secretName: %s
        defaultMode: 420`, kubeconfigSecretName)

	// Create a ContainerGroup (is_container_group: true) via the AWX API.
	// AWX runs on the hub cluster so we can call the API from the test process
	// using kubectl port-forward or directly from a pod. Since the test host
	// cannot reach cluster-internal services, use kubectl exec on an AWX pod
	// to curl the local API.
	createIG := fmt.Sprintf(
		`curl -sf -X POST %s/instance_groups/ `+
			`-H "Authorization: Bearer %s" `+
			`-H "Content-Type: application/json" `+
			`-d '{"name":"multicluster-test-ig","is_container_group":true,"pod_spec_override":%s}'`,
		awxAPI, decodedToken, jsonEscape(podSpec))

	// Run curl from an AWX task pod.
	igOut, err := runKubectlHub("exec",
		"deploy/awx-task", "-n", operatorNamespace, "--",
		"bash", "-c", createIG)
	if err != nil {
		// May already exist from a previous run — try to update instead.
		_, _ = fmt.Fprintf(GinkgoWriter, "ContainerGroup create returned: %v (may already exist)\n%s\n", err, igOut)
	}

	// Get the ContainerGroup ID.
	getIG := fmt.Sprintf(
		`curl -sf %s/instance_groups/?name=multicluster-test-ig `+
			`-H "Authorization: Bearer %s"`,
		awxAPI, decodedToken)
	igListOut, err := runKubectlHub("exec",
		"deploy/awx-task", "-n", operatorNamespace, "--",
		"bash", "-c", getIG)
	Expect(err).NotTo(HaveOccurred(), "list InstanceGroups: %s", igListOut)

	// Parse the ID from the response. Local struct because pkg/aap has
	// no exported InstanceGroup type — its API surface covers only job
	// and template operations, not instance-group management.
	var igList struct {
		Results []struct {
			ID int `json:"id"`
		} `json:"results"`
	}
	Expect(json.Unmarshal(igListOut, &igList)).To(Succeed(), "parse InstanceGroup list")
	Expect(igList.Results).NotTo(BeEmpty(), "multicluster-test-ig not found")
	igID := igList.Results[0].ID

	// Assign the ContainerGroup to compute-instance and storage
	// job templates so the AAP playbooks can reach both the hub
	// (in-cluster ServiceAccount) and the workload cluster
	// (OSAC_REMOTE_CLUSTER_KUBECONFIG volume mount).
	for _, tmpl := range []string{
		"osac-create-compute-instance",
		"osac-delete-compute-instance",
		"osac-create-tenant-cluster-storage",
		"osac-delete-tenant-cluster-storage",
		"osac-create-tenant-storage-backend",
		"osac-delete-tenant-storage-backend",
	} {
		// Get template ID.
		getTemplate := fmt.Sprintf(
			`curl -sf "%s/job_templates/?name=%s" -H "Authorization: Bearer %s"`,
			awxAPI, tmpl, decodedToken)
		tmplOut, err := runKubectlHub("exec",
			"deploy/awx-task", "-n", operatorNamespace, "--",
			"bash", "-c", getTemplate)
		Expect(err).NotTo(HaveOccurred(), "get template %s: %s", tmpl, tmplOut)

		var tmplList struct {
			Results []struct {
				ID int `json:"id"`
			} `json:"results"`
		}
		Expect(json.Unmarshal(tmplOut, &tmplList)).To(Succeed(), "parse template list for %s", tmpl)
		Expect(tmplList.Results).NotTo(BeEmpty(),
			"job template %s must exist — waitForAWXProjectAndTemplates should have ensured this", tmpl)
		tmplID := tmplList.Results[0].ID

		// Associate the ContainerGroup with the template.
		assocIG := fmt.Sprintf(
			`curl -sf -X POST %s/job_templates/%d/instance_groups/ `+
				`-H "Authorization: Bearer %s" `+
				`-H "Content-Type: application/json" `+
				`-d '{"id":%d}'`,
			awxAPI, tmplID, decodedToken, igID)
		assocOut, err := runKubectlHub("exec",
			"deploy/awx-task", "-n", operatorNamespace, "--",
			"bash", "-c", assocIG)
		if err != nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "WARNING: associate IG with %s: %v\n%s\n", tmpl, err, assocOut)
		} else {
			_, _ = fmt.Fprintf(GinkgoWriter, "associated ContainerGroup %d with template %s (id=%d)\n", igID, tmpl, tmplID)
		}
	}
}

// jsonEscape returns s as a JSON string value (with outer quotes).
func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// patchOperatorForRemoteCluster patches the operator Deployment to add the
// remote cluster kubeconfig volume/mount and the OSAC_REMOTE_CLUSTER_KUBECONFIG
// env var. This mirrors the strategic-merge patch in
// osac-installer/scripts/setup-remote-cluster.sh (line 174).
//
// Controller enable flags are NOT set here — they are configured at Helm
// install time via the Makefile's EXTRA_HELM_ARGS for the multicluster CI
// target. The existing Helm-deployed AAP configuration (URL, token) is
// preserved; strategic merge only adds/overrides the listed keys.
func patchOperatorForRemoteCluster() {
	deploymentName := getOperatorDeploymentName()

	// Capture the current revision before patching so that
	// restoreOperatorDeployment can target this exact revision.
	revOut, err := runKubectlHub("get", "deployment", deploymentName,
		"-n", operatorNamespace,
		"-o", `jsonpath={.metadata.annotations.deployment\.kubernetes\.io/revision}`)
	Expect(err).NotTo(HaveOccurred(), "get operator deployment revision")
	originalDeploymentRevision = strings.TrimSpace(string(revOut))
	Expect(originalDeploymentRevision).NotTo(BeEmpty(),
		"operator deployment revision must be present")

	patch := fmt.Sprintf(`{
  "spec":{"template":{"spec":{
    "volumes":[{
      "name":"remote-kubeconfig",
      "secret":{"secretName":"%s"}
    }],
    "containers":[{
      "name":"manager",
      "volumeMounts":[{
        "name":"remote-kubeconfig",
        "mountPath":"/var/run/secrets/remote",
        "readOnly":true
      }],
      "env":[{
        "name":"OSAC_REMOTE_CLUSTER_KUBECONFIG",
        "value":"/var/run/secrets/remote/kubeconfig"
      }]
    }]
  }}}
}`, kubeconfigSecretName)

	_, err = runKubectlHub("patch", "deployment", deploymentName,
		"-n", operatorNamespace,
		"--type=strategic", "-p", patch)
	Expect(err).NotTo(HaveOccurred(), "patch operator deployment")

	// Set restore state only after the patch succeeds.
	originalDeploymentName = deploymentName
	operatorPatchSucceeded = true
}

// restoreOperatorDeployment reverts the operator Deployment to its pre-patch
// revision using rollout undo --to-revision. If the patch did not create a new
// revision (no-op strategic merge), the Deployment is unchanged and rollback is
// skipped. Returns true on success. The CI Makefile redeploys from scratch on
// every run, so a restore failure is non-fatal but visible.
func restoreOperatorDeployment() bool {
	if originalDeploymentName == "" {
		return true
	}

	// Check whether the patch actually created a new revision. A
	// strategic-merge patch that matches the existing PodTemplate is a
	// no-op and does not bump the revision. Blindly undoing would revert
	// an unrelated prior revision.
	currentOut, err := runKubectlHub("get", "deployment", originalDeploymentName,
		"-n", operatorNamespace,
		"-o", `jsonpath={.metadata.annotations.deployment\.kubernetes\.io/revision}`)
	if err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter,
			"WARNING: could not determine operator deployment revision: %v\n%s\n",
			err, currentOut)
		return false
	}
	if strings.TrimSpace(string(currentOut)) == originalDeploymentRevision {
		// No new revision was created — nothing to undo.
		return true
	}
	operatorDeploymentChanged = true

	if out, err := runKubectlHub("rollout", "undo",
		"deployment/"+originalDeploymentName,
		"--to-revision="+originalDeploymentRevision,
		"-n", operatorNamespace); err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "WARNING: operator deployment rollout undo failed: %v\n%s\n", err, out)
		return false
	}

	// Wait for the rollout to complete so the original pod is healthy
	// before the suite tears down the workload cluster.
	if out, err := runKubectlHub("rollout", "status",
		"deployment/"+originalDeploymentName,
		"-n", operatorNamespace,
		"--timeout=120s"); err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "WARNING: operator rollout status wait failed: %v\n%s\n", err, out)
		return false
	}
	return true
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

// waitForOperatorHealthy waits for the Deployment rollout triggered by the
// strategic-merge patch to complete. kubectl rollout status tracks the new
// ReplicaSet and exits when all replicas are ready, which is both simpler
// and more reliable than polling pod annotations.
func waitForOperatorHealthy() {
	out, err := runKubectlHub("rollout", "status",
		"deployment/"+originalDeploymentName,
		"-n", operatorNamespace, "--timeout=300s")
	if err != nil {
		dumpOperatorDiagnostics()
		Fail(fmt.Sprintf(
			"operator rollout did not complete within 5m: %v\n%s", err, out))
	}
}

// dumpOperatorDiagnostics prints pod state, events, and recent logs so
// that a rollout failure is actionable from CI output alone.
func dumpOperatorDiagnostics() {
	sel := "control-plane=controller-manager,app.kubernetes.io/name=operator"
	if out, err := runKubectlHub("get", "pods", "-l", sel,
		"-n", operatorNamespace, "-o", "wide"); err == nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "=== Operator pods ===\n%s\n", out)
	}
	if out, err := runKubectlHub("describe", "pods", "-l", sel,
		"-n", operatorNamespace); err == nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "=== Operator pod describe ===\n%s\n", out)
	}
	if out, err := runKubectlHub("logs", "-l", sel,
		"-n", operatorNamespace, "--tail=80"); err == nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "=== Operator logs (last 80) ===\n%s\n", out)
	}
	if out, err := runKubectlHub("get", "events",
		"-n", operatorNamespace,
		"--sort-by=.lastTimestamp"); err == nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "=== Namespace events ===\n%s\n", out)
	}
}

// dumpProvisioningDiagnostics prints the ComputeInstance status, AWX job
// template and project state, AWX job stdout for failed jobs, and
// operator logs so that a provisioning failure is actionable from CI
// output. Called on test failure via DeferCleanup in the provisioning
// spec.
func dumpProvisioningDiagnostics(ciName, namespace string) {
	_, _ = fmt.Fprintf(GinkgoWriter,
		"\n=== Provisioning diagnostics for %s ===\n", ciName)

	// ComputeInstance full YAML (status, conditions, jobHistory).
	if out, err := runKubectlHub("get", "computeinstance",
		ciName, "-n", namespace, "-o", "yaml"); err == nil {
		_, _ = fmt.Fprintf(GinkgoWriter,
			"--- ComputeInstance YAML ---\n%s\n", out)
	} else {
		_, _ = fmt.Fprintf(GinkgoWriter,
			"--- ComputeInstance get failed: %v ---\n", err)
	}

	// AWX project and template state via the AWX API.
	awxAPI, awxToken := awxEndpoint()
	for _, query := range []struct {
		label, path string
	}{
		{"AWX projects", "projects/?page_size=5"},
		{"AWX job templates", "job_templates/?page_size=20"},
		{"AWX jobs (recent)", "jobs/?order_by=-id&page_size=10"},
	} {
		cmd := fmt.Sprintf(
			`curl -sf "%s/%s" -H "Authorization: Bearer %s"`,
			awxAPI, query.path, awxToken)
		if out, err := runKubectlHub("exec",
			"deploy/awx-task", "-n", namespace,
			"--", "bash", "-c", cmd); err == nil {
			_, _ = fmt.Fprintf(GinkgoWriter,
				"--- %s ---\n%s\n", query.label, out)
		}
	}

	// Fetch stdout for failed AWX jobs so the Ansible error is
	// visible without re-running or manual AWX API queries.
	dumpFailedAWXJobStdout(awxAPI, awxToken, namespace)

	// Operator logs (provisioning errors appear here).
	dumpOperatorDiagnostics()
}

// dumpFailedAWXJobStdout queries the 10 most recent AWX jobs, finds
// those with status "failed" or "error", and fetches their stdout via
// the AWX API. This provides the actual Ansible error message that
// caused the failure — the jobs list only shows metadata.
func dumpFailedAWXJobStdout(awxAPI, awxToken, namespace string) {
	listCmd := fmt.Sprintf(
		`curl -sf "%s/jobs/?order_by=-id&page_size=10" `+
			`-H "Authorization: Bearer %s"`,
		awxAPI, awxToken)
	out, err := runKubectlHub("exec",
		"deploy/awx-task", "-n", namespace,
		"--", "bash", "-c", listCmd)
	if err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter,
			"--- AWX job list for stdout fetch failed: %v ---\n", err)
		return
	}

	var resp struct {
		Results []struct {
			ID     int    `json:"id"`
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"results"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter,
			"--- parse AWX job list failed: %v ---\n", err)
		return
	}

	for _, job := range resp.Results {
		// Dump stdout for failed jobs AND for successful
		// storage jobs — a successful storage job that did
		// not create the expected StorageClass is the most
		// common multicluster failure mode.
		isStorage := strings.Contains(job.Name, "storage")
		isFailed := job.Status == "failed" ||
			job.Status == "error"
		if !isFailed && !isStorage {
			continue
		}
		stdoutCmd := fmt.Sprintf(
			`curl -sf "%s/jobs/%d/stdout/?format=txt" `+
				`-H "Authorization: Bearer %s"`,
			awxAPI, job.ID, awxToken)
		stdoutOut, err := runKubectlHub("exec",
			"deploy/awx-task", "-n", namespace,
			"--", "bash", "-c", stdoutCmd)
		if err != nil {
			_, _ = fmt.Fprintf(GinkgoWriter,
				"--- AWX job %d (%s) [%s] stdout "+
					"fetch failed: %v ---\n",
				job.ID, job.Name, job.Status, err)
			continue
		}
		_, _ = fmt.Fprintf(GinkgoWriter,
			"--- AWX job %d (%s) [%s] stdout ---\n%s\n",
			job.ID, job.Name, job.Status, stdoutOut)
	}

	// Tenant status (storage conditions, clusterStorage).
	if out, err := runKubectlHub("get", "tenant",
		"-n", namespace, "-o", "yaml"); err == nil {
		_, _ = fmt.Fprintf(GinkgoWriter,
			"--- Tenant status ---\n%s\n", out)
	}

	// Workload cluster StorageClasses (if reachable).
	if workloadHostKubeconfig != "" {
		if out, err := runKubectlWorkload(
			"get", "storageclasses",
			"-o", "wide"); err == nil {
			_, _ = fmt.Fprintf(GinkgoWriter,
				"--- Workload StorageClasses ---\n%s\n",
				out)
		} else {
			_, _ = fmt.Fprintf(GinkgoWriter,
				"--- Workload StorageClasses failed: "+
					"%v ---\n", err)
		}
	}
}

func removeFinalizers(kind, name, namespace string) {
	if out, err := runKubectlHub("patch", kind, name,
		"-n", namespace, "--type=merge",
		"-p", `{"metadata":{"finalizers":[]}}`); err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "WARNING: remove finalizers from %s/%s failed: %v\n%s\n", kind, name, err, out)
	}
}

func removeFinalizersByLabel(kind, namespace, labelSelector string) {
	output, err := runKubectlHub("get", kind, "-n", namespace,
		"-l", labelSelector,
		"-o", "jsonpath={.items[*].metadata.name}")
	if err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "WARNING: list %s by label %s failed: %v\n", kind, labelSelector, err)
		return
	}
	for name := range strings.SplitSeq(string(output), " ") {
		if name != "" {
			removeFinalizers(kind, name, namespace)
		}
	}
}

// generateRunID returns a short random hex string for labeling test resources.
func generateRunID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// applyYAML writes yamlContent to a temp file and applies it using the
// provided kubectl runner (e.g. runKubectlHub or runKubectlWorkload).
func applyYAML(yamlContent string, kubectlRunner func(...string) ([]byte, error)) ([]byte, error) {
	tmpFile, err := os.CreateTemp("", "multicluster-test-*.yaml")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmpFile.Name())
	if err := os.WriteFile(tmpFile.Name(), []byte(yamlContent), 0600); err != nil {
		return nil, err
	}
	_ = tmpFile.Close()
	return kubectlRunner("apply", "-f", tmpFile.Name())
}

// createOnlyYAML writes yamlContent to a temp file and creates it using
// kubectl create. Unlike applyYAML, this fails on AlreadyExists instead
// of merging into an existing resource, preventing the suite from
// inadvertently adopting (and later deleting) a pre-existing resource.
func createOnlyYAML(yamlContent string, kubectlRunner func(...string) ([]byte, error)) ([]byte, error) {
	tmpFile, err := os.CreateTemp("", "multicluster-test-*.yaml")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmpFile.Name())
	if err := os.WriteFile(tmpFile.Name(), []byte(yamlContent), 0600); err != nil {
		return nil, err
	}
	_ = tmpFile.Close()
	return kubectlRunner("create", "-f", tmpFile.Name())
}

// waitForTenantStorageClass polls the workload cluster for a
// StorageClass labeled osac.openshift.io/tenant=<tenantName>.  The
// real storage path — enabled via lvms.enabled=true and the
// register-local-storage Helm hook — creates a "local" StorageBackend
// and StorageTier in the fulfillment service.  When the Tenant reaches
// Ready, the storage reconciler queries the Tier API, finds no
// tenant-labeled StorageClass on the workload cluster, and triggers the
// osac-create-tenant-cluster-storage AWX template.  The playbook's
// osac.service.storage_provider role then creates the tenant-labeled
// StorageClass.  This function asserts that flow completed before
// ComputeInstance provisioning begins.
func waitForTenantStorageClass(tenantName string) {
	Eventually(func() error {
		out, err := runKubectlWorkload("get", "storageclasses",
			"-l", "osac.openshift.io/tenant="+tenantName,
			"-o", "jsonpath={.items[*].metadata.name}")
		if err != nil {
			return fmt.Errorf("list tenant StorageClasses: %w", err)
		}
		names := strings.TrimSpace(string(out))
		if names == "" {
			return fmt.Errorf(
				"no StorageClass labeled osac.openshift.io/tenant=%s "+
					"found on workload cluster", tenantName)
		}
		_, _ = fmt.Fprintf(GinkgoWriter,
			"tenant StorageClass found: %s\n", names)
		return nil
	}, 5*time.Minute, 10*time.Second).Should(Succeed(),
		"OSAC/AAP should create a tenant-labeled StorageClass on "+
			"the workload cluster via the real storage provisioning "+
			"path (register-local-storage hook → storage reconciler "+
			"→ osac-create-tenant-cluster-storage AWX template)")
}
