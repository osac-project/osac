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
	"sync"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/osac-project/osac/osac-operator/test/utils"
)

const (
	operatorNamespace    = "osac"
	workloadClusterName  = "osac-workload"
	kubeconfigSecretName = "remote-cluster-kubeconfig"
	kubeconfigMountPath  = "/var/run/secrets/remote/kubeconfig"
	kubectlTimeout       = 30 * time.Second
	testRunLabelKey      = "multicluster-test-run"

	// diagnosticArtifactDir is the directory where Kind node
	// bootstrap diagnostics are written on workload-cluster
	// creation failure.  The CI workflow uploads this directory
	// as an artifact.  Set via MULTICLUSTER_ARTIFACT_DIR env;
	// defaults to a temp directory.
	defaultArtifactSubdir = "multicluster-bootstrap-diagnostics"

	// diagnosticTimeout bounds each diagnostic command so a
	// hung kubelet journal or container runtime cannot block
	// the test indefinitely.
	diagnosticTimeout = 30 * time.Second

	// diagnosticJournalLines caps the number of journal lines
	// collected per unit to keep artifact size reasonable.
	diagnosticJournalLines = "500"

	// kindClusterLabelKey is the Docker/OCI label that Kind sets
	// on every node container to identify which cluster it belongs
	// to.  Official key from Kind v0.20+:
	// sigs.k8s.io/kind/pkg/cluster/internal/providers/docker/constants.go
	kindClusterLabelKey = "io.x-k8s.kind.cluster"
)

// kubeletProcessScript is the shell script executed inside the Kind
// node container to collect kubelet's configured limits, actual
// process limits, and FD count.  Captures one MainPID; reports
// PID, race/permission status, and "unavailable" honestly when
// kubelet is not running or /proc entries are unreadable.
//
// FD listing uses a SINGLE captured ls invocation: the listing is
// stored once and word-counted from the captured variable, so a
// process that exits between a guard ls and a counting ls cannot
// produce a false fd_count=0.  Only numeric UID/FD fields are
// printed; no environment/cmdline/FD targets.
const kubeletProcessScript = `set -e
# Unit-level configured limit (separate from process)
NOFILE=$(systemctl show kubelet \
  -p LimitNOFILE --value 2>/dev/null) || NOFILE=""
echo "unit_LimitNOFILE=${NOFILE:-unavailable}"
RESTARTS=$(systemctl show kubelet \
  -p NRestarts --value 2>/dev/null) || RESTARTS=""
echo "unit_NRestarts=${RESTARTS:-unavailable}"
# Actual process
PID=$(systemctl show kubelet \
  -p MainPID --value 2>/dev/null) || PID=""
echo "kubelet_MainPID=${PID:-unavailable}"
if [ -z "$PID" ] || [ "$PID" = "0" ]; then
  echo "kubelet_process: not running (MainPID=0 or empty)"
  exit 0
fi
echo "kubelet_pid=$PID"
# Process limits (Max open files line only)
if [ -r "/proc/$PID/limits" ]; then
  grep "^Max open files" "/proc/$PID/limits" 2>/dev/null \
    || echo "proc_limits: Max open files line absent"
else
  echo "proc_limits: /proc/$PID/limits unreadable"
fi
# FD count: single captured ls — never re-read for counting.
# If ls fails the variable is empty AND the exit status is
# nonzero, so the else branch fires.  An empty directory
# (PID with 0 FDs) gives FDS="" with exit 0 → fd_count=0.
FD_DIR="/proc/$PID/fd"
if FDS=$(ls "$FD_DIR" 2>/dev/null); then
  if [ -z "$FDS" ]; then
    echo "fd_count=0 (approximate, includes inherited)"
  else
    CNT=$(printf '%s\n' "$FDS" | wc -l)
    echo "fd_count=$CNT (approximate, includes inherited)"
  fi
else
  echo "fd_count=unavailable: $FD_DIR unreadable or process exited"
fi
`

// inspectFormat is the Go template passed to "docker inspect --format".
// It extracts only the container ID and the Kind cluster label — never
// Env, full JSON, or secrets.  Built from kindClusterLabelKey so the
// label key is defined in exactly one place.
var inspectFormat = `{{.Id}}` + "\t" +
	`{{index .Config.Labels "` + kindClusterLabelKey + `"}}`

// testRunID uniquely identifies this test run so that resource cleanup is
// scoped to the resources created by this specific run.
var testRunID = generateRunID()

// workloadHostKubeconfig is the path to a kubeconfig file that can be used from
// the test host (127.0.0.1:<nodePort>) to reach the workload cluster.
var workloadHostKubeconfig string

// workloadClusterCreated tracks whether "kind create cluster" succeeded
// for the workload cluster.  Used by AfterSuite cleanup and diagnostic
// capture to avoid operating on a cluster that was never created.
var workloadClusterCreated bool

// diagnosticArtifactPath is the directory where bootstrap diagnostics
// are written.  Resolved once in BeforeSuite from MULTICLUSTER_ARTIFACT_DIR
// or a temp directory.
var diagnosticArtifactPath string

// originalDeploymentName is the operator Deployment name saved during patch so
// that restoreOperatorDeployment can address it without re-querying.
var originalDeploymentName string

// originalDeploymentRevision is the Deployment's revision annotation before
// the suite's patch, so restoreOperatorDeployment targets the exact revision.
var originalDeploymentRevision string

// ownedContainerID is the Docker container ID of the workload Kind
// node created by this test run's "kind create cluster" call.
// Set ONLY by resolveContainerOwnership, which compares before-create
// and after-create container state.  A pre-existing container that
// survived the initial "kind delete" is never treated as owned.
var ownedContainerID string

// preClusterHostSnapshot stores host-level inotify and FD resource
// readings taken before cluster creation.  Written to the diagnostic
// artifact on failure so pre-cluster state is preserved alongside
// failure-time readings without requiring a separate artifact file.
var preClusterHostSnapshot string

// preCreateContainerID records the container ID (if any) that exists
// BEFORE "kind create cluster" runs.  resolveContainerOwnership uses
// this to distinguish a genuinely new container from a pre-existing
// one whose "kind delete" was silently ignored.
var preCreateContainerID string

// preCreateInspectOK records whether the before-create docker inspect
// succeeded (true = confirmed state) or failed (false = unknown state).
// When false, resolveContainerOwnership fails closed.
var preCreateInspectOK bool

// operatorPatchSucceeded is set after the Deployment patch completes so
// AfterSuite knows whether cleanup should restore/remove resources.
var operatorPatchSucceeded bool

// operatorDeploymentChanged is set when the patch created a new rollout
// revision. When false, rollback is skipped and the Secret/workload cluster
// are preserved.
var operatorDeploymentChanged bool

// fulfillmentIssuerURL is the Keycloak issuer URL read from the
// osac-fulfillment-config ConfigMap during BeforeSuite. Used by
// runFulfillmentGRPC to acquire short-lived tokens without ever
// exposing them in pod logs or Go test memory.
var fulfillmentIssuerURL string

var _ = BeforeSuite(func() {
	By("resolving diagnostic artifact directory")
	diagnosticArtifactPath = resolveDiagnosticArtifactDir()

	By("deleting any stale workload cluster from a previous run")
	_, _ = runShell("kind", "delete", "cluster", "--name", workloadClusterName)

	By("capturing before-workload host resource limits")
	preClusterHostSnapshot = captureHostResourceSnapshot()
	_, _ = fmt.Fprintf(GinkgoWriter,
		"before-workload host snapshot:\n%s",
		preClusterHostSnapshot)

	// Snapshot container state BEFORE creation so
	// resolveContainerOwnership can distinguish a genuinely new
	// container from a pre-existing one.
	By("snapshotting pre-create container state")
	preResult := inspectContainerReal(
		workloadClusterName + "-control-plane")
	preCreateContainerID = preResult.id
	preCreateInspectOK = preResult.ok

	// kind create cluster changes the current kubectl context to the new
	// cluster. Capture the hub context now so we can restore it after.
	By("capturing current kubectl context (hub cluster)")
	hubContextOut, err := runShell("kubectl", "config", "current-context")
	Expect(err).NotTo(HaveOccurred(), "capture hub context")
	hubContext := strings.TrimSpace(string(hubContextOut))

	By("creating workload Kind cluster (--retain for diagnostics)")
	output, createErr := runShell("kind", "create", "cluster",
		"--name", workloadClusterName,
		"--retain",
		"--wait", "5m")
	// Resolve ownership by comparing before/after container state.
	ownedContainerID = resolveContainerOwnership(
		workloadClusterName + "-control-plane")
	if createErr != nil {
		// Capture bootstrap diagnostics from the retained node
		// BEFORE failing the assertion.  Best-effort: diagnostic
		// failures must not mask the original Kind create error.
		captureBootstrapDiagnostics(diagnosticArtifactPath)
		// Clean up the retained (failed) cluster so it does not
		// leak.  The node data was already captured above.
		cleanupRetainedWorkloadCluster()
	}
	Expect(createErr).NotTo(HaveOccurred(),
		"create workload cluster: %s", output)
	workloadClusterCreated = true

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
	// after the auto-provisioned Subnet CR.  With networking enabled
	// (operator.controllers.networking=true, networkingProvisioning=true),
	// the subnet controller dispatches cudn_net.create_subnet via AAP,
	// which creates both the namespace and ClusterUserDefinedNetwork
	// on the workload cluster automatically.  The test asserts that
	// AAP creates these resources before ComputeInstance provisioning.

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

	By("discovering fulfillment issuer URL from ConfigMap")
	fulfillmentIssuerURL = discoverFulfillmentIssuerURL(operatorNamespace)

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

		if workloadClusterCreated {
			By("deleting workload Kind cluster")
			_, _ = runShell("kind", "delete", "cluster",
				"--name", workloadClusterName)
		}
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
		{
			"osac-create-subnet",
			"osac-aap/playbook_osac_create_subnet.yml",
		},
		{
			"osac-delete-subnet",
			"osac-aap/playbook_osac_delete_subnet.yml",
		},
		{
			"osac-create-virtual-network",
			"osac-aap/playbook_osac_create_virtual_network.yml",
		},
		{
			"osac-delete-virtual-network",
			"osac-aap/playbook_osac_delete_virtual_network.yml",
		},
		{
			"osac-create-security-group",
			"osac-aap/playbook_osac_create_security_group.yml",
		},
		{
			"osac-delete-security-group",
			"osac-aap/playbook_osac_delete_security_group.yml",
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
		"osac-create-subnet",
		"osac-delete-subnet",
		"osac-create-virtual-network",
		"osac-delete-virtual-network",
		"osac-create-security-group",
		"osac-delete-security-group",
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

// discoverFulfillmentIssuerURL reads OSAC_FULFILLMENT_ISSUER_URL from the
// osac-fulfillment-config ConfigMap in the given namespace.  The ConfigMap
// is created by the installer chart
// (osac-installer/charts/osac/templates/fulfillment-runtime-config.yaml).
// Called once in BeforeSuite; the result is stored in the package-level
// fulfillmentIssuerURL variable for use by runFulfillmentGRPC.
func discoverFulfillmentIssuerURL(namespace string) string {
	out, err := runKubectlHub("get", "configmap",
		"osac-fulfillment-config", "-n", namespace,
		"-o", `jsonpath={.data.OSAC_FULFILLMENT_ISSUER_URL}`)
	Expect(err).NotTo(HaveOccurred(),
		"read OSAC_FULFILLMENT_ISSUER_URL from "+
			"osac-fulfillment-config ConfigMap")
	url := strings.TrimSpace(string(out))
	Expect(url).NotTo(BeEmpty(),
		"OSAC_FULFILLMENT_ISSUER_URL in "+
			"osac-fulfillment-config must not be empty")
	_, _ = fmt.Fprintf(GinkgoWriter,
		"discovered fulfillment issuer URL: %s\n", url)
	return url
}

// buildGRPCPod constructs a typed corev1.Pod for a one-shot grpcurl
// invocation.  The Pod uses:
//   - command: ["grpcurl"] (no shell — distroless image)
//   - args: structured list (no YAML template / fmt.Sprintf injection)
//   - token from a pre-created Secret via env secretKeyRef
//   - grpcurl -expand-headers expands ${FULFILLMENT_TOKEN} in -H
//   - automountServiceAccountToken: false (no SA needed)
//   - CA bundle from ConfigMap volume
//
// This matches the proven devstack provision-tenant.sh pattern
// (osac-installer/charts/osac-devstack/files/provision-tenant.sh
// lines 108-136): token acquired host-side, stored in a Secret,
// grpcurl Pod receives it via env secretKeyRef.
//
// The returned Pod is safe to serialize to JSON for kubectl apply;
// no string interpolation of user-supplied payloads into YAML.
func buildGRPCPod(
	podName, namespace, tokenSecretName,
	grpcMethod, grpcPayload, grpcTarget string,
) *corev1.Pod {
	automount := false
	return &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Pod",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: namespace,
		},
		Spec: corev1.PodSpec{
			RestartPolicy:                corev1.RestartPolicyNever,
			AutomountServiceAccountToken: &automount,
			Containers: []corev1.Container{{
				Name:    "grpcurl",
				Image:   "docker.io/fullstorydev/grpcurl:latest",
				Command: []string{"grpcurl"},
				Args: []string{
					"-expand-headers",
					"-cacert", "/etc/ca-bundle/bundle.pem",
					"-H",
					"authorization: Bearer ${FULFILLMENT_TOKEN}",
					"-d", grpcPayload,
					grpcTarget,
					grpcMethod,
				},
				Env: []corev1.EnvVar{{
					Name: "FULFILLMENT_TOKEN",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: tokenSecretName,
							},
							Key: "token",
						},
					},
				}},
				VolumeMounts: []corev1.VolumeMount{{
					Name:      "ca-bundle",
					MountPath: "/etc/ca-bundle",
					ReadOnly:  true,
				}},
			}},
			Volumes: []corev1.Volume{{
				Name: "ca-bundle",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: "ca-bundle",
						},
						Items: []corev1.KeyToPath{{
							Key:  "bundle.pem",
							Path: "bundle.pem",
						}},
					},
				},
			}},
		},
	}
}

// buildTokenScript returns the shell script for the token-acquisition
// Pod.  The script reads the client secret from a mounted file
// (matching fulfillment-auth.sh's client_secret@file pattern at
// line 21) so the secret never appears in argv, logs, or process
// listings.
//
// The returned script is safe to log — it contains only the
// non-secret client_id and the token URL.
// tokenFilePath is the in-Pod path where the token script writes
// the access token.  Must match the readTokenFromPod exec path.
const tokenFilePath = "/tmp/token"

func buildTokenScript(
	clientID, tokenURL string,
) string {
	// Matches fulfillment-auth.sh:
	// - client_secret@file (line 21)
	// - python3 json.load (lines 28-41)
	// - --cacert for TLS (line 17)
	// - Generic error messages without secrets (lines 23, 42)
	//
	// Token is written to tokenFilePath (mode 0600) — NEVER to
	// stdout/stderr.  The container then sleeps so the caller
	// can retrieve the file via kubectl exec.
	return fmt.Sprintf(
		`set -e; `+
			`umask 077; `+
			`RESP=$(curl --silent --show-error --fail `+
			`--cacert /etc/ca-bundle/bundle.pem `+
			`--connect-timeout 10 --max-time 30 `+
			`--request POST `+
			`--data-urlencode 'grant_type=client_credentials' `+
			`--data-urlencode 'client_id=%s' `+
			`--data-urlencode 'client_secret@/var/run/secrets/fc-cred/client-secret' `+
			`'%s') || `+
			`{ echo "FAIL: token request failed" >&2; exit 1; }; `+
			`printf '%%s' "$RESP" | python3 -c '`+
			`import json,sys; `+
			`t=json.load(sys.stdin).get("access_token",""); `+
			`sys.exit(1) if not t else `+
			`open("%s","w").write(t)' || `+
			`{ echo "FAIL: invalid token response" >&2; exit 1; }; `+
			`exec sleep 300`,
		clientID, tokenURL, tokenFilePath)
}

// buildTokenPod constructs a typed corev1.Pod for token acquisition.
// Matches the proven provision-tenant.sh Helm hook pattern:
//   - ca-bundle ConfigMap mounted at /etc/ca-bundle
//   - fulfillment-controller-credentials Secret at /var/run/secrets/fc-cred
//   - Image: quay.io/openshift/origin-cli (bash, curl, python3)
//   - automountServiceAccountToken: false
//
// The script writes the token to tokenFilePath (mode 0600), never to
// stdout/stderr, then sleeps.  A readiness probe checks the file
// exists so the caller knows when to retrieve it via kubectl exec.
// No secrets appear in the Pod spec, args, env values, or logs.
func buildTokenPod(
	podName, namespace, tokenSecretCredName string,
	script string,
) *corev1.Pod {
	automount := false
	mode := int32(0440)
	return &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Pod",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: namespace,
		},
		Spec: corev1.PodSpec{
			RestartPolicy:                corev1.RestartPolicyNever,
			AutomountServiceAccountToken: &automount,
			Containers: []corev1.Container{{
				Name:    "get-token",
				Image:   "quay.io/openshift/origin-cli:4.20.0",
				Command: []string{"bash", "-c"},
				Args:    []string{script},
				ReadinessProbe: &corev1.Probe{
					ProbeHandler: corev1.ProbeHandler{
						Exec: &corev1.ExecAction{
							Command: []string{
								"test", "-s",
								tokenFilePath,
							},
						},
					},
					PeriodSeconds:    2,
					FailureThreshold: 60,
				},
				VolumeMounts: []corev1.VolumeMount{
					{
						Name:      "ca-bundle",
						MountPath: "/etc/ca-bundle",
						ReadOnly:  true,
					},
					{
						Name:      "fc-cred",
						MountPath: "/var/run/secrets/fc-cred",
						ReadOnly:  true,
					},
				},
			}},
			Volumes: []corev1.Volume{
				{
					Name: "ca-bundle",
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "ca-bundle",
							},
							Items: []corev1.KeyToPath{{
								Key:  "bundle.pem",
								Path: "bundle.pem",
							}},
						},
					},
				},
				{
					Name: "fc-cred",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName:  tokenSecretCredName,
							DefaultMode: &mode,
						},
					},
				},
			},
		},
	}
}

// readTokenFromPod retrieves the token file from a running Pod via
// kubectl exec.  Uses exec.CommandContext directly — NOT utils.Run
// or runKubectlHub — so neither the command output nor its content
// is logged.  Returns a generic error on failure without exposing
// any partial token or response content.
func readTokenFromPod(
	podName, namespace string,
) (string, error) {
	ctx, cancel := context.WithTimeout(
		context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kubectl",
		"exec", podName, "-n", namespace,
		"-c", "get-token",
		"--", "cat", tokenFilePath)
	dir, _ := utils.GetProjectDir()
	cmd.Dir = dir

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf(
			"failed to read token from pod %s "+
				"(details withheld)", podName)
	}
	token := strings.TrimSpace(string(out))
	if token == "" {
		return "", fmt.Errorf(
			"token file in pod %s was empty", podName)
	}
	return token, nil
}

// acquireFulfillmentToken obtains a short-lived Keycloak access token
// by creating a transient Pod that mounts the ca-bundle ConfigMap and
// the fulfillment-controller-credentials Secret.  The script writes
// the token to a file inside the Pod (mode 0600, never stdout/stderr),
// then sleeps.  The caller reads the file via kubectl exec (not logs),
// using readTokenFromPod which does not log its output.
//
// The token IS held briefly in Go memory (returned to caller for
// Secret creation); it is never logged, never in Pod logs, and never
// in any logged command args.
func acquireFulfillmentToken(namespace string) string {
	tokenURL := strings.TrimRight(fulfillmentIssuerURL, "/") +
		"/protocol/openid-connect/token"

	clientIDOut, err := runKubectlHub("get", "secret",
		"fulfillment-controller-credentials",
		"-n", namespace,
		"-o", "jsonpath={.data.client-id}")
	Expect(err).NotTo(HaveOccurred(),
		"read fulfillment client-id")
	clientIDB64 := strings.TrimSpace(string(clientIDOut))
	Expect(clientIDB64).NotTo(BeEmpty())
	clientIDBytes, err := base64.StdEncoding.DecodeString(
		clientIDB64)
	Expect(err).NotTo(HaveOccurred())

	podName := fmt.Sprintf("mc-test-token-%s", testRunID)

	// Register cleanup BEFORE assertions.
	defer func() {
		_, _ = runKubectlHub("delete", "pod", podName,
			"-n", namespace,
			"--ignore-not-found", "--wait=false")
	}()
	_, _ = runKubectlHub("delete", "pod", podName,
		"-n", namespace, "--ignore-not-found")

	script := buildTokenScript(
		string(clientIDBytes), tokenURL)

	pod := buildTokenPod(podName, namespace,
		"fulfillment-controller-credentials", script)

	podJSON, err := json.Marshal(pod)
	Expect(err).NotTo(HaveOccurred(), "marshal token Pod")

	_, err = applyYAML(string(podJSON), runKubectlHub)
	Expect(err).NotTo(HaveOccurred(),
		"apply token-acquisition pod")

	// Wait for the container to become Ready (token file
	// exists and is non-empty, per the readiness probe).
	Eventually(func() string {
		out, pErr := runKubectlHub("get", "pod", podName,
			"-n", namespace,
			"-o",
			`jsonpath={.status.containerStatuses[0].ready}`)
		if pErr != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}, 2*time.Minute, 2*time.Second).Should(Equal("true"),
		"token pod %s container should become ready "+
			"(token file written)", podName)

	// Read token via kubectl exec (not logs).
	token, err := readTokenFromPod(podName, namespace)
	Expect(err).NotTo(HaveOccurred(),
		"read token from pod")
	return token
}

// runFulfillmentGRPC creates a one-shot Pod that calls the
// fulfillment private gRPC API.  Follows the proven devstack
// provision-tenant.sh pattern (lines 89-139):
//
//  1. Acquire token HOST-SIDE via acquireFulfillmentToken
//     (kubectl exec into awx-task pod which has curl+python3).
//  2. Store token in a temporary Secret via kubectl.
//  3. Create a typed corev1.Pod (via buildGRPCPod) serialized
//     to JSON — no YAML fmt.Sprintf template injection.
//  4. The Pod runs grpcurl directly (distroless image, no shell)
//     with token from env secretKeyRef + -expand-headers.
//
// The token never appears in: pod spec, pod logs, Go test
// memory beyond the acquireFulfillmentToken call, or assertion
// messages.
//
// Returns the pod log output (gRPC response) and the pod phase.
func runFulfillmentGRPC(
	namespace, podBaseName, grpcMethod, grpcPayload string,
) (output []byte, phase string) {
	podName := fmt.Sprintf("%s-%s", podBaseName, testRunID)
	tokenSecretName := fmt.Sprintf("%s-token-%s",
		podBaseName, testRunID)

	// Register cleanup BEFORE any assertions so early failures
	// still clean up the pod and secret.
	defer func() {
		_, _ = runKubectlHub("delete", "pod", podName,
			"-n", namespace, "--ignore-not-found")
		_, _ = runKubectlHub("delete", "secret",
			tokenSecretName, "-n", namespace,
			"--ignore-not-found")
	}()

	// Clean up any leftover pod/secret from a previous attempt.
	_, _ = runKubectlHub("delete", "pod", podName,
		"-n", namespace, "--ignore-not-found")
	_, _ = runKubectlHub("delete", "secret", tokenSecretName,
		"-n", namespace, "--ignore-not-found")

	// 1. Acquire token host-side (matching provision-tenant.sh).
	token := acquireFulfillmentToken(namespace)

	// 2. Store token in a temporary Secret (matching
	//    provision-tenant.sh lines 95-98).  Use dry-run+apply
	//    for idempotency.
	tmpTokenFile, err := os.CreateTemp("",
		"mc-test-token-*")
	Expect(err).NotTo(HaveOccurred())
	defer os.Remove(tmpTokenFile.Name())
	Expect(os.WriteFile(tmpTokenFile.Name(),
		[]byte(token), 0600)).To(Succeed())
	_ = tmpTokenFile.Close()
	_, err = runKubectlHub("create", "secret", "generic",
		tokenSecretName,
		"--from-file=token="+tmpTokenFile.Name(),
		"-n", namespace)
	Expect(err).NotTo(HaveOccurred(),
		"create token Secret %s", tokenSecretName)

	// 3. Build typed Pod (safe JSON serialization, no YAML
	//    template injection).
	grpcTarget := fmt.Sprintf(
		"fulfillment-internal-api.%s.svc.cluster.local:8001",
		namespace)
	pod := buildGRPCPod(podName, namespace,
		tokenSecretName, grpcMethod, grpcPayload, grpcTarget)

	podJSON, err := json.Marshal(pod)
	Expect(err).NotTo(HaveOccurred(), "marshal Pod to JSON")

	_, err = applyYAML(string(podJSON), runKubectlHub)
	Expect(err).NotTo(HaveOccurred(),
		"apply grpcurl pod %s", podName)

	// 4. Wait for the pod to complete.
	Eventually(func() string {
		out, pErr := runKubectlHub("get", "pod", podName,
			"-n", namespace,
			"-o", "jsonpath={.status.phase}")
		if pErr != nil {
			return ""
		}
		phase = strings.TrimSpace(string(out))
		return phase
	}, 2*time.Minute, 2*time.Second).Should(
		BeElementOf("Succeeded", "Failed"),
		"grpcurl pod %s should complete", podName)

	// Read main container logs (grpcurl output only — token is
	// never printed).
	logOut, _ := runKubectlHub("logs", podName,
		"-c", "grpcurl", "-n", namespace)
	output = logOut

	return output, phase
}

// ---------------------------------------------------------------------------
// Workload-cluster bootstrap diagnostics
// ---------------------------------------------------------------------------

// maxCapturedBytes caps the raw stdout+stderr collected from each
// diagnostic command.  500 lines of journal at ~200 bytes/line is
// ~100 KB; 512 KB gives headroom for longer systemctl output.
// The boundedWriter enforces this cap during capture so memory
// use is bounded while the command runs, not post-hoc.
const maxCapturedBytes = 512 * 1024

// boundedWriter is a concurrency-safe io.Writer that stores at
// most maxBytes bytes in an internal buffer.  Writes beyond the
// cap are consumed (reported as written so the child process does
// not block on a full pipe) but discarded.  Truncation is tracked.
// Both cmd.Stdout and cmd.Stderr can point to the same instance.
type boundedWriter struct {
	mu        sync.Mutex
	buf       []byte
	maxBytes  int
	truncated bool
}

func newBoundedWriter(maxBytes int) *boundedWriter {
	initCap := 64 * 1024
	if maxBytes < initCap {
		initCap = maxBytes
	}
	return &boundedWriter{
		buf:      make([]byte, 0, initCap),
		maxBytes: maxBytes,
	}
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	remaining := w.maxBytes - len(w.buf)
	if remaining > 0 {
		take := remaining
		if len(p) < take {
			take = len(p)
		}
		w.buf = append(w.buf, p[:take]...)
		if take < len(p) {
			w.truncated = true
		}
	} else if len(p) > 0 {
		w.truncated = true
	}
	// Report full write so the child process never stalls.
	return len(p), nil
}

// Bytes returns the captured content.  Safe to call after the
// command has exited; concurrent use with Write is safe but
// the returned slice must not be mutated.
func (w *boundedWriter) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]byte, len(w.buf))
	copy(out, w.buf)
	return out
}

// Truncated returns true if any write exceeded the cap.
func (w *boundedWriter) Truncated() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.truncated
}

// resolveDiagnosticArtifactDir returns the directory where bootstrap
// diagnostics should be written.  Uses MULTICLUSTER_ARTIFACT_DIR if
// set (CI wires this to the upload path); otherwise creates a
// private temp directory.  Never falls back to os.TempDir (shared,
// may contain unrelated files that leak into uploads); returns ""
// on failure so the caller can skip safely.
func resolveDiagnosticArtifactDir() string {
	if d := os.Getenv("MULTICLUSTER_ARTIFACT_DIR"); d != "" {
		if err := os.MkdirAll(d, 0o755); err != nil {
			_, _ = fmt.Fprintf(GinkgoWriter,
				"WARNING: cannot create artifact dir %s: "+
					"%v; diagnostics will be skipped\n",
				d, err)
			return ""
		}
		return d
	}
	d, err := os.MkdirTemp("", defaultArtifactSubdir+"-*")
	if err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter,
			"WARNING: cannot create temp artifact dir: "+
				"%v; diagnostics will be skipped\n", err)
		return ""
	}
	_, _ = fmt.Fprintf(GinkgoWriter,
		"diagnostic artifact dir (temp): %s\n", d)
	return d
}

// captureHostResourceSnapshot reads host-level inotify and FD
// resource limits, plus an aggregated count of inotify descriptor
// references by numeric UID across /proc.  All values are numeric-
// only reads; no process environment, command lines, arbitrary FD
// targets, or credentials are read.  FD symlink targets are read
// SOLELY to classify anon_inode:inotify and are never printed.
//
// Readings are best-effort: unavailable values are labeled as such
// rather than fabricated.  Inotify descriptor references may
// include inherited FDs and are NOT unique inotify instances.
// Processes may exit mid-scan (racing); counts reflect a bounded
// snapshot, not an atomic view.
func captureHostResourceSnapshot() string {
	var sb strings.Builder

	// Sysctl limits (numeric, read-only).
	sysctls := []struct {
		label string
		path  string
	}{
		{"fs.inotify.max_user_instances",
			"/proc/sys/fs/inotify/max_user_instances"},
		{"fs.inotify.max_user_watches",
			"/proc/sys/fs/inotify/max_user_watches"},
	}
	for _, sc := range sysctls {
		data, err := os.ReadFile(sc.path)
		if err != nil {
			fmt.Fprintf(&sb, "%s: unavailable (%s)\n",
				sc.label,
				sanitizeErrorMessage(err))
			continue
		}
		val := strings.TrimSpace(string(data))
		// Security: validate numeric-only content.
		if isNumericValue(val) {
			fmt.Fprintf(&sb, "%s: %s\n", sc.label, val)
		} else {
			fmt.Fprintf(&sb,
				"%s: non-numeric content redacted\n",
				sc.label)
		}
	}

	// Aggregate inotify FD references by numeric UID across
	// processes VISIBLE to this unprivileged user.  On GitHub
	// Hosted runners, root-owned processes (kubelet, containerd)
	// are typically unreadable without sudo; their FDs appear
	// in the "unreadable" count, not in per-UID totals.  The
	// scan is a partial visibility snapshot, not a complete
	// system view.  Read-only: reads /proc/<pid>/fd symlinks
	// solely to classify anon_inode:inotify; never prints
	// targets/paths/env.
	inotifyReport := aggregateHostInotifyRefs()
	fmt.Fprintf(&sb, "%s", inotifyReport)

	return sb.String()
}

// aggregateHostInotifyRefs scans /proc for numeric-PID
// directories, reads each process's /proc/<pid>/fd/ symlink
// targets to count those pointing to "anon_inode:inotify",
// and aggregates counts by the numeric UID from
// /proc/<pid>/status.  Bounded by inotifyScanDeadline both
// at the process level and inside each FD directory.
//
// Visibility: runs unprivileged.  Root-owned processes
// (kubelet, containerd) are typically unreadable without
// sudo; they appear in the "unreadable" count.  Readlink
// failures on individual FDs (closed mid-scan or permission
// denied) are counted as "readlink_unavailable" rather than
// silently counted as zero.  The output is a PARTIAL
// VISIBILITY snapshot, NOT a complete system inventory.
//
// Output: per-UID descriptor-ref counts (NOT unique inotify
// instances — a forked child inherits refs), total visible
// refs, scanned/unreadable/racing/readlink_unavailable
// counts.  No paths, environment, or command lines.
func aggregateHostInotifyRefs() string {
	var sb strings.Builder
	deadline := time.Now().Add(inotifyScanDeadline)

	entries, err := os.ReadDir("/proc")
	if err != nil {
		fmt.Fprintf(&sb,
			"inotify_scan: unavailable (%s)\n",
			sanitizeErrorMessage(err))
		return sb.String()
	}

	type uidCount struct {
		refs int
	}
	byUID := make(map[string]*uidCount)
	var scanned, unreadable, racing, rlUnavail int

	for _, e := range entries {
		if time.Now().After(deadline) {
			fmt.Fprintf(&sb,
				"inotify_scan: deadline reached "+
					"after %d processes\n", scanned)
			break
		}
		name := e.Name()
		if !isNumericValue(name) {
			continue
		}
		pid := name

		uid := readProcUID(pid)
		if uid == "" {
			racing++
			continue
		}

		fdDir := "/proc/" + pid + "/fd"
		fdEntries, err := os.ReadDir(fdDir)
		if err != nil {
			unreadable++
			continue
		}
		scanned++

		refs := 0
		for _, fd := range fdEntries {
			if time.Now().After(deadline) {
				break
			}
			target, err := os.Readlink(
				fdDir + "/" + fd.Name())
			if err != nil {
				rlUnavail++
				continue
			}
			if target == "anon_inode:inotify" {
				refs++
			}
		}
		if refs > 0 {
			uc, ok := byUID[uid]
			if !ok {
				uc = &uidCount{}
				byUID[uid] = uc
			}
			uc.refs += refs
		}
	}

	totalRefs := 0
	for uid, uc := range byUID {
		fmt.Fprintf(&sb,
			"inotify_fd_refs_uid_%s: %d "+
				"(visible descriptor refs, "+
				"not unique instances)\n",
			uid, uc.refs)
		totalRefs += uc.refs
	}
	fmt.Fprintf(&sb,
		"inotify_fd_refs_total: %d "+
			"(visible descriptor refs, "+
			"not unique instances)\n",
		totalRefs)
	fmt.Fprintf(&sb,
		"inotify_scan: partial visibility snapshot "+
			"scanned=%d unreadable=%d racing=%d "+
			"readlink_unavailable=%d\n",
		scanned, unreadable, racing, rlUnavail)

	return sb.String()
}

// inotifyScanDeadline bounds the /proc scan so it cannot block
// the test indefinitely on a host with many processes.
const inotifyScanDeadline = 10 * time.Second

// readProcUID extracts the numeric real UID from
// /proc/<pid>/status.  Returns "" on any error.
func readProcUID(pid string) string {
	data, err := os.ReadFile("/proc/" + pid + "/status")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "Uid:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && isNumericValue(fields[1]) {
			return fields[1]
		}
	}
	return ""
}

// isNumericValue returns true if s consists only of ASCII digits.
// Used to validate sysctl readings before including them in
// diagnostic output.
func isNumericValue(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// captureBootstrapDiagnostics collects kubelet, container-runtime,
// and systemd bootstrap logs from the retained workload Kind node.
// Every operation is best-effort with bounded timeouts — a failure
// here must never mask the original Kind create error.
//
// Security: no kubeconfigs, Secrets, certificates, private keys,
// or bearer tokens are collected.  All output is sanitized as a
// whole blob (handling multiline PEM blocks) through sanitizeOutput
// before being written to the artifact file.
func captureBootstrapDiagnostics(artifactDir string) {
	if artifactDir == "" {
		_, _ = fmt.Fprintf(GinkgoWriter,
			"diagnostics: no artifact dir; skipping\n")
		return
	}

	containerName := workloadClusterName + "-control-plane"

	// Verify container ownership: the container must exist AND
	// its ID must match the one captured immediately after our
	// "kind create cluster" call.  This prevents operating on a
	// pre-existing or replacement container with the same name.
	if ownedContainerID == "" {
		_, _ = fmt.Fprintf(GinkgoWriter,
			"diagnostics: no owned container ID; "+
				"skipping capture (fail-closed)\n")
		return
	}
	currentID := getDockerContainerID(containerName)
	if currentID != ownedContainerID {
		_, _ = fmt.Fprintf(GinkgoWriter,
			"diagnostics: container %s ID mismatch "+
				"(owned=%s, current=%s); skipping "+
				"capture (fail-closed)\n",
			containerName, ownedContainerID, currentID)
		return
	}

	// Use the immutable ownedContainerID for docker exec targets,
	// not the container name (which could be reassigned to a
	// replacement container between ownership check and exec).
	execTarget := ownedContainerID

	_, _ = fmt.Fprintf(GinkgoWriter,
		"capturing bootstrap diagnostics from %s (id=%s)\n",
		containerName, execTarget)

	outPath := filepath.Join(artifactDir,
		"workload-bootstrap-diagnostics.txt")
	f, err := os.OpenFile(outPath,
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter,
			"WARNING: cannot create diagnostic file %s: "+
				"%v\n", outPath, err)
		return
	}
	defer f.Close()

	writeHeader(f, "Workload Kind node bootstrap diagnostics")
	writef(f, "container: %s (id=%s)\n", containerName, execTarget)
	writef(f, "capture_time: %s\n\n",
		time.Now().UTC().Format(time.RFC3339))

	// Include before-workload host resource readings to
	// compare with at-failure state.  The snapshot was taken
	// after the hub Kind cluster was running but before the
	// workload "kind create cluster" call.  Writing it here
	// preserves it since this file is created at failure time.
	if preClusterHostSnapshot != "" {
		writeHeader(f,
			"host resource limits (before-workload)")
		writef(f, "%s\n", preClusterHostSnapshot)
	}

	writeHeader(f, "host resource limits (at-failure)")
	writef(f, "%s\n", captureHostResourceSnapshot())

	// Each diagnostic command is run in sequence with its own
	// timeout.  The allowlist is narrow: only kubelet, containerd,
	// and systemd bootstrap state.
	type diagCmd struct {
		label string
		args  []string
	}
	commands := []diagCmd{
		{
			"kubelet journal (last 500 lines)",
			[]string{"journalctl", "-u", "kubelet",
				"--no-pager", "-n", diagnosticJournalLines},
		},
		{
			"containerd journal (last 500 lines)",
			[]string{"journalctl", "-u", "containerd",
				"--no-pager", "-n", diagnosticJournalLines},
		},
		{
			"systemd failed units",
			[]string{"systemctl", "--failed", "--no-pager"},
		},
		{
			"kubelet process status",
			[]string{"systemctl", "status", "kubelet",
				"--no-pager", "-l"},
		},
		{
			"containerd process status",
			[]string{"systemctl", "status", "containerd",
				"--no-pager", "-l"},
		},
		// Kubelet process limits: configured systemd
		// properties and observable FD count.  Numeric
		// values only; no env/cmdline/FD targets.
		{
			"kubelet systemd properties " +
				"(LimitNOFILE, NRestarts, MainPID)",
			[]string{"systemctl", "show", "kubelet",
				"--property=LimitNOFILE," +
					"NRestarts,MainPID",
				"--no-pager"},
		},
		{
			"kubelet process limits and FD count",
			[]string{"sh", "-c",
				kubeletProcessScript},
		},
	}

	for _, dc := range commands {
		// Use ownedContainerID (immutable) as docker exec target.
		runDiagnosticCommand(f, execTarget, dc.label,
			dc.args)
	}

	_, _ = fmt.Fprintf(GinkgoWriter,
		"diagnostics written to %s\n", outPath)
}

// buildDockerExecArgs constructs the argument list for a
// "docker exec <container> <cmd...>" invocation.  This matches the
// proven pattern used elsewhere in this file (e.g. line 419/425):
// no "--" separator between container name and command, unlike
// kubectl exec which requires it.
func buildDockerExecArgs(
	containerName string, cmdArgs []string,
) []string {
	args := make([]string, 0, 2+len(cmdArgs))
	args = append(args, "exec", containerName)
	args = append(args, cmdArgs...)
	return args
}

// runDiagnosticCommand executes a single command inside the Kind
// node container, sanitizes the WHOLE output blob (handling
// multiline PEM blocks and truncated credentials), and writes the
// result to the artifact file.  Bounded by diagnosticTimeout.
//
// Memory is bounded during capture: stdout and stderr are
// directed to a boundedWriter that stores at most
// maxCapturedBytes, consuming (discarding) excess so the child
// process never blocks.  Failures are recorded in the artifact
// file and never propagated.
func runDiagnosticCommand(
	f *os.File,
	containerName, label string,
	cmdArgs []string,
) {
	writeHeader(f, label)
	start := time.Now()

	ctx, cancel := context.WithTimeout(
		context.Background(), diagnosticTimeout)
	defer cancel()

	args := buildDockerExecArgs(containerName, cmdArgs)
	cmd := exec.CommandContext(ctx, "docker", args...)

	// Use a boundedWriter for stdout+stderr so memory is
	// capped while the command runs, not post-hoc.
	bw := newBoundedWriter(maxCapturedBytes)
	cmd.Stdout = bw
	cmd.Stderr = bw
	err := cmd.Run()
	elapsed := time.Since(start)

	out := bw.Bytes()
	truncated := bw.Truncated()

	writef(f, "# command: docker exec %s %s\n",
		containerName, strings.Join(cmdArgs, " "))
	writef(f, "# elapsed: %s\n",
		elapsed.Round(time.Millisecond))

	if err != nil {
		// Do NOT log raw err.Error() — it may contain a
		// snippet of the command output.  Report only the
		// exit status.
		writef(f, "# exit_error: %s\n",
			sanitizeErrorMessage(err))
	}
	if truncated {
		writef(f, "# truncated: output exceeded %d bytes\n",
			maxCapturedBytes)
	}

	// Sanitize the WHOLE output blob (not per-line) so
	// multiline PEM blocks and JSON-embedded credentials
	// are caught before any disk write.
	sanitized := sanitizeOutput(string(out))
	writef(f, "%s\n", sanitized)
}

// sanitizeOutput redacts sensitive content from a complete
// output blob.  Unlike per-line sanitization, this handles
// multiline PEM blocks (BEGIN…END spanning multiple lines)
// and truncated PEM blocks (BEGIN present, END missing —
// redacts through end of string).
//
// Processing order:
//  1. Multiline PEM blocks (BEGIN…END pairs)
//  2. Truncated PEM blocks (BEGIN without END → redact to EOF)
//  3. Per-line patterns (bearer, token, password, etc.)
func sanitizeOutput(s string) string {
	// 1. Complete PEM blocks (multiline).
	s = multilinePEMPattern().ReplaceAllStringFunc(s,
		func(match string) string {
			// Keep the BEGIN marker for context.
			idx := strings.Index(match, "\n")
			if idx < 0 {
				return "[REDACTED PEM BLOCK]"
			}
			begin := match[:idx]
			return begin + "\n[REDACTED PEM CONTENT]"
		})

	// 2. Truncated PEM blocks (BEGIN without matching END).
	s = redactTruncatedPEMBlocks(s)

	// 3. Per-line sensitive patterns.
	var result strings.Builder
	result.Grow(len(s))
	for _, line := range strings.Split(s, "\n") {
		result.WriteString(sanitizeLine(line))
		result.WriteByte('\n')
	}
	return result.String()
}

// sanitizeLine redacts sensitive content from a single log line.
// Called by sanitizeOutput after multiline PEM handling.
func sanitizeLine(line string) string {
	lower := strings.ToLower(line)
	hasSensitive := false
	for _, kw := range sensitiveKeywords {
		if strings.Contains(lower, kw) {
			hasSensitive = true
			break
		}
	}
	if !hasSensitive {
		return line
	}

	for _, re := range linePatterns() {
		line = re.ReplaceAllString(line, "${1}[REDACTED]")
	}
	return line
}

// sensitiveKeywords are checked (lowercase) to short-circuit
// sanitizeLine for the vast majority of safe log lines.
var sensitiveKeywords = []string{
	"bearer", "token", "password", "secret",
	"certificate", "private", "kubeconfig",
	"client_secret", "client-secret", "client-key",
	"admin.conf", "authorization", "bootstrap-token",
}

// linePatterns returns per-line redaction regexes.
var linePatternCache []*regexp.Regexp

func linePatterns() []*regexp.Regexp {
	if linePatternCache != nil {
		return linePatternCache
	}
	patterns := []string{
		`(?i)(bearer\s+)\S+`,
		`(?i)(token[=:]\s*)\S+`,
		`(?i)(password[=:]\s*)\S+`,
		`(?i)(client[_-]secret[=:]\s*)\S+`,
		// JSON-quoted client_secret: "client_secret":"val".
		`(?i)(client[_-]secret"\s*:\s*")[^"]+`,
		`(?i)(certificate-authority-data:\s*)\S+`,
		`(?i)(client-certificate-data:\s*)\S+`,
		`(?i)(client-key-data:\s*)\S+`,
		`(?i)(authorization:\s*).+`,
		`(?i)(bootstrap-token[=:\s]+)\S+`,
	}
	linePatternCache = make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		linePatternCache = append(
			linePatternCache, regexp.MustCompile(p))
	}
	return linePatternCache
}

// multilinePEMPattern matches complete PEM blocks across lines.
var multilinePEMCache *regexp.Regexp

func multilinePEMPattern() *regexp.Regexp {
	if multilinePEMCache != nil {
		return multilinePEMCache
	}
	multilinePEMCache = regexp.MustCompile(
		`(?s)-----BEGIN [A-Z ]*(?:PRIVATE KEY|CERTIFICATE)-----` +
			`.+?` +
			`-----END [A-Z ]*(?:PRIVATE KEY|CERTIFICATE)-----`)
	return multilinePEMCache
}

// redactTruncatedPEMBlocks handles BEGIN markers left after
// multilinePEMPattern removed complete blocks.  A remaining
// BEGIN whose next line is NOT already "[REDACTED PEM CONTENT]"
// means the output was truncated mid-PEM.  Replaces everything
// from the BEGIN marker to end of string.
func redactTruncatedPEMBlocks(s string) string {
	const alreadyRedacted = "[REDACTED PEM CONTENT]"
	searchFrom := 0
	for {
		idx := strings.Index(s[searchFrom:], "-----BEGIN ")
		if idx < 0 {
			return s
		}
		idx += searchFrom

		// Find the end of the BEGIN line.
		nl := strings.Index(s[idx:], "\n")
		if nl < 0 {
			// BEGIN at end of string with no newline.
			return s[:idx] + "[REDACTED TRUNCATED PEM]"
		}
		// If the next line is already our redaction marker,
		// this was a complete PEM block handled in step 1.
		afterNL := s[idx+nl+1:]
		if strings.HasPrefix(afterNL, alreadyRedacted) {
			searchFrom = idx + nl + 1
			continue
		}
		beginLine := s[idx : idx+nl]
		return s[:idx] + beginLine + "\n" +
			"[REDACTED TRUNCATED PEM CONTENT]"
	}
}

// sanitizeErrorMessage extracts only the exit-status portion
// of an exec error, stripping any embedded command output that
// could contain secrets.
func sanitizeErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	// exec.ExitError.Error() format: "exit status N" — safe.
	// exec.CommandContext timeout: "signal: killed" — safe.
	// Anything longer may embed output.
	if strings.HasPrefix(msg, "exit status") ||
		strings.HasPrefix(msg, "signal:") {
		return msg
	}
	// Generic: report only that it failed.
	return "command failed (details redacted)"
}

// containerInspectResult holds the output of a container inspect
// call, distinguishing exists/absent/unknown.
type containerInspectResult struct {
	id    string // non-empty if container exists
	label string // value of kindClusterLabelKey label
	ok    bool   // true = confirmed state; false = unknown
}

// dockerInspectFn is the signature for container inspection.
// Production uses inspectContainerReal; tests inject fakes.
type dockerInspectFn func(name string) containerInspectResult

// inspectContainerReal performs a real docker inspect, returning
// the container ID and its Kind cluster label (kindClusterLabelKey).
//
// Absence classification: only the exact Docker "No such object:
// <name>" message (exit 1, stderr) confirms the container does
// not exist.  Socket errors ("no such file or directory"),
// permission errors, timeouts, and any other failure are UNKNOWN
// (ok=false) — the container may or may not exist.
//
// Bounded by diagnosticTimeout.  Only reads Id and one label
// from the inspect output — never Env, full JSON, or secrets.
func inspectContainerReal(name string) containerInspectResult {
	ctx, cancel := context.WithTimeout(
		context.Background(), diagnosticTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "inspect",
		"--format", inspectFormat,
		name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Classify the error: only a definitive "no such
		// object/container" response confirms absence.
		// Docker: "Error response from daemon: No such container: ..."
		// Podman: "Error: no such object: ..."
		// Any other error (socket missing, permission denied,
		// timeout, "no such file or directory") is UNKNOWN.
		lower := strings.ToLower(string(out))
		if strings.Contains(lower, "no such object:") ||
			strings.Contains(lower, "no such container:") {
			// Reject if the message ALSO mentions a file/socket
			// error — that's infrastructure, not container absence.
			if strings.Contains(lower, "no such file") ||
				strings.Contains(lower, "permission denied") ||
				strings.Contains(lower, "connection refused") {
				return containerInspectResult{ok: false}
			}
			return containerInspectResult{ok: true}
		}
		return containerInspectResult{ok: false}
	}
	parts := strings.SplitN(
		strings.TrimSpace(string(out)), "\t", 2)
	r := containerInspectResult{id: parts[0], ok: true}
	if len(parts) > 1 {
		r.label = parts[1]
	}
	return r
}

// getDockerContainerID returns the container ID or "" (for callers
// that don't need the full inspect result).
func getDockerContainerID(name string) string {
	return inspectContainerReal(name).id
}

// resolveContainerOwnership decides whether the container that
// currently exists was created by this test run.  It compares the
// before-create snapshot (preCreateContainerID, preCreateInspectOK)
// with the current state, and verifies the Kind cluster label.
//
// Uses inspectFn so tests can inject deterministic fakes.
// Returns the container ID only when ownership is positively
// confirmed; returns "" (fail-closed) otherwise.
//
// Ownership rules:
//   - Before-state unknown → "" (fail-closed)
//   - After-state unknown → "" (fail-closed)
//   - No container after create → "" (nothing to own)
//   - After-ID == before-ID → "" (pre-existing, not ours)
//   - Wrong Kind cluster label → "" (not our cluster)
//   - After-ID != before-ID, correct label → owned
func resolveContainerOwnership(
	containerName string,
) string {
	return resolveOwnership(
		containerName,
		preCreateContainerID,
		preCreateInspectOK,
		inspectContainerReal,
	)
}

// resolveOwnership is the pure decision logic extracted for
// testability.  Production calls it via resolveContainerOwnership;
// tests call it directly with a fake inspectFn.
func resolveOwnership(
	containerName string,
	beforeID string,
	beforeOK bool,
	inspectFn dockerInspectFn,
) string {
	if !beforeOK {
		_, _ = fmt.Fprintf(GinkgoWriter,
			"ownership: before-create inspect failed; "+
				"fail-closed (no ownership)\n")
		return ""
	}
	after := inspectFn(containerName)
	if !after.ok {
		_, _ = fmt.Fprintf(GinkgoWriter,
			"ownership: after-create inspect failed; "+
				"fail-closed (no ownership)\n")
		return ""
	}
	if after.id == "" {
		_, _ = fmt.Fprintf(GinkgoWriter,
			"ownership: no container after create; "+
				"nothing to own\n")
		return ""
	}
	if after.id == beforeID {
		_, _ = fmt.Fprintf(GinkgoWriter,
			"ownership: container %s unchanged "+
				"(id=%s); pre-existing, not owned\n",
			containerName, after.id)
		return ""
	}
	if after.label != workloadClusterName {
		_, _ = fmt.Fprintf(GinkgoWriter,
			"ownership: container %s has wrong Kind "+
				"label %q (want %q); not owned\n",
			containerName, after.label,
			workloadClusterName)
		return ""
	}
	_, _ = fmt.Fprintf(GinkgoWriter,
		"ownership: confirmed new container %s "+
			"(before=%q, after=%s, label=%s)\n",
		containerName, beforeID, after.id,
		after.label)
	return after.id
}

// cleanupRetainedWorkloadCluster deletes the retained workload
// Kind cluster after diagnostics have been captured.  Only the
// cluster whose container ID matches ownedContainerID is deleted
// — a pre-existing or replacement container is never touched.
// Best-effort: errors are logged with safe messages and never
// propagated.
func cleanupRetainedWorkloadCluster() {
	containerName := workloadClusterName + "-control-plane"
	if ownedContainerID == "" {
		_, _ = fmt.Fprintf(GinkgoWriter,
			"cleanup: no owned container ID; "+
				"skipping (fail-closed)\n")
		return
	}
	currentID := getDockerContainerID(containerName)
	if currentID != ownedContainerID {
		_, _ = fmt.Fprintf(GinkgoWriter,
			"cleanup: container %s ID mismatch "+
				"(owned=%s, current=%s); skipping "+
				"(fail-closed)\n",
			containerName, ownedContainerID, currentID)
		return
	}
	// Remove the retained node container by its immutable ID
	// (not by name, which could race with a replacement).
	// A failed single-node Kind bootstrap leaves only this one
	// container; the normal successful-run AfterSuite uses
	// "kind delete cluster" which is preserved unchanged.
	_, _ = fmt.Fprintf(GinkgoWriter,
		"cleaning up retained container %s "+
			"(cluster %s)\n",
		ownedContainerID, workloadClusterName)
	ctx, cancel := context.WithTimeout(
		context.Background(), diagnosticTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "rm", "-f",
		ownedContainerID)
	if out, err := cmd.CombinedOutput(); err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter,
			"WARNING: cleanup of retained container %s "+
				"failed: %s\n",
			ownedContainerID,
			sanitizeErrorMessage(err))
		_ = out
	}
}

// writeHeader writes a section separator to the diagnostic file.
func writeHeader(f *os.File, title string) {
	writef(f, "=== %s ===\n", title)
}

// writef is a best-effort fprintf to a file.
func writef(f *os.File, format string, args ...interface{}) {
	_, _ = fmt.Fprintf(f, format, args...)
}
