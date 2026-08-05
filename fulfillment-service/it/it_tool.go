/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package it

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/onsi/gomega/ghttp"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/jq"
	"github.com/osac-project/fulfillment-service/internal/network"
	"github.com/osac-project/fulfillment-service/internal/oauth"
	"github.com/osac-project/fulfillment-service/internal/testing"
	"github.com/osac-project/fulfillment-service/internal/uuid"
	"go.yaml.in/yaml/v2"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/version"
)

var ServiceAccountTenants = map[string]string{
	"alice": "a",
	"bob":   "a",
	"carol": "b",
	"dave":  "b",
}

var OIDCTenants = map[string][]string{
	"adam":    {"engineering"},
	"ben":     {"development"},
	"charles": {"sales"},
}

// ToolBuilder contains the data and logic needed to create an instance of the integration test tool. Don't create
// instances of this directly, use the NewTool function instead.
type ToolBuilder struct {
	logger      *slog.Logger
	projectDir  string
	crdFiles    []string
	keepCluster bool
	keepService bool
	debug       bool
	secret      string
	caKeyFile   string
	caCrtFile   string
}

// Tool is an instance of the integration test tool that sets up the test environment. Don't create instances of this
// directly, use the NewTool function instead.
type Tool struct {
	logger        *slog.Logger
	projectDir    string
	crdFiles      []string
	keepKind      bool
	keepService   bool
	debug         bool
	caKeyFile     string
	caCrtFile     string
	tmpDir        string
	cluster       *testing.Kind
	kubeClient    crclient.Client
	kubeClientSet *kubernetes.Clientset
	caPool        *x509.CertPool
	kcFile        string
	internalView  *ToolView
	externalView  *ToolView
	secret        string
	jqTool        *jq.Tool
	cliBinaryPath string
}

// ToolView contains the gRPC connections and HTTP clients that can be used to connect to the cluster. This is a
// separate type to simplify having two copies: one for the internal API and another one for the external API.
type ToolView struct {
	anonymousConn   *grpc.ClientConn
	emergencyConn   *grpc.ClientConn
	adminConn       *grpc.ClientConn
	userConn        *grpc.ClientConn
	anonymousClient *http.Client
	emergencyClient *http.Client
	adminClient     *http.Client
	userClient      *http.Client
}

// NewTool creates a builder that can then be used to configure and create an instance of the integration test tool.
func NewTool() *ToolBuilder {
	return &ToolBuilder{}
}

// SetLogger sets the logger that the tool will use to write messages to the log. This is mandatory.
func (b *ToolBuilder) SetLogger(value *slog.Logger) *ToolBuilder {
	b.logger = value
	return b
}

// SetProjectDir sets the root directory of the project. This is optional, if not specified, the tool will search for
// the 'go.mod' file starting from the current directory.
func (b *ToolBuilder) SetProjectDir(value string) *ToolBuilder {
	b.projectDir = value
	return b
}

// AddCrdFile adds a CRD file to be installed in the cluster.
func (b *ToolBuilder) AddCrdFile(value string) *ToolBuilder {
	b.crdFiles = append(b.crdFiles, value)
	return b
}

// AddCrdFiles adds multiple CRD files to be installed in the cluster.
func (b *ToolBuilder) AddCrdFiles(values ...string) *ToolBuilder {
	b.crdFiles = append(b.crdFiles, values...)
	return b
}

// SetKeepCluster sets whether to keep the cluster after the tests complete. The default is to destroy the cluster.
func (b *ToolBuilder) SetKeepCluster(value bool) *ToolBuilder {
	b.keepCluster = value
	return b
}

// SetKeepService sets whether to keep the service after the tests complete. The default is to undeploy the service.
func (b *ToolBuilder) SetKeepService(value bool) *ToolBuilder {
	b.keepService = value
	return b
}

// SetDebug sets whether to enable the debug mode. This means that the debugger binary will be added to the container
// image, and that the services will be started under the control of the debugger. Access to the debugger will be done
// via the following ports:
//
// - gRPC server: 30001
// - REST gateway: 30002
// - Controller: 30003
func (b *ToolBuilder) SetDebug(value bool) *ToolBuilder {
	b.debug = value
	return b
}

// SetSecret sets the secret used in all places where passwords or secrets are needed, such as service account client
// secrets and user passwords. If not set then a random one will be generated.
func (b *ToolBuilder) SetSecret(value string) *ToolBuilder {
	b.secret = value
	return b
}

// SetCaFiles sets the paths to PEM files containing a pre-generated CA private key and certificate. When set, the
// Kind cluster will use these files instead of generating a new CA each time. This is optional.
func (b *ToolBuilder) SetCaFiles(keyFile, crtFile string) *ToolBuilder {
	b.caKeyFile = keyFile
	b.caCrtFile = crtFile
	return b
}

// Build uses the data stored in the builder to create a new instance of the integration test tool.
func (b *ToolBuilder) Build() (result *Tool, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if (b.caKeyFile == "") != (b.caCrtFile == "") {
		err = errors.New("key file and certificate file must both be provided or both be omitted")
		return
	}

	// Find the project directory if not specified:
	projectDir := b.projectDir
	if projectDir == "" {
		projectDir, err = b.findProjectDir()
		if err != nil {
			return
		}
	}

	// Create the JQ tool:
	jqTool, err := jq.NewTool().
		SetLogger(b.logger).
		Build()
	if err != nil {
		err = fmt.Errorf("failed to create JQ tool: %w", err)
		return
	}

	// Create and populate the object:
	result = &Tool{
		logger:      b.logger,
		projectDir:  projectDir,
		crdFiles:    slices.Clone(b.crdFiles),
		keepKind:    b.keepCluster,
		keepService: b.keepService,
		debug:       b.debug,
		caKeyFile:   b.caKeyFile,
		caCrtFile:   b.caCrtFile,
		secret:      b.secret,
		jqTool:      jqTool,
	}
	return
}

// findProjectDir finds the project directory by searching for the go.mod file starting from the current directory.
func (b *ToolBuilder) findProjectDir() (result string, err error) {
	currentDir, err := os.Getwd()
	if err != nil {
		err = fmt.Errorf("failed to get current directory: %w", err)
		return
	}
	for {
		modFile := filepath.Join(currentDir, "go.mod")
		_, statErr := os.Stat(modFile)
		if statErr == nil {
			result = currentDir
			return
		}
		if !errors.Is(statErr, os.ErrNotExist) {
			err = fmt.Errorf("failed to stat '%s': %w", modFile, statErr)
			return
		}
		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
			err = fmt.Errorf("failed to find 'go.mod' file starting from '%s'", currentDir)
			return
		}
		currentDir = parentDir
	}
}

// Setup prepares the integration test environment. This includes building the binary and container image, starting
// the Kind cluster, installing Keycloak and the service, and creating the necessary clients.
func (t *Tool) Setup(ctx context.Context) error {
	var err error

	// Check that the required host names are resolvable:
	err = t.checkAddress(ctx, keycloakAddr)
	if err != nil {
		return err
	}
	err = t.checkAddress(ctx, externalServiceAddr)
	if err != nil {
		return err
	}
	err = t.checkAddress(ctx, internalServiceAddr)
	if err != nil {
		return err
	}

	// Create a temporary directory:
	t.tmpDir, err = os.MkdirTemp("", "*.it")
	if err != nil {
		return fmt.Errorf("failed to create temporary directory: %w", err)
	}

	// Check that the required command line tools are available:
	err = t.checkCommands(ctx)
	if err != nil {
		return err
	}

	// Build the container image:
	imageRef, err := t.buildImage(ctx)
	if err != nil {
		return err
	}

	// Build the CLI binary:
	err = t.buildCLI(ctx)
	if err != nil {
		return err
	}

	// Save the container image to a tar file:
	imageTar, err := t.saveImage(ctx, imageRef)
	if err != nil {
		return err
	}

	// Start the cluster:
	if err = t.startCluster(ctx); err != nil {
		return err
	}

	// Load the container image into the cluster:
	err = t.cluster.LoadArchive(ctx, imageTar)
	if err != nil {
		return fmt.Errorf("failed to load container image into cluster: %w", err)
	}

	// Write the kubeconfig file:
	t.kcFile = filepath.Join(t.tmpDir, "kubeconfig")
	err = os.WriteFile(t.kcFile, t.cluster.Kubeconfig(), 0400)
	if err != nil {
		return fmt.Errorf("failed to write kubeconfig file: %w", err)
	}

	// Get the clients:
	t.kubeClient = t.cluster.Client()
	t.kubeClientSet = t.cluster.ClientSet()

	// Resolve the secret to use for passwords and credentials:
	err = t.resolveRandomSecret(ctx)
	if err != nil {
		return err
	}

	// Load the CA bundle:
	err = t.loadCaBundle(ctx)
	if err != nil {
		return err
	}

	// Install PostgreSQL:
	err = t.deployPostgres(ctx)
	if err != nil {
		return err
	}

	// Create the Keycloak database resources:
	err = t.createKeycloakDatabaseResources(ctx)
	if err != nil {
		return err
	}

	// Install Keycloak:
	err = t.deployKeycloak(ctx)
	if err != nil {
		return err
	}

	// Create the service database resources:
	err = t.createServiceDatabaseResources(ctx)
	if err != nil {
		return err
	}

	// Create the controller credentials:
	err = t.createControllerCredentials(ctx)
	if err != nil {
		return err
	}

	// Install the service:
	err = t.deployService(ctx, imageRef)
	if err != nil {
		return err
	}

	// Create the gRPC and HTTP clients:
	err = t.createClients(ctx)
	if err != nil {
		return err
	}

	// Wait for the servers to be ready:
	err = t.waitForServersReady(ctx)
	if err != nil {
		return err
	}

	// Create the hub namespace:
	err = t.createHubNamespace(ctx)
	if err != nil {
		return err
	}

	// Create the test tenants:
	err = t.createTenants(ctx)
	if err != nil {
		return err
	}

	// Add users to Keycloak Organizations:
	err = t.addUsersToKeycloakOrganizations(ctx)
	if err != nil {
		return err
	}

	// Create the test user service accounts:
	err = t.createUserServiceAccounts(ctx)
	if err != nil {
		return err
	}

	// Register the hub:
	if err = t.registerHub(ctx); err != nil {
		return err
	}

	return nil
}

// checkAddress checks that the given address is resolvable.
func (t *Tool) checkAddress(ctx context.Context, addr string) error {
	t.logger.DebugContext(ctx, "Checking address", "address", addr)
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("failed to split host and port from '%s': %w", addr, err)
	}
	_, err = net.LookupHost(host)
	if err != nil {
		return fmt.Errorf(
			"failed to lookup host '%[1]s', you may need to add a '127.0.0.1 %[1]s' entry to the "+
				"'/etc/hosts' file: %[2]w",
			host, err,
		)
	}
	return nil
}

// checkCommands checks that the required command line tools are available.
func (t *Tool) checkCommands(ctx context.Context) error {
	t.logger.DebugContext(ctx, "Checking command line tools")
	commands := []string{
		kubectlCmd,
		podmanCmd,
		helmCmd,
	}
	for _, command := range commands {
		_, err := exec.LookPath(command)
		if err != nil {
			return fmt.Errorf("command '%s' is not available: %w", command, err)
		}
	}
	return nil
}

// buildImage builds the container image and returns the full image reference.
func (t *Tool) buildImage(ctx context.Context) (result string, err error) {
	t.logger.DebugContext(ctx, "Building image")
	imageTag := time.Now().Format("20060102150405")
	imageRef := fmt.Sprintf("%s:%s", imageName, imageTag)

	// Resolve the version on the host so that the container build does not
	// need access to the .git directory. This is required when building from
	// a git worktree, where the .git file is a pointer that cannot be
	// resolved inside the container context.
	versionBytes, versionErr := exec.CommandContext(ctx, "git", "-C", t.projectDir, "describe", "--tags", "--always").Output()
	gitVersion := "dev"
	if versionErr == nil {
		gitVersion = strings.TrimSpace(string(versionBytes))
	}

	buildTarget := "runtime"
	if t.debug {
		buildTarget = "runtime-debug"
	}
	buildCmd, err := testing.NewCommand().
		SetLogger(t.logger).
		SetHome(t.projectDir).
		SetDir(t.projectDir).
		SetName(podmanCmd).
		SetArgs(
			"build",
			"--build-arg", fmt.Sprintf("DEBUG=%t", t.debug),
			"--build-arg", fmt.Sprintf("VERSION=%s", gitVersion),
			"--target", buildTarget,
			"--tag", imageRef,
			"--file", "Containerfile",
			".",
		).
		Build()
	if err != nil {
		err = fmt.Errorf("failed to create command to build image: %w", err)
		return
	}
	err = buildCmd.Execute(ctx)
	if err != nil {
		err = fmt.Errorf("failed to build image: %w", err)
		return
	}
	result = imageRef
	return
}

// saveImage saves the given container image to a tar file and returns the path to that tar file.
func (t *Tool) saveImage(ctx context.Context, imageRef string) (result string, err error) {
	t.logger.DebugContext(ctx, "Saving container image to tar file")
	imageTar := filepath.Join(t.tmpDir, "image.tar")
	saveCmd, err := testing.NewCommand().
		SetLogger(t.logger).
		SetHome(t.projectDir).
		SetDir(t.projectDir).
		SetName(podmanCmd).
		SetArgs(
			"save",
			"--output", imageTar,
			imageRef,
		).
		Build()
	if err != nil {
		err = fmt.Errorf("failed to create command to save image: %w", err)
		return
	}
	err = saveCmd.Execute(ctx)
	if err != nil {
		err = fmt.Errorf("failed to save container image: %w", err)
		return
	}
	result = imageTar
	return
}

func (t *Tool) startCluster(ctx context.Context) error {
	t.logger.DebugContext(ctx, "Starting cluster")
	builder := testing.NewKind()
	builder.SetLogger(t.logger)
	builder.SetHome(t.projectDir)
	builder.SetName("fulfillment-service-it")
	for _, crdFile := range t.crdFiles {
		builder.AddCrdFile(crdFile)
	}
	if t.debug {
		builder.AddPortMapping("127.0.0.1", 30001, 30001) // gRPC server.
		builder.AddPortMapping("127.0.0.1", 30002, 30002) // REST gateway.
		builder.AddPortMapping("127.0.0.1", 30003, 30003) // Controller.
	}
	if t.caKeyFile != "" && t.caCrtFile != "" {
		builder.SetCaFiles(t.caKeyFile, t.caCrtFile)
	}
	var err error
	t.cluster, err = builder.Build()
	if err != nil {
		return fmt.Errorf("failed to create cluster: %w", err)
	}
	err = t.cluster.Start(ctx)
	if err != nil {
		return fmt.Errorf("failed to start cluster: %w", err)
	}
	return nil
}

// resolveRandomSecret determines the secret to use for all passwords and credentials. If the secret was explicitly
// provided (via the 'IT_SECRET' environment variable) it is used and persisted to the cluster. Otherwise, the method
// tries to read an existing secret from the cluster. If none exists, a random one is generated and saved. This ensures
// that re-runs against an existing cluster reuse the same secret.
func (t *Tool) resolveRandomSecret(ctx context.Context) error {
	t.logger.DebugContext(ctx, "Resolving secret")

	// Try to fetch the current secret from the cluster:
	secretKey := crclient.ObjectKey{
		Namespace: "default",
		Name:      randomSecretName,
	}
	secretObject := &corev1.Secret{}
	err := t.kubeClient.Get(ctx, secretKey, secretObject)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf(
			"failed to get random secret '%s/%s': %w",
			randomSecretNamespace, randomSecretName, err,
		)
	}

	// If the secret didn't exist then generate a new one if needed, and save it to the cluster:
	if apierrors.IsNotFound(err) {
		if t.secret == "" {
			t.secret = uuid.New()
		}
		secretObject = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: secretKey.Namespace,
				Name:      secretKey.Name,
			},
			Data: map[string][]byte{
				randomSecretKey: []byte(t.secret),
			},
		}
		err = t.kubeClient.Create(ctx, secretObject)
		if err != nil {
			return fmt.Errorf(
				"failed to create random secret '%s/%s': %w",
				randomSecretNamespace, randomSecretName, err,
			)
		}
		return nil
	}

	// Make sure that the secret loaded from the cluster does contain the expected key and that it matches the one
	// explicitly provided, as otherwise things will break:
	secretBytes, ok := secretObject.Data[randomSecretKey]
	if !ok {
		return fmt.Errorf(
			"secret '%s/%s' does not contain the expected key '%s'",
			randomSecretNamespace, randomSecretName, randomSecretKey,
		)
	}
	secretText := string(secretBytes)
	if t.secret != "" && t.secret != secretText {
		return fmt.Errorf(
			"secret '%s/%s' has changed from '%s' to '%s'",
			randomSecretNamespace, randomSecretName, t.secret, secretText,
		)
	}

	// If we are here then we can use the secret loaded from the cluster:
	t.secret = secretText

	return nil
}

func (t *Tool) loadCaBundle(ctx context.Context) error {
	t.logger.DebugContext(ctx, "Loading CA bundle")

	// Wait for the CA bundle to be available:
	caBundleKey := crclient.ObjectKey{
		Namespace: "default",
		Name:      "ca-bundle",
	}
	caBundleMap := &corev1.ConfigMap{}
	var err error
	for i := 0; i < 60; i++ {
		err = t.kubeClient.Get(ctx, caBundleKey, caBundleMap)
		if err == nil {
			break
		}
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get CA bundle: %w", err)
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		return fmt.Errorf("CA bundle not available after waiting: %w", err)
	}

	// Write CA files:
	var caFiles []string
	for caKey, caText := range caBundleMap.Data {
		caFile := filepath.Join(t.tmpDir, caKey)
		err = os.WriteFile(caFile, []byte(caText), 0400)
		if err != nil {
			return fmt.Errorf("failed to write CA file: %w", err)
		}
		caFiles = append(caFiles, caFile)
	}

	// Create the CA pool:
	t.caPool, err = network.NewCertPool().
		SetLogger(t.logger).
		AddFiles(caFiles...).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create CA pool: %w", err)
	}
	return nil
}

// createKeycloakDatabaseResources creates the cert-manager Certificate and connection ConfigMap in
// the keycloak namespace for connecting to the PostgreSQL database. The certificate uses PKCS8
// encoding with DER additional output format, as required by the Keycloak JDBC driver.
func (t *Tool) createKeycloakDatabaseResources(ctx context.Context) error {
	t.logger.DebugContext(ctx, "Creating Keycloak database resources")

	// Create the keycloak namespace if it doesn't exist:
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "keycloak",
		},
	}
	err := t.kubeClient.Create(ctx, ns)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create keycloak namespace: %w", err)
	}

	// Create the cert-manager Certificate:
	cert := &unstructured.Unstructured{}
	cert.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cert-manager.io",
		Version: "v1",
		Kind:    "Certificate",
	})
	cert.SetNamespace("keycloak")
	cert.SetName("keycloak-database-client")
	cert.Object["spec"] = map[string]any{
		"issuerRef": map[string]any{
			"kind": "ClusterIssuer",
			"name": "default-ca",
		},
		"usages":     []any{"client auth"},
		"commonName": "keycloak",
		"secretName": keycloakDatabaseClientCertSecret,
		"privateKey": map[string]any{
			"encoding":       "PKCS8",
			"rotationPolicy": "Always",
		},
		"additionalOutputFormats": []any{
			map[string]any{"type": "DER"},
		},
	}
	err = t.kubeClient.Create(ctx, cert)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create keycloak database client certificate: %w", err)
	}

	// Create the ConfigMap with the database connection details:
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "keycloak",
			Name:      keycloakDatabaseConfigMap,
		},
		Data: map[string]string{
			"url":      "postgres://postgres.postgres.svc.cluster.local:5432/keycloak",
			"user":     "keycloak",
			"password": "",
			"sslmode":  "require",
		},
	}
	err = t.kubeClient.Create(ctx, configMap)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create keycloak database config map: %w", err)
	}

	// Wait for the certificate secret to be available:
	secretKey := crclient.ObjectKey{
		Namespace: "keycloak",
		Name:      keycloakDatabaseClientCertSecret,
	}
	secret := &corev1.Secret{}
	for i := 0; i < 60; i++ {
		err = t.kubeClient.Get(ctx, secretKey, secret)
		if err == nil {
			return nil
		}
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get keycloak database client certificate secret: %w", err)
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("keycloak database client certificate secret not available after waiting: %w", err)
}

// createServiceDatabaseResources creates the cert-manager Certificate and connection ConfigMap in
// the osac namespace for connecting the fulfillment service to the PostgreSQL database.
func (t *Tool) createServiceDatabaseResources(ctx context.Context) error {
	t.logger.DebugContext(ctx, "Creating service database resources")

	// Create the osac namespace if it doesn't exist:
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "osac",
		},
	}
	err := t.kubeClient.Create(ctx, ns)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create osac namespace: %w", err)
	}

	// Create the cert-manager Certificate:
	cert := &unstructured.Unstructured{}
	cert.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cert-manager.io",
		Version: "v1",
		Kind:    "Certificate",
	})
	cert.SetNamespace("osac")
	cert.SetName("fulfillment-database-client")
	cert.Object["spec"] = map[string]any{
		"issuerRef": map[string]any{
			"kind": "ClusterIssuer",
			"name": "default-ca",
		},
		"usages":     []any{"client auth"},
		"commonName": "service",
		"secretName": serviceDatabaseClientCertSecret,
		"privateKey": map[string]any{
			"rotationPolicy": "Always",
		},
	}
	err = t.kubeClient.Create(ctx, cert)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create service database client certificate: %w", err)
	}

	// Create the ConfigMap with the database connection details:
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "osac",
			Name:      serviceDatabaseConfigMap,
		},
		Data: map[string]string{
			"url":     "postgres://service@postgres.postgres.svc.cluster.local:5432/service",
			"sslmode": "verify-full",
		},
	}
	err = t.kubeClient.Create(ctx, configMap)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create service database config map: %w", err)
	}

	// Wait for the certificate secret to be available:
	secretKey := crclient.ObjectKey{
		Namespace: "osac",
		Name:      serviceDatabaseClientCertSecret,
	}
	secret := &corev1.Secret{}
	for i := 0; i < 60; i++ {
		err = t.kubeClient.Get(ctx, secretKey, secret)
		if err == nil {
			return nil
		}
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get service database client certificate secret: %w", err)
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("service database client certificate secret not available after waiting: %w", err)
}

// createControllerCredentials creates a Kubernetes secret containing the OAuth client credentials that the controller
// uses to authenticate to the API.
func (t *Tool) createControllerCredentials(ctx context.Context) error {
	t.logger.DebugContext(ctx, "Creating controller API credentials secret")
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "osac",
			Name:      controllerCredentialsSecret,
		},
		StringData: map[string]string{
			"client-id":     controllerClientId,
			"client-secret": t.secret,
		},
	}
	err := t.kubeClient.Create(ctx, secret)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create controller credentials secret: %w", err)
	}
	return nil
}

// deployKeycloak installs the Keycloak chart.
func (t *Tool) deployKeycloak(ctx context.Context) error {
	// Get the host name:
	t.logger.DebugContext(ctx, "Installing Keycloak chart")
	host, _, err := net.SplitHostPort(keycloakAddr)
	if err != nil {
		return fmt.Errorf("failed to split host and port from '%s': %w", keycloakAddr, err)
	}

	// Prepare the lists of clients, users and groups:
	var (
		clients []map[string]any
		users   []map[string]any
		groups  []map[string]any
	)

	// Prepare a map containing the values for the chart:
	valuesData := map[string]any{
		"hostname": host,
		"admin": map[string]any{
			"username": "admin",
			"password": t.secret,
		},
		"certs": map[string]any{
			"issuerRef": map[string]any{
				"kind": "ClusterIssuer",
				"name": "default-ca",
			},
			"caBundle": map[string]any{
				"configMap": "ca-bundle",
			},
		},
		"database": map[string]any{
			"connection": []any{
				map[string]any{
					"configMap": map[string]any{
						"name": keycloakDatabaseConfigMap,
						"items": []any{
							map[string]any{
								"key":   "url",
								"param": "url",
							},
							map[string]any{
								"key":   "user",
								"param": "user",
							},
							map[string]any{
								"key":   "password",
								"param": "password",
							},
							map[string]any{
								"key":   "sslmode",
								"param": "sslmode",
							},
						},
					},
				},
				map[string]any{
					"secret": map[string]any{
						"name": keycloakDatabaseClientCertSecret,
						"items": []any{
							map[string]any{
								"key":   "tls.crt",
								"param": "sslcert",
							},
							map[string]any{
								"key":   "key.der",
								"param": "sslkey",
							},
							map[string]any{
								"key":   "ca.crt",
								"param": "sslrootcert",
							},
						},
					},
				},
			},
		},
	}

	// Add the groups:
	addGroup := func(name string) {
		for _, group := range groups {
			if group["name"] == name {
				return
			}
		}
		groups = append(
			groups,
			map[string]any{
				"name": name,
				"path": fmt.Sprintf("/%s", name),
			},
		)
	}
	addGroup(adminsGroup)
	addGroup(usersGroup)

	// Add the users:
	type userData struct {
		Name     string
		First    string
		Last     string
		Groups   []string
		Password string
	}
	addUser := func(data userData) {
		groups := make([]string, len(data.Groups))
		for i, name := range data.Groups {
			addGroup(name)
			groups[i] = fmt.Sprintf("/%s", name)
		}
		users = append(
			users,
			map[string]any{
				"username":      data.Name,
				"enabled":       true,
				"firstName":     data.First,
				"lastName":      data.Last,
				"email":         fmt.Sprintf("%s@example.com", data.Name),
				"emailVerified": true,
				"groups":        groups,
				"credentials": []any{
					map[string]any{
						"type":      "password",
						"value":     data.Password,
						"temporary": false,
					},
				},
			},
		)
	}
	addUser(userData{
		Name:     adminUsername,
		First:    "Ms.",
		Last:     "Admin",
		Groups:   []string{adminsGroup},
		Password: adminsPassword,
	})
	addUser(userData{
		Name:     userUsername,
		First:    "Mr.",
		Last:     "User",
		Groups:   []string{usersGroup},
		Password: usersPassword,
	})

	// Add the OIDC tenants
	for oidcUser, oidcGroups := range OIDCTenants {
		addUser(userData{
			Name:     oidcUser,
			First:    oidcUser,
			Last:     oidcUser,
			Groups:   oidcGroups,
			Password: usersPassword,
		})
	}

	// Add the service account clients and their corresponding users:
	type serviceAccountData struct {
		Name        string
		Description string
		ClientId    string
		ClientRoles map[string][]string
	}
	addServiceAccount := func(data serviceAccountData) {
		clients = append(
			clients,
			map[string]any{
				"name":                      data.Name,
				"description":               data.Description,
				"clientId":                  data.ClientId,
				"enabled":                   true,
				"clientAuthenticatorType":   "client-secret",
				"secret":                    t.secret,
				"serviceAccountsEnabled":    true,
				"publicClient":              false,
				"standardFlowEnabled":       false,
				"implicitFlowEnabled":       false,
				"directAccessGrantsEnabled": false,
				"protocol":                  "openid-connect",
				"fullScopeAllowed":          true,
				"defaultClientScopes": []string{
					"basic",
					"username",
					"groups",
					auth.Audience,
				},
			},
		)
		users = append(
			users, map[string]any{
				"username":               fmt.Sprintf("service-account-%s", data.ClientId),
				"enabled":                true,
				"serviceAccountClientId": data.ClientId,
				"clientRoles":            data.ClientRoles,
			},
		)
	}
	addServiceAccount(serviceAccountData{
		Name:        "OSAC administrator",
		Description: "Service account for the OSAC administrator",
		ClientId:    adminClientId,
	})
	addServiceAccount(serviceAccountData{
		Name:        "OSAC controller",
		Description: "Service account for the OSAC controller",
		ClientId:    controllerClientId,
		ClientRoles: map[string][]string{
			"realm-management": {
				"manage-realm",
				"manage-users",
				"view-realm",
				"view-users",
				"view-clients",
				"manage-identity-providers",
				"view-identity-providers",
			},
		},
	})

	// Add the prepared clients, groups and users to the values:
	valuesData["clients"] = clients
	valuesData["groups"] = groups
	valuesData["users"] = users

	// Write the values to a temporary file:
	valuesBytes, err := yaml.Marshal(valuesData)
	if err != nil {
		return fmt.Errorf("failed to marshal values to YAML: %w", err)
	}
	valuesFile := filepath.Join(t.tmpDir, "keycloak-values.yaml")
	err = os.WriteFile(valuesFile, valuesBytes, 0400)
	if err != nil {
		return fmt.Errorf("failed to write values to file: %w", err)
	}

	// Install the chart:
	installCmd, err := testing.NewCommand().
		SetLogger(t.logger).
		SetHome(t.projectDir).
		SetDir(t.projectDir).
		SetName(helmCmd).
		SetArgs(
			"upgrade",
			"--install",
			"keycloak",
			"it/charts/keycloak",
			"--kubeconfig", t.kcFile,
			"--namespace", "keycloak",
			"--create-namespace",
			"--values", valuesFile,
			"--wait",
		).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create Keycloak install command: %w", err)
	}
	if err = installCmd.Execute(ctx); err != nil {
		return fmt.Errorf("failed to install Keycloak: %w", err)
	}

	// Create a token source to connect to the Keycloak admin API:
	tokenStore, err := auth.NewMemoryTokenStore().
		SetLogger(t.logger).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create Keycloak admin token store: %w", err)
	}
	tokenSource, err := oauth.NewTokenSource().
		SetLogger(t.logger).
		SetStore(tokenStore).
		SetCaPool(t.caPool).
		SetIssuer(fmt.Sprintf("https://%s/realms/master", keycloakAddr)).
		SetFlow(oauth.PasswordFlow).
		SetClientId("admin-cli").
		SetUsername("admin").
		SetPassword(t.secret).
		SetScopes("openid").
		Build()
	if err != nil {
		return fmt.Errorf("failed to create Keycloak admin token source: %w", err)
	}

	// Create an HTTP client to connect to the Keycloak admin API:
	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: t.caPool,
			},
		},
	}

	// Helper to make authenticated requests to the Keycloak admin API:
	sendRequest := func(method, path string, input any) (code int, output []byte, err error) {
		var body io.Reader
		if input != nil {
			var data []byte
			data, err = json.Marshal(input)
			if err != nil {
				err = fmt.Errorf("failed to marshal request body: %w", err)
				return
			}
			body = bytes.NewReader(data)
		}
		url := fmt.Sprintf("https://%s/admin/realms/master%s", keycloakAddr, path)
		request, err := http.NewRequestWithContext(ctx, method, url, body)
		if err != nil {
			err = fmt.Errorf("failed to create request: %w", err)
			return
		}
		if input != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		var token *auth.Token
		token, err = tokenSource.Token(ctx)
		if err != nil {
			err = fmt.Errorf("failed to get token: %w", err)
			return
		}
		request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.Access))
		response, err := httpClient.Do(request)
		if err != nil {
			err = fmt.Errorf("failed to send request: %w", err)
			return
		}
		defer response.Body.Close()
		output, err = io.ReadAll(response.Body)
		if err != nil {
			err = fmt.Errorf("failed to read response body: %w", err)
			return
		}
		code = response.StatusCode
		return
	}

	// Create the 'admin' client in the master realm:
	var body []byte
	code, _, err := sendRequest(
		http.MethodPost,
		"/clients",
		map[string]any{
			"clientId":                  "admin",
			"name":                      "Administrator",
			"description":               "Administrator",
			"enabled":                   true,
			"clientAuthenticatorType":   "client-secret",
			"secret":                    t.secret,
			"serviceAccountsEnabled":    true,
			"publicClient":              false,
			"standardFlowEnabled":       false,
			"implicitFlowEnabled":       false,
			"directAccessGrantsEnabled": false,
			"protocol":                  "openid-connect",
			"fullScopeAllowed":          true,
		},
	)
	if err != nil {
		return err
	}
	if code != http.StatusCreated && code != http.StatusConflict {
		return fmt.Errorf("failed to create admin client in master realm: %d", code)
	}

	// Find the internal identifier of the client we just created:
	code, body, err = sendRequest(
		http.MethodGet,
		"/clients",
		nil,
	)
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return fmt.Errorf("failed to find admin client in master realm: %d", code)
	}
	var adminClientId string
	err = t.jqTool.EvaluateBytes(
		`.[] | select(.clientId == "admin") | .id`,
		body,
		&adminClientId,
	)
	if err != nil {
		return fmt.Errorf("failed to get admin client identifier: %w", err)
	}

	// Get the service account user for the client:
	code, body, err = sendRequest(
		http.MethodGet,
		fmt.Sprintf("/clients/%s/service-account-user", adminClientId),
		nil,
	)
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return fmt.Errorf("failed to get service account user: %d", code)
	}
	var adminUserId string
	err = t.jqTool.EvaluateBytes(`.id`, body, &adminUserId)
	if err != nil {
		return fmt.Errorf("failed to get service account user identifier: %w", err)
	}

	// Find the 'admin' realm role in the master realm:
	code, body, err = sendRequest(
		http.MethodGet,
		"/roles/admin",
		nil,
	)
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return fmt.Errorf("failed to find admin role: %d", code)
	}
	var adminRoleId string
	err = t.jqTool.EvaluateBytes(`.id`, body, &adminRoleId)
	if err != nil {
		return fmt.Errorf("failed to get admin role identifier: %w", err)
	}

	// Assign the 'admin' role to the service account user:
	code, _, err = sendRequest(
		http.MethodPost,
		fmt.Sprintf("/users/%s/role-mappings/realm", adminUserId),
		[]any{
			map[string]any{
				"id":   adminRoleId,
				"name": "admin",
			},
		},
	)
	if err != nil {
		return err
	}
	if code != http.StatusNoContent && code != http.StatusConflict {
		return fmt.Errorf("failed to assign admin role: %d", code)
	}

	t.logger.InfoContext(ctx, "Created admin service account")

	return nil
}

// deployPostgres installs the PostgreSQL chart with databases for Keycloak and the service.
func (t *Tool) deployPostgres(ctx context.Context) error {
	t.logger.DebugContext(ctx, "Installing PostgreSQL chart")

	// Prepare a map containing the values for the chart:
	valuesData := map[string]any{
		"certs": map[string]any{
			"issuerRef": map[string]any{
				"kind": "ClusterIssuer",
				"name": "default-ca",
			},
			"caBundle": map[string]any{
				"configMap": "ca-bundle",
			},
		},
		"databases": []any{
			map[string]any{
				"name": "keycloak",
				"user": "keycloak",
			},
			map[string]any{
				"name": "service",
				"user": "service",
			},
		},
	}
	valuesBytes, err := yaml.Marshal(valuesData)
	if err != nil {
		return fmt.Errorf("failed to marshal PostgreSQL values to YAML: %w", err)
	}

	// Write the values to a temporary file:
	valuesFile := filepath.Join(t.tmpDir, "postgres-values.yaml")
	err = os.WriteFile(valuesFile, valuesBytes, 0400)
	if err != nil {
		return fmt.Errorf("failed to write PostgreSQL values to file: %w", err)
	}

	// Install the chart:
	installCmd, err := testing.NewCommand().
		SetLogger(t.logger).
		SetHome(t.projectDir).
		SetDir(t.projectDir).
		SetName(helmCmd).
		SetArgs(
			"upgrade",
			"--install",
			"postgres",
			"it/charts/postgres",
			"--kubeconfig", t.kcFile,
			"--namespace", "postgres",
			"--create-namespace",
			"--values", valuesFile,
			"--wait",
		).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create PostgreSQL install command: %w", err)
	}
	if err = installCmd.Execute(ctx); err != nil {
		return fmt.Errorf("failed to install PostgreSQL: %w", err)
	}
	return nil
}

func (t *Tool) deployService(ctx context.Context, imageRef string) error {
	// Prepare the values:
	externalHostname, _, err := net.SplitHostPort(externalServiceAddr)
	if err != nil {
		return fmt.Errorf("failed to extract host from external service address: %w", err)
	}
	internalHostname, _, err := net.SplitHostPort(internalServiceAddr)
	if err != nil {
		return fmt.Errorf("failed to extract host from internal service address: %w", err)
	}
	valuesData := map[string]any{
		"variant":          "kind",
		"debug":            t.debug,
		"externalHostname": externalHostname,
		"internalHostname": internalHostname,
		"log": map[string]any{
			"level":   "debug",
			"headers": true,
			"bodies":  true,
		},
		"images": map[string]any{
			"service": imageRef,
		},
		"certs": map[string]any{
			"issuerRef": map[string]any{
				"kind": "ClusterIssuer",
				"name": "default-ca",
			},
			"caBundle": map[string]any{
				"configMap": "ca-bundle",
			},
		},
		"auth": map[string]any{
			"issuerUrl": fmt.Sprintf("https://%s/realms/osac", keycloakAddr),
			"controllerCredentials": []any{
				map[string]any{
					"secret": map[string]any{
						"name": controllerCredentialsSecret,
						"items": []any{
							map[string]any{
								"key":   "client-id",
								"param": "client-id",
							},
							map[string]any{
								"key":   "client-secret",
								"param": "client-secret",
							},
						},
					},
				},
			},
		},
		"database": map[string]any{
			"connection": []any{
				map[string]any{
					"configMap": map[string]any{
						"name": serviceDatabaseConfigMap,
						"items": []any{
							map[string]any{
								"key":   "url",
								"param": "url",
							},
							map[string]any{
								"key":   "sslmode",
								"param": "sslmode",
							},
						},
					},
				},
				map[string]any{
					"secret": map[string]any{
						"name": serviceDatabaseClientCertSecret,
						"items": []any{
							map[string]any{
								"key":   "tls.crt",
								"param": "sslcert",
							},
							map[string]any{
								"key":   "tls.key",
								"param": "sslkey",
							},
							map[string]any{
								"key":   "ca.crt",
								"param": "sslrootcert",
							},
						},
					},
				},
			},
		},
		"idp": map[string]any{
			"provider": "keycloak",
			"url":      fmt.Sprintf("https://%s", keycloakAddr),
			"credentials": []any{
				map[string]any{
					"secret": map[string]any{
						"name": controllerCredentialsSecret,
						"items": []any{
							map[string]any{
								"key":   "client-id",
								"param": "client-id",
							},
							map[string]any{
								"key":   "client-secret",
								"param": "client-secret",
							},
						},
					},
				},
			},
		},
	}
	valuesBytes, err := yaml.Marshal(valuesData)
	if err != nil {
		return fmt.Errorf("failed to marshal values to YAML: %w", err)
	}
	valuesFile := filepath.Join(t.tmpDir, "service-values.yaml")
	err = os.WriteFile(valuesFile, valuesBytes, 0400)
	if err != nil {
		return fmt.Errorf("failed to write values to file: %w", err)
	}
	t.logger.DebugContext(
		ctx,
		"Service chart values",
		slog.Any("values", valuesData),
	)

	// Deploy the service:
	t.logger.DebugContext(ctx, "Deploying service with Helm")
	installCmd, err := testing.NewCommand().
		SetLogger(t.logger).
		SetHome(t.projectDir).
		SetDir(t.projectDir).
		SetName(helmCmd).
		SetArgs(
			"upgrade",
			"--install",
			"fulfillment-service",
			"charts/service",
			"--kubeconfig", t.kcFile,
			"--namespace", "osac",
			"--create-namespace",
			"--values", valuesFile,
			"--wait",
		).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create service install command: %w", err)
	}
	if err = installCmd.Execute(ctx); err != nil {
		return fmt.Errorf("failed to install service: %w", err)
	}
	return nil
}

func (t *Tool) undeployService(ctx context.Context) error {
	t.logger.DebugContext(ctx, "Undeploying service with Helm")
	uninstallCmd, err := testing.NewCommand().
		SetLogger(t.logger).
		SetHome(t.projectDir).
		SetDir(t.projectDir).
		SetName(helmCmd).
		SetArgs(
			"uninstall",
			"fulfillment-service",
			"--kubeconfig", t.kcFile,
			"--namespace", "osac",
		).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create service uninstall command: %w", err)
	}
	if err = uninstallCmd.Execute(ctx); err != nil {
		return fmt.Errorf("failed to uninstall service: %w", err)
	}
	return nil
}

func (t *Tool) createClients(ctx context.Context) error {
	// Create token sources:
	emergencyTokenSource, err := t.makeKubernetesTokenSource(ctx, emergencyServiceAccount, "osac")
	if err != nil {
		return err
	}
	adminTokenSource, err := t.makeKeycloakTokenSource(ctx, adminUsername, adminsPassword)
	if err != nil {
		return err
	}
	userTokenSource, err := t.makeKeycloakTokenSource(ctx, userUsername, usersPassword)
	if err != nil {
		return err
	}

	// Create gRPC clients:
	t.internalView = &ToolView{}
	t.internalView.anonymousConn, err = t.makeGrpcConn(internalServiceAddr, nil)
	if err != nil {
		return err
	}
	t.internalView.emergencyConn, err = t.makeGrpcConn(internalServiceAddr, emergencyTokenSource)
	if err != nil {
		return err
	}
	t.internalView.adminConn, err = t.makeGrpcConn(internalServiceAddr, adminTokenSource)
	if err != nil {
		return err
	}
	t.internalView.userConn, err = t.makeGrpcConn(internalServiceAddr, userTokenSource)
	if err != nil {
		return err
	}
	t.externalView = &ToolView{}
	t.externalView.anonymousConn, err = t.makeGrpcConn(externalServiceAddr, nil)
	if err != nil {
		return err
	}
	t.externalView.emergencyConn, err = t.makeGrpcConn(externalServiceAddr, emergencyTokenSource)
	if err != nil {
		return err
	}
	t.externalView.adminConn, err = t.makeGrpcConn(externalServiceAddr, adminTokenSource)
	if err != nil {
		return err
	}
	t.externalView.userConn, err = t.makeGrpcConn(externalServiceAddr, userTokenSource)
	if err != nil {
		return err
	}

	// Create HTTP clients:
	t.internalView.anonymousClient = t.makeHttpClient(internalServiceAddr, nil)
	t.internalView.emergencyClient = t.makeHttpClient(internalServiceAddr, emergencyTokenSource)
	t.internalView.adminClient = t.makeHttpClient(internalServiceAddr, adminTokenSource)
	t.internalView.userClient = t.makeHttpClient(internalServiceAddr, userTokenSource)
	t.externalView.anonymousClient = t.makeHttpClient(externalServiceAddr, nil)
	t.externalView.emergencyClient = t.makeHttpClient(externalServiceAddr, emergencyTokenSource)
	t.externalView.adminClient = t.makeHttpClient(externalServiceAddr, adminTokenSource)
	t.externalView.userClient = t.makeHttpClient(externalServiceAddr, userTokenSource)

	return nil
}

// createTenants creates the tenants that are used by the tests.
func (t *Tool) createTenants(ctx context.Context) error {
	// Currently we map Keycloak groups to tenants, so we need to have a tenant for each group. In the tests we only
	// have two tenants, one for regular users and one for system administrators. System administrators belong to
	// the 'system' tenant, which is built-in and doesn't need to be explicitly created, so we only need to create
	// the tenant for regular users. Tests may create additional tenants as needed.
	uniqueTenants := make(map[string]bool)
	uniqueTenants[usersGroup] = true
	for _, tenants := range OIDCTenants {
		for _, tenant := range tenants {
			uniqueTenants[tenant] = true
		}
	}
	tenantsClient := privatev1.NewTenantsClient(t.internalView.adminConn)
	for tenant := range uniqueTenants {
		_, err := tenantsClient.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   tenant,
					Tenant: tenant,
				}.Build(),
			}.Build(),
		}.Build())
		status, ok := grpcstatus.FromError(err)
		if ok && status.Code() == grpccodes.AlreadyExists {
			continue
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// addUsersToKeycloakOrganizations adds test users to their corresponding tenant (Keycloak organization)
// so that the organization claim is included in their JWT tokens.
func (t *Tool) addUsersToKeycloakOrganizations(ctx context.Context) error {
	// Build a map of tenant name to list of users
	tenantUsers := make(map[string][]string)
	tenantUsers[usersGroup] = []string{userUsername}
	for user, tenants := range OIDCTenants {
		for _, tenant := range tenants {
			tenantUsers[tenant] = append(tenantUsers[tenant], user)
		}
	}

	// For each tenant, get its Keycloak Organization ID and add all users to it
	for tenantName, users := range tenantUsers {
		// Wait for the tenant to be synced to Keycloak by the controller
		var tenantId string
		backOff := backoff.NewExponentialBackOff()
		backOff.InitialInterval = 1 * time.Second
		backOff.MaxInterval = 10 * time.Second
		backOff.MaxElapsedTime = 60 * time.Second
		err := backoff.Retry(func() error {
			query := url.Values{}
			query.Set("exact", "true")
			query.Set("search", tenantName)
			code, body, err := t.KeycloakAdminRequest(ctx, http.MethodGet,
				"/organizations?"+query.Encode(), nil)
			if err != nil {
				return fmt.Errorf("failed to get tenant '%s': %w", tenantName, err)
			}
			if code != http.StatusOK {
				return fmt.Errorf("failed to get tenant '%s': status=%d body=%s", tenantName, code, string(body))
			}

			var orgs []map[string]any
			if err := json.Unmarshal(body, &orgs); err != nil {
				return fmt.Errorf("failed to parse organizations response: %w", err)
			}
			if len(orgs) == 0 {
				t.logger.DebugContext(
					ctx,
					"Tenant not yet synced to Keycloak, will retry",
					"tenant", tenantName,
				)
				return fmt.Errorf("tenant '%s' not found in Keycloak", tenantName)
			}

			id, ok := orgs[0]["id"].(string)
			if !ok {
				return fmt.Errorf("tenant '%s' has no id", tenantName)
			}
			tenantId = id
			return nil
		}, backoff.WithContext(backOff, ctx))
		if err != nil {
			return err
		}

		// Add each user to this tenant
		for _, username := range users {
			// Get user ID by username
			query := url.Values{}
			query.Set("username", username)
			query.Set("exact", "true")
			code, body, err := t.KeycloakAdminRequest(ctx, http.MethodGet,
				"/users?"+query.Encode(), nil)
			if err != nil {
				return fmt.Errorf("failed to get user '%s': %w", username, err)
			}
			if code != http.StatusOK {
				return fmt.Errorf("failed to get user '%s': status=%d body=%s", username, code, string(body))
			}

			var usersResult []map[string]any
			if err := json.Unmarshal(body, &usersResult); err != nil {
				return fmt.Errorf("failed to parse users response: %w", err)
			}
			if len(usersResult) == 0 {
				return fmt.Errorf("user '%s' not found in Keycloak", username)
			}

			userId, ok := usersResult[0]["id"].(string)
			if !ok {
				return fmt.Errorf("user '%s' has no id", username)
			}

			// Add user to organization
			code, body, err = t.KeycloakAdminRequest(ctx, http.MethodPost,
				fmt.Sprintf("/organizations/%s/members", tenantId), userId)
			if err != nil {
				return fmt.Errorf("failed to add user '%s' to tenant '%s': %w", username, tenantName, err)
			}
			// 201 Created, 204 No Content, or 409 Conflict (already a member) are all acceptable
			if code != http.StatusCreated && code != http.StatusNoContent && code != http.StatusConflict {
				return fmt.Errorf("failed to add user '%s' to organization '%s': status=%d body=%s",
					username, tenantName, code, string(body))
			}

			t.logger.InfoContext(ctx, "Added user to Keycloak tenant",
				"!user", username, "!tenant", tenantName)
		}

		// Create a default group in the tenant and add all users to it.
		// This is required for the oidc-tenant-group-membership-mapper to include
		// the tenant in the JWT token's tenant claim.
		defaultGroupName := "/members"
		groupPayload := map[string]any{
			"name": defaultGroupName,
		}
		code, body, err := t.KeycloakAdminRequest(ctx, http.MethodPost,
			fmt.Sprintf("/organizations/%s/groups", tenantId), groupPayload)
		if err != nil {
			return fmt.Errorf("failed to create group '%s' in tenant '%s': %w",
				defaultGroupName, tenantName, err)
		}
		// 201 Created or 409 Conflict (already exists) are acceptable
		if code != http.StatusCreated && code != http.StatusConflict {
			return fmt.Errorf("failed to create group '%s' in tenant '%s': status=%d body=%s",
				defaultGroupName, tenantName, code, string(body))
		}

		// Get the group ID
		var groupId string
		if code == http.StatusCreated {
			// Parse the created group response to get the ID
			var groupResp map[string]any
			if err := json.Unmarshal(body, &groupResp); err != nil {
				return fmt.Errorf("failed to parse group creation response: %w", err)
			}
			groupId, _ = groupResp["id"].(string)
		}

		if groupId == "" {
			// Group already existed, need to fetch it
			code, body, err = t.KeycloakAdminRequest(ctx, http.MethodGet,
				fmt.Sprintf("/organizations/%s/groups", tenantId), nil)
			if err != nil {
				return fmt.Errorf("failed to get groups for tenant '%s': %w", tenantName, err)
			}
			if code != http.StatusOK {
				return fmt.Errorf("failed to get groups for tenant '%s': status=%d body=%s",
					tenantName, code, string(body))
			}

			var groups []map[string]any
			if err := json.Unmarshal(body, &groups); err != nil {
				return fmt.Errorf("failed to parse groups response: %w", err)
			}

			for _, g := range groups {
				if name, ok := g["name"].(string); ok && name == defaultGroupName {
					groupId, _ = g["id"].(string)
					break
				}
			}

			if groupId == "" {
				return fmt.Errorf("failed to find group '%s' in tenant '%s'",
					defaultGroupName, tenantName)
			}
		}

		// Add all users in this organization to the default group
		for _, username := range users {
			// Get user ID by username
			query := url.Values{}
			query.Set("username", username)
			query.Set("exact", "true")
			code, body, err = t.KeycloakAdminRequest(ctx, http.MethodGet,
				"/users?"+query.Encode(), nil)
			if err != nil {
				return fmt.Errorf("failed to get user '%s': %w", username, err)
			}
			if code != http.StatusOK {
				return fmt.Errorf("failed to get user '%s': status=%d body=%s",
					username, code, string(body))
			}

			var usersResult []map[string]any
			if err := json.Unmarshal(body, &usersResult); err != nil {
				return fmt.Errorf("failed to parse users response: %w", err)
			}
			if len(usersResult) == 0 {
				return fmt.Errorf("user '%s' not found in Keycloak", username)
			}

			userId, ok := usersResult[0]["id"].(string)
			if !ok {
				return fmt.Errorf("user '%s' has no id", username)
			}

			// Add user to the group
			code, body, err = t.KeycloakAdminRequest(ctx, http.MethodPut,
				fmt.Sprintf("/organizations/%s/groups/%s/members/%s", tenantId, groupId, userId), nil)
			if err != nil {
				return fmt.Errorf("failed to add user '%s' to group '%s' in tenant '%s': %w",
					username, defaultGroupName, tenantName, err)
			}
			// 200 OK, 201 Created, 204 No Content, or 409 Conflict (already a member) are all acceptable
			if code != http.StatusOK && code != http.StatusCreated && code != http.StatusNoContent && code != http.StatusConflict {
				return fmt.Errorf("failed to add user '%s' to group '%s' in tenant '%s': status=%d body=%s",
					username, defaultGroupName, tenantName, code, string(body))
			}

			t.logger.InfoContext(ctx, "Added user to tenant group",
				"!user", username, "!tenant", tenantName, "group", defaultGroupName)
		}
	}

	return nil
}

func (t *Tool) createUserServiceAccounts(ctx context.Context) error {
	var tenantNamespaces []string
	for user, group := range ServiceAccountTenants {
		if !slices.Contains(tenantNamespaces, group) {
			err := t.kubeClient.Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: group,
				},
			})

			if err != nil && !apierrors.IsAlreadyExists(err) {
				return fmt.Errorf("failed to create namespace '%s': %w", group, err)
			}

			tenantNamespaces = append(tenantNamespaces, group)
		}
		err := t.kubeClient.Create(ctx, &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      user,
				Namespace: group,
			},
		})
		if err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create service account '%s': %w", user, err)
		}
	}
	return nil
}

func (t *Tool) makeKubernetesTokenSource(ctx context.Context, sa, namespace string) (result auth.TokenSource, err error) {
	response, err := t.kubeClientSet.CoreV1().ServiceAccounts(namespace).CreateToken(
		ctx,
		sa,
		&authenticationv1.TokenRequest{
			Spec: authenticationv1.TokenRequestSpec{
				ExpirationSeconds: new(int64(3600)),
			},
		},
		metav1.CreateOptions{},
	)
	if err != nil {
		err = fmt.Errorf("failed to create token for service account '%s': %w", sa, err)
		return
	}
	token := &auth.Token{
		Access: response.Status.Token,
	}
	result, err = auth.NewStaticTokenSource().
		SetLogger(t.logger).
		SetToken(token).
		Build()
	return
}

func (t *Tool) makeKeycloakTokenSource(ctx context.Context, username, password string) (result auth.TokenSource, err error) {
	store, err := auth.NewMemoryTokenStore().
		SetLogger(t.logger).
		Build()
	if err != nil {
		return
	}
	result, err = oauth.NewTokenSource().
		SetLogger(t.logger).
		SetStore(store).
		SetCaPool(t.caPool).
		SetIssuer(fmt.Sprintf("https://%s/realms/osac", keycloakAddr)).
		SetFlow(oauth.PasswordFlow).
		SetClientId("osac-cli").
		SetUsername(username).
		SetPassword(password).
		SetScopes("openid", "organization").
		Build()
	return
}

// KeycloakAdminRequest makes an authenticated request to the Keycloak admin API for the 'osac' realm.
// The path is relative to /admin/realms/osac (e.g., "/organizations", "/users/{id}").
func (t *Tool) KeycloakAdminRequest(ctx context.Context, method, path string, input any) (
	code int, output []byte, err error,
) {
	store, err := auth.NewMemoryTokenStore().
		SetLogger(t.logger).
		Build()
	if err != nil {
		err = fmt.Errorf("failed to create Keycloak admin token store: %w", err)
		return
	}
	tokenSource, err := oauth.NewTokenSource().
		SetLogger(t.logger).
		SetStore(store).
		SetCaPool(t.caPool).
		SetIssuer(fmt.Sprintf("https://%s/realms/master", keycloakAddr)).
		SetFlow(oauth.PasswordFlow).
		SetClientId("admin-cli").
		SetUsername("admin").
		SetPassword(t.secret).
		SetScopes("openid").
		Build()
	if err != nil {
		err = fmt.Errorf("failed to create Keycloak admin token source: %w", err)
		return
	}
	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:    t.caPool,
				MinVersion: tls.VersionTLS12,
			},
		},
	}
	var body io.Reader
	if input != nil {
		var data []byte
		data, err = json.Marshal(input)
		if err != nil {
			err = fmt.Errorf("failed to marshal request body: %w", err)
			return
		}
		body = bytes.NewReader(data)
	}
	url := fmt.Sprintf("https://%s/admin/realms/osac%s", keycloakAddr, path)
	request, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		err = fmt.Errorf("failed to create request: %w", err)
		return
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	token, err := tokenSource.Token(ctx)
	if err != nil {
		err = fmt.Errorf("failed to get token: %w", err)
		return
	}
	request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.Access))
	response, err := httpClient.Do(request)
	if err != nil {
		err = fmt.Errorf("failed to send request: %w", err)
		return
	}
	defer response.Body.Close()
	output, err = io.ReadAll(response.Body)
	if err != nil {
		err = fmt.Errorf("failed to read response body: %w", err)
		return
	}
	code = response.StatusCode
	return
}

// makeGrpcConn creates a gRPC connection that automatically adds the token to the request.
func (t *Tool) makeGrpcConn(addr string, tokenSource auth.TokenSource) (result *grpc.ClientConn, err error) {
	userAgent := fmt.Sprintf("%s/%s", userAgent, version.Get())
	result, err = network.NewGrpcClient().
		SetLogger(t.logger).
		SetCaPool(t.caPool).
		SetAddress(addr).
		SetTokenSource(tokenSource).
		SetUserAgent(userAgent).
		Build()
	return
}

// makeHttpClient creates an HTTP client that automatically adds the scheme, host and token to the request. Users of the
// client only need to provide the URL path, and other headers as needed.
func (t *Tool) makeHttpClient(addr string, tokenSource auth.TokenSource) *http.Client {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			RootCAs: t.caPool,
		},
	}
	tripper := ghttp.RoundTripperFunc(
		func(request *http.Request) (response *http.Response, err error) {
			// Replace the scheme and host, so that users of the client only need to provide the path:
			request.URL.Scheme = "https"
			request.URL.Host = addr

			// Add the token if there is a token source available:
			if tokenSource != nil {
				token, err := tokenSource.Token(request.Context())
				if err != nil {
					return nil, err
				}
				request.Header.Set(
					"Authorization",
					fmt.Sprintf("Bearer %s", token.Access),
				)
			}

			// Forward the request:
			response, err = transport.RoundTrip(request)
			return
		},
	)
	return &http.Client{
		Transport: tripper,
	}
}

func (t *Tool) waitForServersReady(ctx context.Context) error {
	err := t.waitForGrpcServerReady(ctx)
	if err != nil {
		return err
	}
	return t.waitForRestGatewayReady(ctx)
}

func (t *Tool) waitForGrpcServerReady(ctx context.Context) error {
	t.logger.DebugContext(ctx, "Waiting for gRPC server to be ready")
	client := publicv1.NewCapabilitiesClient(t.externalView.adminConn)
	request := publicv1.CapabilitiesGetRequest_builder{}.Build()
	backOff := backoff.NewExponentialBackOff()
	backOff.InitialInterval = 1 * time.Second
	backOff.MaxInterval = 10 * time.Second
	backOff.MaxElapsedTime = 60 * time.Second
	return backoff.Retry(func() error {
		_, err := client.Get(ctx, request)
		if err != nil {
			t.logger.DebugContext(
				ctx,
				"gRPC server not ready yet, will retry",
				slog.Any("error", err),
			)
		}
		return err
	}, backoff.WithContext(backOff, ctx))
}

func (t *Tool) waitForRestGatewayReady(ctx context.Context) error {
	t.logger.DebugContext(ctx, "Waiting for REST gateway to be ready")
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		"/api/fulfillment/v1/capabilities",
		nil,
	)
	if err != nil {
		return fmt.Errorf("failed to create REST health check request: %w", err)
	}
	backOff := backoff.NewExponentialBackOff()
	backOff.InitialInterval = 1 * time.Second
	backOff.MaxInterval = 10 * time.Second
	backOff.MaxElapsedTime = 60 * time.Second
	return backoff.Retry(func() error {
		response, err := t.externalView.adminClient.Do(request)
		if err != nil {
			t.logger.DebugContext(
				ctx,
				"REST gateway not ready yet, will retry",
				slog.Any("error", err),
			)
			return err
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			err = fmt.Errorf("REST gateway returned status %d", response.StatusCode)
			t.logger.DebugContext(
				ctx,
				"REST gateway not ready yet, will retry",
				slog.Any("error", err),
			)
			return err
		}
		return nil
	}, backoff.WithContext(backOff, ctx))
}

func (t *Tool) createHubNamespace(ctx context.Context) error {
	t.logger.DebugContext(ctx, "Creating hub namespace")
	hubNamespaceObject := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: hubNamespace,
		},
	}
	err := t.kubeClient.Create(ctx, hubNamespaceObject)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create hub namespace: %w", err)
	}
	return nil
}

func (t *Tool) registerHub(ctx context.Context) error {
	t.logger.DebugContext(ctx, "Registering hub")

	// Prepare the kubeconfig for the hub:
	hubKcBytes := t.cluster.Kubeconfig()
	hubKcObject, err := clientcmd.Load(hubKcBytes)
	if err != nil {
		return fmt.Errorf("failed to load hub kubeconfig: %w", err)
	}
	for clusterKey := range hubKcObject.Clusters {
		hubKcObject.Clusters[clusterKey].Server = "https://kubernetes.default.svc"
	}
	hubKcBytes, err = clientcmd.Write(*hubKcObject)
	if err != nil {
		return fmt.Errorf("failed to write hub Kc: %w", err)
	}

	// Create the hubs client:
	hubsClient := privatev1.NewHubsClient(t.internalView.adminConn)

	// Wait for the API to be ready:
	for range 30 {
		_, err = hubsClient.List(ctx, privatev1.HubsListRequest_builder{}.Build())
		if err == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		return fmt.Errorf("API not ready after waiting: %w", err)
	}

	// Create the hub:
	_, err = hubsClient.Create(ctx, privatev1.HubsCreateRequest_builder{
		Object: privatev1.Hub_builder{
			Id: hubId,
			Spec: privatev1.HubSpec_builder{
				Kubeconfig: hubKcBytes,
				Namespace:  hubNamespace,
			}.Build(),
		}.Build(),
	}.Build())
	if err != nil {
		status, ok := grpcstatus.FromError(err)
		if ok && status.Code() == grpccodes.AlreadyExists {
			return nil
		}
		return fmt.Errorf("failed to create hub: %w", err)
	}
	return nil
}

// buildCLI builds the osac CLI binary and stores its path for use in CLI integration tests.
func (t *Tool) buildCLI(ctx context.Context) error {
	t.logger.DebugContext(ctx, "Building CLI binary")
	t.cliBinaryPath = filepath.Join(t.tmpDir, "osac")
	buildCmd, err := testing.NewCommand().
		SetLogger(t.logger).
		SetHome(t.projectDir).
		SetDir(t.projectDir).
		SetName("go").
		SetArgs("build", "-o", t.cliBinaryPath, "./cmd/osac").
		Build()
	if err != nil {
		return fmt.Errorf("failed to create command to build CLI: %w", err)
	}
	err = buildCmd.Execute(ctx)
	if err != nil {
		return fmt.Errorf("failed to build CLI binary: %w", err)
	}
	return nil
}

// CLIBinaryPath returns the path to the built osac CLI binary.
func (t *Tool) CLIBinaryPath() string {
	return t.cliBinaryPath
}

// Secret returns the shared secret used for passwords and credentials in the test environment.
func (t *Tool) Secret() string {
	return t.secret
}

// NewCLIHomeDir creates an isolated temporary directory suitable for use as a HOME directory
// during CLI tests. Each test should call this to get credential isolation. The caller is
// responsible for cleaning up the directory (typically via DeferCleanup).
func (t *Tool) NewCLIHomeDir() (string, error) {
	return os.MkdirTemp("", "*.cli-home")
}

// RunCLI executes the osac CLI binary with the given arguments and a custom HOME directory
// for credential isolation. Returns stdout, stderr, and the process exit code.
func (t *Tool) RunCLI(ctx context.Context, homeDir string, args ...string) (stdout, stderr string, exitCode int) {
	return t.runCLI(ctx, homeDir, nil, args...)
}

// RunCLIWithEnv executes the osac binary with additional environment variables beyond the HOME
// override. Each entry in extraEnv should be in "KEY=VALUE" format. Use "KEY=" to unset a variable.
func (t *Tool) RunCLIWithEnv(ctx context.Context, homeDir string, extraEnv []string, args ...string) (stdout, stderr string, exitCode int) {
	return t.runCLI(ctx, homeDir, extraEnv, args...)
}

// runCLI is the shared implementation for RunCLI and RunCLIWithEnv. We intentionally use
// exec.CommandContext directly rather than testing.Command because the CLI tests need custom
// environment sandboxing and explicit exit-code extraction for non-zero exits (expected
// behavior, not errors).
func (t *Tool) runCLI(ctx context.Context, homeDir string, extraEnv []string, args ...string) (stdout, stderr string, exitCode int) {
	cmd := exec.CommandContext(ctx, t.cliBinaryPath, args...)
	cmd.Env = append(cliEnv(homeDir), extraEnv...)
	cmd.Dir = t.projectDir
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	exitCode = 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
			t.logger.ErrorContext(ctx, "CLI command failed with unexpected error",
				slog.String("binary", t.cliBinaryPath),
				slog.Any("args", redactCLIArgs(args)),
				slog.String("error", err.Error()),
			)
		}
	}
	t.logger.DebugContext(ctx, "CLI command completed",
		slog.String("binary", t.cliBinaryPath),
		slog.Any("args", redactCLIArgs(args)),
		slog.Int("code", exitCode),
	)
	return outBuf.String(), errBuf.String(), exitCode
}

// cliEnv builds a minimal sandboxed environment for CLI subprocess execution. Only the
// variables strictly required by the CLI binary are set; everything else from the host
// is excluded to guarantee full test isolation.
func cliEnv(homeDir string) []string {
	return []string{
		"HOME=" + homeDir,
		"PATH=" + os.Getenv("PATH"),
		"OSAC_CONFIG=" + filepath.Join(homeDir, ".config", "osac"),
		"OSAC_CACHE=" + filepath.Join(homeDir, ".cache", "osac"),
	}
}

// redactCLIArgs returns a copy of args with sensitive flag values masked.
func redactCLIArgs(args []string) []string {
	out := make([]string, len(args))
	copy(out, args)
	for i, a := range out {
		switch {
		case strings.HasPrefix(a, "--password="):
			out[i] = "--password=<redacted>"
		case strings.HasPrefix(a, "--client-secret="):
			out[i] = "--client-secret=<redacted>"
		}
	}
	return out
}

// LoginCLI authenticates the CLI using the password flow against the external API with the
// given user credentials. The homeDir parameter provides credential isolation between tests.
func (t *Tool) LoginCLI(ctx context.Context, homeDir, user, password string) (stdout, stderr string, exitCode int) {
	return t.RunCLI(ctx, homeDir,
		"login", fmt.Sprintf("https://%s", externalServiceAddr),
		"--flow=password",
		"--user="+user,
		"--password="+password,
		"--insecure",
	)
}

func (t *Tool) Cleanup(ctx context.Context) error {
	var errs []error

	// Close gRPC views:
	if t.internalView != nil {
		err := t.internalView.Close()
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to close internal gRPC view: %w", err))
		}
	}
	if t.externalView != nil {
		err := t.externalView.Close()
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to close external gRPC view: %w", err))
		}
	}

	// Dump the logs:
	if t.cluster != nil && !t.keepKind {
		err := t.cluster.Dump(ctx, filepath.Join(t.projectDir, "logs"))
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to dump cluster logs: %w", err))
		}
	}

	// Undeploy the service:
	if t.cluster != nil && t.keepKind && !t.keepService {
		err := t.undeployService(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to undeploy service: %w", err))
		}
	}

	// Stop the cluster:
	if t.cluster != nil && !t.keepKind {
		err := t.cluster.Stop(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to stop cluster: %w", err))
		}
	}

	// Remove temporary directory:
	if t.tmpDir != "" {
		err := os.RemoveAll(t.tmpDir)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to remove temporary directory: %w", err))
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func (t *Tool) Dump(ctx context.Context) error {
	if t.cluster == nil {
		return nil
	}
	logsDir := filepath.Join(t.projectDir, "logs")
	return t.cluster.Dump(ctx, logsDir)
}

// Cluster returns the Kind cluster.
func (t *Tool) Cluster() *testing.Kind {
	return t.cluster
}

// KubeClient returns the Kubernetes client.
func (t *Tool) KubeClient() crclient.Client {
	return t.kubeClient
}

// KubeClientSet returns the Kubernetes clientset.
func (t *Tool) KubeClientSet() *kubernetes.Clientset {
	return t.kubeClientSet
}

// InternalView returns the view of the internal API.
func (t *Tool) InternalView() *ToolView {
	return t.internalView
}

// ExternalView returns the view of the external API.
func (t *Tool) ExternalView() *ToolView {
	return t.externalView
}

// AnonymousConn returns the gRPC connection for the anonymous user.
func (v *ToolView) AnonymousConn() *grpc.ClientConn {
	return v.anonymousConn
}

// EmergencyConn returns the gRPC connection for the emergency administration service account.
func (v *ToolView) EmergencyConn() *grpc.ClientConn {
	return v.emergencyConn
}

// AdminConn returns the gRPC connection for the administrator user.
func (v *ToolView) AdminConn() *grpc.ClientConn {
	return v.adminConn
}

// UserConn returns the gRPC connection for the regular user.
func (v *ToolView) UserConn() *grpc.ClientConn {
	return v.userConn
}

// AnonymousClient returns the HTTP client for the anonymous user.
func (v *ToolView) AnonymousClient() *http.Client {
	return v.anonymousClient
}

// EmergencyClient returns the HTTP client for the emergency administration service account.
func (v *ToolView) EmergencyClient() *http.Client {
	return v.emergencyClient
}

// AdminClient returns the HTTP client for the administrator user.
func (v *ToolView) AdminClient() *http.Client {
	return v.adminClient
}

// UserClient returns the HTTP client for the regular user.
func (v *ToolView) UserClient() *http.Client {
	return v.userClient
}

// Close closes the gRPC connections and HTTP clients of the view.
func (v *ToolView) Close() error {
	closeConn := func(conn *grpc.ClientConn) error {
		if conn != nil {
			return conn.Close()
		}
		return nil
	}
	closeClient := func(client *http.Client) error {
		return nil
	}
	return errors.Join(
		closeConn(v.anonymousConn),
		closeConn(v.emergencyConn),
		closeConn(v.adminConn),
		closeConn(v.userConn),
		closeClient(v.anonymousClient),
		closeClient(v.emergencyClient),
		closeClient(v.adminClient),
		closeClient(v.userClient),
	)
}

// ProjectDir returns the project directory.
func (t *Tool) ProjectDir() string {
	return t.projectDir
}

// Names of the command line tools:
const (
	helmCmd    = "helm"
	kubectlCmd = "kubectl"
	podmanCmd  = "podman"
)

// Name and namespace of the hub:
const hubId = "local"
const hubNamespace = "osac-operator-system"

// Image details:
const imageName = "ghcr.io/osac/fulfillment-service"

// userAgent is the user agent string for the integration test tool.
const userAgent = "fulfillment-it-tool"

// Service host name and address:
const (
	keycloakAddr        = "keycloak.keycloak.svc.cluster.local:8000"
	externalServiceAddr = "fulfillment-api.osac.svc.cluster.local:8000"
	internalServiceAddr = "fulfillment-internal-api.osac.svc.cluster.local:8000"
)

// Names of the database-related Kubernetes resources.
const (
	keycloakDatabaseClientCertSecret = "keycloak-database-client-cert"
	keycloakDatabaseConfigMap        = "keycloak-database-config"
	serviceDatabaseClientCertSecret  = "fulfillment-database-client-cert"
	serviceDatabaseConfigMap         = "fulfillment-database-config"
)

// Namespace, name and key of the Kubernetes secret that contains the random secret used for passwords and credentials.
const (
	randomSecretNamespace = "default"
	randomSecretName      = "random"
	randomSecretKey       = "secret"
)

// Name of the Kubernetes secret that contains the OAuth client credentials that the controller uses to authenticate to
// the API.
const controllerCredentialsSecret = "fulfillment-controller-credentials"

// Name of the Kubernetes service account that is used for emergency administration access.
const emergencyServiceAccount = "admin"

// Details of the Keycloak administrator user:
const (
	adminUsername  = "admin"
	adminsPassword = "password"
)

// Details of the Keycloak regular user:
const (
	userUsername  = "user"
	usersPassword = "password"
	usersGroup    = "users"
	adminsGroup   = "admins"
)

// Details of the Keycloak service accounts:
const (
	adminClientId      = "osac-admin"
	controllerClientId = "osac-controller"
)

// ExtractOrganizationNames extracts organization names from a JWT organization claim.
// The claim can be in two formats:
// - Array format: ["org1", "org2"]
// - Object format with groups: {"org1": {"groups": [...]}, "org2": {"groups": [...]}}
func ExtractOrganizationNames(orgClaim any) ([]string, error) {
	switch v := orgClaim.(type) {
	case []any:
		// Simple array format: ["org-name"]
		var orgNames []string
		for _, o := range v {
			if s, ok := o.(string); ok {
				orgNames = append(orgNames, s)
			}
		}
		return orgNames, nil
	case map[string]any:
		// Object format with groups: {"org-name": {"groups": [...]}}
		var orgNames []string
		for orgName := range v {
			orgNames = append(orgNames, orgName)
		}
		return orgNames, nil
	default:
		return nil, fmt.Errorf("organization claim should be an array or object, got %T", orgClaim)
	}
}
