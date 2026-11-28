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

package contract

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"
)

// renderOperatorDeployment renders templates/deployment.yaml from the
// operator Helm chart with the given --set values.
func renderOperatorDeployment(t *testing.T, setValues ...string) ([]byte, error) {
	t.Helper()
	helmBin, err := exec.LookPath("helm")
	if err != nil {
		t.Fatalf("helm binary is required on PATH for Helm contract tests: %v", err)
	}
	chartDir := filepath.Join(repoRoot(), "bare-metal-fulfillment-operator", "charts", "operator")
	args := make([]string, 0, 5+2*len(setValues))
	args = append(args, "template", "bmf-operator", chartDir, "-s", "templates/deployment.yaml")
	for _, val := range setValues {
		args = append(args, "--set", val)
	}
	cmd := exec.Command(helmBin, args...) //nolint:gosec
	return cmd.CombinedOutput()
}

// parseRenderedDeployment unmarshals rendered YAML bytes into an
// appsv1.Deployment and validates its Kind.
func parseRenderedDeployment(t *testing.T, raw []byte) appsv1.Deployment {
	t.Helper()
	var deployment appsv1.Deployment
	if err := yaml.Unmarshal(raw, &deployment); err != nil {
		t.Fatalf("failed to unmarshal rendered Deployment YAML: %v\nraw:\n%s", err, string(raw))
	}
	if deployment.Kind != "Deployment" {
		t.Fatalf("rendered resource has kind %q, want Deployment", deployment.Kind)
	}
	return deployment
}

// findManagerContainer returns the manager container from deployment or
// fails the test if absent.
func findManagerContainer(t *testing.T, deployment appsv1.Deployment) corev1.Container {
	t.Helper()
	for _, c := range deployment.Spec.Template.Spec.Containers {
		if c.Name == "manager" {
			return c
		}
	}
	t.Fatalf("manager container not found in rendered Deployment")
	return corev1.Container{}
}

// findVolume returns a pointer to the named Pod volume in deployment, or nil
// if not found.
func findVolume(deployment appsv1.Deployment, name string) *corev1.Volume {
	for i := range deployment.Spec.Template.Spec.Volumes {
		if deployment.Spec.Template.Spec.Volumes[i].Name == name {
			return &deployment.Spec.Template.Spec.Volumes[i]
		}
	}
	return nil
}

// repoRoot returns the mono-repo root by walking up from this test file's
// location until it finds the go.work file that anchors the workspace.
func repoRoot() string {
	_, thisFile, _, _ := runtime.Caller(0) //nolint:dogsled
	dir := filepath.Dir(thisFile)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root without finding go.work.
			// Fall back to a relative path from this test file:
			// test/contract/ -> bare-metal-fulfillment-operator/ -> root
			_, f, _, _ := runtime.Caller(0) //nolint:dogsled
			return filepath.Join(filepath.Dir(f), "..", "..", "..")
		}
		dir = parent
	}
}

// defaultSecretKey is the remote-kubeconfig Secret's default item key, as
// the installer's REMOTE_KUBECONFIG flow stages it.
const defaultSecretKey = "kubeconfig"

// TestHelmDeploymentDefaultLocalModeOmitsRemoteKubeconfig verifies that
// default chart values omit remote kubeconfig env vars and volume mounts, so
// local mode renders byte-identical to before the option existed.
func TestHelmDeploymentDefaultLocalModeOmitsRemoteKubeconfig(t *testing.T) {
	out, err := renderOperatorDeployment(t)
	if err != nil {
		t.Fatalf("helm template failed in default local mode: %v\noutput:\n%s", err, string(out))
	}
	deployment := parseRenderedDeployment(t, out)
	manager := findManagerContainer(t, deployment)

	for _, env := range manager.Env {
		if env.Name == "OSAC_REMOTE_CLUSTER_KUBECONFIG" {
			t.Fatalf("unexpected OSAC_REMOTE_CLUSTER_KUBECONFIG env var in default local mode: %+v", env)
		}
	}
	for _, vm := range manager.VolumeMounts {
		if vm.Name == "remote-kubeconfig" {
			t.Fatalf("unexpected remote-kubeconfig volumeMount in default local mode: %+v", vm)
		}
	}
	if vol := findVolume(deployment, "remote-kubeconfig"); vol != nil {
		t.Fatalf("unexpected remote-kubeconfig volume in default local mode: %+v", *vol)
	}
}

// TestHelmDeploymentRemoteKubeconfigRendersEnvAndSecretMount verifies remote
// kubeconfig env var and read-only Secret mount rendering. The values mirror
// the installer's REMOTE_KUBECONFIG flow, which mounts the staged
// osac-remote-kubeconfig Secret under /var/run/secrets/remote.
func TestHelmDeploymentRemoteKubeconfigRendersEnvAndSecretMount(t *testing.T) {
	out, err := renderOperatorDeployment(t,
		"remoteClusterKubeconfig=/var/run/secrets/remote/kubeconfig",
		"remoteClusterKubeconfigSecretName=osac-remote-kubeconfig",
	)
	if err != nil {
		t.Fatalf("helm template failed in remote mode: %v\noutput:\n%s", err, string(out))
	}
	deployment := parseRenderedDeployment(t, out)
	manager := findManagerContainer(t, deployment)

	var foundEnv *corev1.EnvVar
	for i := range manager.Env {
		if manager.Env[i].Name == "OSAC_REMOTE_CLUSTER_KUBECONFIG" {
			foundEnv = &manager.Env[i]
			break
		}
	}
	if foundEnv == nil {
		t.Fatalf("OSAC_REMOTE_CLUSTER_KUBECONFIG env var not found on manager container")
	}
	if foundEnv.Value != "/var/run/secrets/remote/kubeconfig" {
		t.Fatalf("OSAC_REMOTE_CLUSTER_KUBECONFIG = %q, want %q",
			foundEnv.Value, "/var/run/secrets/remote/kubeconfig")
	}

	var foundMount *corev1.VolumeMount
	for i := range manager.VolumeMounts {
		if manager.VolumeMounts[i].Name == "remote-kubeconfig" {
			foundMount = &manager.VolumeMounts[i]
			break
		}
	}
	if foundMount == nil {
		t.Fatalf("remote-kubeconfig volumeMount not found on manager container")
	}
	if foundMount.MountPath != "/var/run/secrets/remote" || !foundMount.ReadOnly {
		t.Fatalf("remote-kubeconfig volumeMount = %+v, want mountPath=/var/run/secrets/remote readOnly=true", *foundMount)
	}

	foundVol := findVolume(deployment, "remote-kubeconfig")
	if foundVol == nil || foundVol.Secret == nil {
		t.Fatalf("remote-kubeconfig Secret volume not found on Pod spec: %+v", foundVol)
	}
	if foundVol.Secret.SecretName != "osac-remote-kubeconfig" {
		t.Fatalf("remote-kubeconfig secretName = %q, want %q",
			foundVol.Secret.SecretName, "osac-remote-kubeconfig")
	}
	if len(foundVol.Secret.Items) != 1 ||
		foundVol.Secret.Items[0].Key != defaultSecretKey ||
		foundVol.Secret.Items[0].Path != defaultSecretKey {
		t.Fatalf(
			"remote-kubeconfig secret items = %+v, want [{Key:kubeconfig Path:kubeconfig}]",
			foundVol.Secret.Items,
		)
	}

	for _, env := range manager.Env {
		if strings.Contains(env.Value, "BEGIN") || strings.Contains(env.Value, "token:") {
			t.Fatalf("rendered env var %q unexpectedly contains raw credential material", env.Name)
		}
	}
}

// TestHelmDeploymentRemoteKubeconfigCustomSecretKey verifies that
// remoteClusterKubeconfigSecretKey overrides the Secret item key.
func TestHelmDeploymentRemoteKubeconfigCustomSecretKey(t *testing.T) {
	out, err := renderOperatorDeployment(t,
		"remoteClusterKubeconfig=/var/run/secrets/remote/kubeconfig",
		"remoteClusterKubeconfigSecretName=osac-remote-kubeconfig",
		"remoteClusterKubeconfigSecretKey=custom-key",
	)
	if err != nil {
		t.Fatalf("helm template failed with custom secret key: %v\noutput:\n%s", err, string(out))
	}
	deployment := parseRenderedDeployment(t, out)

	foundVol := findVolume(deployment, "remote-kubeconfig")
	if foundVol == nil || foundVol.Secret == nil || len(foundVol.Secret.Items) != 1 {
		t.Fatalf("remote-kubeconfig Secret volume items not found: %+v", foundVol)
	}
	if foundVol.Secret.Items[0].Key != "custom-key" || foundVol.Secret.Items[0].Path != defaultSecretKey {
		t.Fatalf(
			"remote-kubeconfig secret items = %+v, want [{Key:custom-key Path:kubeconfig}]",
			foundVol.Secret.Items,
		)
	}
}

// TestHelmDeploymentRemoteKubeconfigMutualFailGuards verifies that helm
// template fails when only one of the two remote kubeconfig values is set.
func TestHelmDeploymentRemoteKubeconfigMutualFailGuards(t *testing.T) {
	out, err := renderOperatorDeployment(t,
		"remoteClusterKubeconfig=/var/run/secrets/remote/kubeconfig",
	)
	if err == nil {
		t.Fatalf("expected helm template to fail when remoteClusterKubeconfigSecretName is omitted, but it succeeded")
	}
	const wantSecretErr = "remoteClusterKubeconfigSecretName is required when remoteClusterKubeconfig is set"
	if !strings.Contains(string(out), wantSecretErr) {
		t.Fatalf("unexpected helm error output when secret name omitted:\n%s", string(out))
	}

	out, err = renderOperatorDeployment(t,
		"remoteClusterKubeconfigSecretName=osac-remote-kubeconfig",
	)
	if err == nil {
		t.Fatalf("expected helm template to fail when remoteClusterKubeconfig is omitted, but it succeeded")
	}
	const wantPathErr = "remoteClusterKubeconfig is required when remoteClusterKubeconfigSecretName is set"
	if !strings.Contains(string(out), wantPathErr) {
		t.Fatalf("unexpected helm error output when path omitted:\n%s", string(out))
	}
}
