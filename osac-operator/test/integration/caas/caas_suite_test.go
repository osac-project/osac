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
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	"github.com/osac-project/osac/osac-operator/internal/controller/baremetalworker"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	"google.golang.org/grpc"
	experimentalcredentials "google.golang.org/grpc/experimental/credentials"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	preflightTimeout = 15 * time.Second
	simOwner         = "osac-operator-hack-sim"
)

var (
	connectedConfig    simConfig
	kubeClient         kubernetes.Interface
	fulfillmentConn    *grpc.ClientConn
	fulfillmentClient  baremetalworker.FulfillmentClient
	catalogItemsClient privatev1.BareMetalInstanceCatalogItemsClient
	hubsClient         privatev1.HubsClient
)

func TestConnectedCaaS(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Connected CaaS Integration Suite")
}

var _ = BeforeSuite(func() {
	config, err := loadSimConfig(findSimEnvFile())
	Expect(err).NotTo(HaveOccurred(), "connected CaaS setup configuration is invalid")
	connectedConfig = config

	ctx, cancel := context.WithTimeout(context.Background(), preflightTimeout)
	defer cancel()

	err = preflightKubernetes(ctx, config)
	Expect(err).NotTo(HaveOccurred(), "connected CaaS Kubernetes preflight failed before fixture creation")

	fulfillmentConn, err = dialFulfillment(ctx, config)
	Expect(err).NotTo(HaveOccurred(),
		"connected CaaS fulfillment-service readiness/TLS preflight failed before fixture creation")

	fulfillmentClient = baremetalworker.NewFulfillmentClientFromConn(fulfillmentConn)
	catalogItemsClient = privatev1.NewBareMetalInstanceCatalogItemsClient(fulfillmentConn)
	hubsClient = privatev1.NewHubsClient(fulfillmentConn)
	if err := preflightFulfillmentIdentity(ctx, config, catalogItemsClient, hubsClient); err != nil {
		_ = fulfillmentConn.Close()
		fulfillmentConn = nil
		Fail(fmt.Sprintf("connected CaaS fulfillment API identity preflight failed before fixture creation: %v", err))
	}
})

var _ = AfterSuite(func() {
	if fulfillmentConn != nil {
		_ = fulfillmentConn.Close()
	}
})

var _ = Describe("connected CaaS backend", func() {
	It("passes the live fulfillment API and registered hub preflight", func() {
		ctx, cancel := context.WithTimeout(context.Background(), preflightTimeout)
		defer cancel()

		Expect(fulfillmentConn).NotTo(BeNil())
		Expect(fulfillmentClient).NotTo(BeNil())

		_, err := catalogItemsClient.List(ctx, &privatev1.BareMetalInstanceCatalogItemsListRequest{})
		Expect(err).NotTo(HaveOccurred(), "authenticated private API read should remain available")

		hub, err := hubsClient.Get(ctx, privatev1.HubsGetRequest_builder{Id: "hub"}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(hub.GetObject().GetSpec().GetNamespace()).To(Equal(connectedConfig.namespace))
	})
})

type simConfig struct {
	address                string
	caFile                 string
	clusterName            string
	kubeconfig             string
	namespace              string
	expectedServiceAccount string
	serverName             string
	serviceImage           string
	token                  string
}

func findSimEnvFile() string {
	for _, candidate := range []string{"hack/sim.env", "../../../hack/sim.env"} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return "../../../hack/sim.env"
}

func loadSimConfig(path string) (simConfig, error) {
	values, err := readSimEnv(path)
	if err != nil {
		return simConfig{}, err
	}

	required := func(key string) (string, error) {
		value := strings.TrimSpace(values[key])
		if value == "" {
			return "", fmt.Errorf("%s is missing from sim.env", key)
		}
		return value, nil
	}

	var config simConfig
	if config.address, err = required("SIM_GRPC_ADDRESS"); err != nil {
		return simConfig{}, err
	}
	if config.caFile, err = required("SIM_CA_FILE"); err != nil {
		return simConfig{}, err
	}
	if config.clusterName, err = required("SIM_CLUSTER_NAME"); err != nil {
		return simConfig{}, err
	}
	if config.kubeconfig, err = required("SIM_KUBECONFIG"); err != nil {
		return simConfig{}, err
	}
	if config.namespace, err = required("SIM_NAMESPACE"); err != nil {
		return simConfig{}, err
	}
	if config.expectedServiceAccount, err = required("SIM_EXPECTED_SERVICE_ACCOUNT"); err != nil {
		return simConfig{}, err
	}
	if config.serverName, err = required("SIM_SERVER_NAME"); err != nil {
		return simConfig{}, err
	}
	if config.serviceImage, err = required("SIM_SERVICE_IMAGE"); err != nil {
		return simConfig{}, err
	}
	if config.token, err = required("SIM_TOKEN"); err != nil {
		return simConfig{}, err
	}

	baseDir := filepath.Dir(path)
	if !filepath.IsAbs(config.caFile) {
		config.caFile = filepath.Join(baseDir, config.caFile)
	}
	if !filepath.IsAbs(config.kubeconfig) {
		config.kubeconfig = filepath.Join(baseDir, config.kubeconfig)
	}
	return config, nil
}

func readSimEnv(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read sim environment file %q: %w (run 'make sim-up' first)", path, err)
	}

	values := make(map[string]string)
	for lineNumber, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			return nil, fmt.Errorf("malformed sim.env entry on line %d", lineNumber+1)
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), "\"'")
		if key == "" {
			return nil, fmt.Errorf("empty sim.env key on line %d", lineNumber+1)
		}
		if _, exists := values[key]; exists {
			return nil, fmt.Errorf("duplicate sim.env key %s", key)
		}
		values[key] = value
	}
	return values, nil
}

func preflightKubernetes(ctx context.Context, config simConfig) error {
	rawConfig, err := clientcmd.LoadFromFile(config.kubeconfig)
	if err != nil {
		return fmt.Errorf("load configured Kubernetes kubeconfig: %w", err)
	}
	expectedContext := "kind-" + config.clusterName
	if rawConfig.CurrentContext != expectedContext {
		return fmt.Errorf("Kubernetes context mismatch: expected %q for sim cluster %q, got %q",
			expectedContext, config.clusterName, rawConfig.CurrentContext)
	}

	kubeConfig, err := clientcmd.BuildConfigFromFlags("", config.kubeconfig)
	if err != nil {
		return fmt.Errorf("build Kubernetes client configuration: %w", err)
	}
	kubeConfig.Timeout = preflightTimeout
	client, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		return fmt.Errorf("create Kubernetes client: %w", err)
	}

	nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("query Kubernetes API for sim cluster nodes: %w", err)
	}
	controlPlaneName := config.clusterName + "-control-plane"
	foundControlPlane := false
	for _, node := range nodes.Items {
		if node.Name == controlPlaneName {
			foundControlPlane = true
			break
		}
	}
	if !foundControlPlane {
		return fmt.Errorf("Kubernetes context %q is reachable but has no Kind control-plane node %q",
			expectedContext, controlPlaneName)
	}

	if _, err := client.CoreV1().Namespaces().Get(ctx, config.namespace, metav1.GetOptions{}); err != nil {
		return fmt.Errorf("query sim namespace %q in cluster %q: %w", config.namespace, config.clusterName, err)
	}
	kubeSystem, err := client.CoreV1().Namespaces().Get(ctx, "kube-system", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("query sim cluster ownership marker: %w", err)
	}
	if kubeSystem.Labels["osac.openshift.io/sim-owner"] != simOwner ||
		kubeSystem.Annotations["osac.openshift.io/sim-cluster-name"] != config.clusterName {
		return fmt.Errorf("Kubernetes context %q is not marked as the owned sim cluster %q",
			expectedContext, config.clusterName)
	}
	deployment, err := client.AppsV1().Deployments(config.namespace).Get(
		ctx, "fulfillment-grpc-server", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("query fulfillment-service deployment in namespace %q: %w", config.namespace, err)
	}
	deployedImage := ""
	for _, container := range deployment.Spec.Template.Spec.Containers {
		if container.Name == "grpc-server" {
			deployedImage = container.Image
			break
		}
	}
	if deployedImage != config.serviceImage {
		return fmt.Errorf("fulfillment-service image mismatch: expected checkout image %q, got %q",
			config.serviceImage, deployedImage)
	}
	controller, err := client.AppsV1().Deployments(config.namespace).Get(
		ctx, "fulfillment-controller", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("query fulfillment reconciler deployment: %w", err)
	}
	if controller.Status.AvailableReplicas < 1 {
		return fmt.Errorf("fulfillment reconciler unavailable (replicas %d); check controller logs and OAuth/IDP credentials",
			controller.Status.AvailableReplicas)
	}

	if err := preflightRequiredCRDs(client.Discovery()); err != nil {
		return err
	}
	kubeClient = client
	return nil
}

func preflightRequiredCRDs(client discovery.DiscoveryInterface) error {
	required := map[string][]string{
		"osac.openshift.io/v1alpha1":         {"clusterorders", "baremetalinstances"},
		"agent-install.openshift.io/v1beta1": {"agents", "infraenvs"},
		"hive.openshift.io/v1":               {"clusterdeployments"},
		"hypershift.openshift.io/v1beta1":    {"nodepools"},
	}
	for groupVersion, expectedResources := range required {
		resources, err := client.ServerResourcesForGroupVersion(groupVersion)
		if err != nil {
			return fmt.Errorf("required CRD group/version %s is unavailable: %w", groupVersion, err)
		}
		available := make(map[string]struct{}, len(resources.APIResources))
		for _, resource := range resources.APIResources {
			available[resource.Name] = struct{}{}
		}
		for _, name := range expectedResources {
			if _, ok := available[name]; !ok {
				return fmt.Errorf("required resource %s/%s is not served by the sim cluster", groupVersion, name)
			}
		}
	}
	return nil
}

func dialFulfillment(ctx context.Context, config simConfig) (*grpc.ClientConn, error) {
	caPEM, err := os.ReadFile(config.caFile)
	if err != nil {
		return nil, fmt.Errorf("read configured fulfillment CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("configured fulfillment CA contains no valid PEM certificates")
	}
	if strings.TrimSpace(config.serverName) == "" {
		return nil, fmt.Errorf("SIM_SERVER_NAME must be set for TLS hostname validation")
	}

	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    roots,
		ServerName: config.serverName,
	}
	//nolint:staticcheck // Blocking dial preserves the bounded fail-fast transport preflight.
	conn, err := grpc.DialContext(
		ctx,
		config.address,
		grpc.WithTransportCredentials(experimentalcredentials.NewTLSWithALPNDisabled(tlsConfig)),
		grpc.WithPerRPCCredentials(staticTokenCredentials{token: config.token}),
		grpc.WithBlock(), //nolint:staticcheck // Required with DialContext to check TLS/transport before fixtures.
	)
	if err != nil {
		return nil, fmt.Errorf("connect to fulfillment gRPC endpoint %q: %w", config.address, err)
	}
	return conn, nil
}

func preflightFulfillmentIdentity(
	ctx context.Context,
	config simConfig,
	client privatev1.BareMetalInstanceCatalogItemsClient,
	hubs privatev1.HubsClient,
) error {
	subject, err := serviceAccountSubject(config.token)
	if err != nil {
		return err
	}
	expected := fmt.Sprintf("system:serviceaccount:%s:%s", config.namespace, config.expectedServiceAccount)
	if subject != expected {
		return fmt.Errorf("token subject mismatch: expected %q, got %q", expected, subject)
	}

	if _, err := client.List(ctx, &privatev1.BareMetalInstanceCatalogItemsListRequest{}); err != nil {
		return fmt.Errorf("authenticated private API read failed for expected service account %q: %w",
			expected, err)
	}
	hubResponse, err := hubs.Get(ctx, privatev1.HubsGetRequest_builder{Id: "hub"}.Build())
	if err != nil {
		return fmt.Errorf("registered fulfillment hub %q is unavailable: %w", "hub", err)
	}
	if hubResponse.GetObject().GetSpec().GetNamespace() != config.namespace {
		return fmt.Errorf("registered fulfillment hub %q targets namespace %q, want %q",
			"hub", hubResponse.GetObject().GetSpec().GetNamespace(), config.namespace)
	}
	if err := validateHubKubeconfig(hubResponse.GetObject().GetSpec().GetKubeconfig()); err != nil {
		return fmt.Errorf("registered fulfillment hub %q has an unusable kubeconfig: %w", "hub", err)
	}
	return nil
}

func validateHubKubeconfig(data []byte) error {
	config, err := clientcmd.Load(data)
	if err != nil {
		return fmt.Errorf("parse registered kubeconfig: %w", err)
	}
	currentContext, ok := config.Contexts[config.CurrentContext]
	if !ok || currentContext.Cluster == "" || currentContext.AuthInfo == "" {
		return fmt.Errorf("current context does not reference a cluster and user")
	}
	cluster, ok := config.Clusters[currentContext.Cluster]
	if !ok {
		return fmt.Errorf("current context references a missing cluster")
	}
	user, ok := config.AuthInfos[currentContext.AuthInfo]
	if !ok || user.Token == "" {
		return fmt.Errorf("current context has no bearer token")
	}
	server, err := url.Parse(cluster.Server)
	if err != nil || server.Scheme != "https" || server.Hostname() == "" {
		return fmt.Errorf("API server must be a valid HTTPS URL")
	}
	if strings.EqualFold(server.Hostname(), "localhost") {
		return fmt.Errorf("API server %q is a host-local endpoint, not reachable from the fulfillment pod", server.Host)
	}
	if ip := net.ParseIP(server.Hostname()); ip != nil && ip.IsLoopback() {
		return fmt.Errorf("API server %q is a loopback endpoint, not reachable from the fulfillment pod", server.Host)
	}
	return nil
}

func serviceAccountSubject(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("SIM_TOKEN is not a Kubernetes service-account JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decode SIM_TOKEN claims: %w", err)
	}
	var claims struct {
		Subject string `json:"sub"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("parse SIM_TOKEN claims: %w", err)
	}
	if claims.Subject == "" {
		return "", fmt.Errorf("SIM_TOKEN has no subject claim")
	}
	return claims.Subject, nil
}

type staticTokenCredentials struct {
	token string
}

func (c staticTokenCredentials) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{"authorization": "Bearer " + c.token}, nil
}

func (staticTokenCredentials) RequireTransportSecurity() bool {
	return true
}
