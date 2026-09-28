/*
Copyright (c) 2026 Red Hat Inc.

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

package caas

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestKubernetesPreflightRejectsUnreachableClusterBeforeFixtures(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve an unused local port: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release unused local port: %v", err)
	}

	kubeconfig := filepath.Join(t.TempDir(), "config")
	writeKubeconfig(t, kubeconfig, "kind-caas-preflight", address)
	config := simConfig{
		clusterName: "caas-preflight",
		kubeconfig:  kubeconfig,
		namespace:   "osac",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	started := time.Now()
	err = preflightKubernetes(ctx, config)
	elapsed := time.Since(started)

	if err == nil || !strings.Contains(err.Error(), "query Kubernetes API for sim cluster nodes") {
		t.Fatalf("expected a clear unreachable-cluster preflight error, got %v", err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("unreachable-cluster preflight took %s; want bounded failure", elapsed)
	}
}

func TestKubernetesPreflightRejectsUnexpectedKindContext(t *testing.T) {
	kubeconfig := filepath.Join(t.TempDir(), "config")
	writeKubeconfig(t, kubeconfig, "kind-not-the-sim-cluster", "127.0.0.1:1")
	config := simConfig{
		clusterName: "caas-preflight",
		kubeconfig:  kubeconfig,
	}

	err := preflightKubernetes(context.Background(), config)
	if err == nil || !strings.Contains(err.Error(), "Kubernetes context mismatch") {
		t.Fatalf("expected a clear unexpected-context preflight error, got %v", err)
	}
}

func TestLoadSimConfigRejectsStaleEnvironmentWithoutLoggingToken(t *testing.T) {
	envPath := filepath.Join(t.TempDir(), "sim.env")
	contents := `SIM_GRPC_ADDRESS=localhost:8001
SIM_CA_FILE=ca.pem
SIM_KUBECONFIG=kubeconfig
SIM_NAMESPACE=osac
SIM_SERVER_NAME=fulfillment-internal-api.osac.svc.cluster.local
SIM_TOKEN=do-not-log-this-token
`
	if err := os.WriteFile(envPath, []byte(contents), 0o600); err != nil {
		t.Fatalf("write stale sim environment: %v", err)
	}

	_, err := loadSimConfig(envPath)
	if err == nil || !strings.Contains(err.Error(), "SIM_CLUSTER_NAME is missing") {
		t.Fatalf("expected a clear stale-environment setup error, got %v", err)
	}
	if strings.Contains(err.Error(), "do-not-log-this-token") {
		t.Fatalf("setup error leaked the configured token: %v", err)
	}
}

func TestValidateHubKubeconfigAcceptsInClusterAPIEndpoint(t *testing.T) {
	config := []byte(`apiVersion: v1
kind: Config
clusters:
- name: hub
  cluster:
    server: https://kubernetes.default.svc
contexts:
- name: hub
  context:
    cluster: hub
    user: hub-access
current-context: hub
users:
- name: hub-access
  user:
    token: test-token
`)
	if err := validateHubKubeconfig(config); err != nil {
		t.Fatalf("expected an in-cluster API endpoint to be accepted: %v", err)
	}
}

func TestValidateHubKubeconfigRejectsHostLocalEndpoint(t *testing.T) {
	config := []byte(`apiVersion: v1
kind: Config
clusters:
- name: hub
  cluster:
    server: https://127.0.0.1:6443
contexts:
- name: hub
  context:
    cluster: hub
    user: hub-access
current-context: hub
users:
- name: hub-access
  user:
    token: test-token
`)
	if err := validateHubKubeconfig(config); err == nil || !strings.Contains(err.Error(), "loopback endpoint") {
		t.Fatalf("expected a loopback hub endpoint to be rejected, got %v", err)
	}
}

func writeKubeconfig(t *testing.T, path, contextName, address string) {
	t.Helper()
	contents := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: test
  cluster:
    server: https://%s
contexts:
- name: %s
  context:
    cluster: test
    user: test
current-context: %s
users:
- name: test
  user:
    token: test-token
`, address, contextName, contextName)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write test kubeconfig: %v", err)
	}
}
