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
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/jq"
	"github.com/osac-project/osac/fulfillment-service/internal/network"
	"github.com/osac-project/osac/fulfillment-service/internal/oauth"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	bmfov1alpha1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/version"
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
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
	logger     *slog.Logger
	projectDir string
	secret     string
}

// Tool is an instance of the integration test tool that sets up the test environment. Don't create instances of this
// directly, use the NewTool function instead.
type Tool struct {
	logger        *slog.Logger
	projectDir    string
	tmpDir        string
	clusterName   string
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

// SetSecret sets the secret used in all places where passwords or secrets are needed, such as service account client
// secrets and user passwords. If not set then a random one will be generated.
func (b *ToolBuilder) SetSecret(value string) *ToolBuilder {
	b.secret = value
	return b
}

// Build uses the data stored in the builder to create a new instance of the integration test tool.
func (b *ToolBuilder) Build() (result *Tool, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
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
		logger:     b.logger,
		projectDir: projectDir,
		secret:     b.secret,
		jqTool:     jqTool,
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

// Setup prepares the integration test environment. Assumes a pre-existing Kind cluster with
// infrastructure (cert-manager, Keycloak, Envoy Gateway) and OSAC services already deployed
// via `make dev-env`. Creates test users, tenants, service accounts, and gRPC/HTTP clients.
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

	// Build the CLI binary:
	err = t.buildCLI(ctx)
	if err != nil {
		return err
	}

	// Connect to the pre-existing Kind cluster:
	t.clusterName = os.Getenv("KIND_CLUSTER")
	if t.clusterName == "" {
		t.clusterName = "osac-dev"
	}
	t.kcFile = os.Getenv("KUBECONFIG")
	if t.kcFile == "" {
		t.kcFile = filepath.Join(os.Getenv("HOME"), ".kube", t.clusterName+"-kind.kubeconfig")
	}

	// Create the Kubernetes clients from the kubeconfig:
	restConfig, err := clientcmd.BuildConfigFromFlags("", t.kcFile)
	if err != nil {
		return fmt.Errorf("failed to build rest config from kubeconfig: %w", err)
	}
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(osacv1alpha1.AddToScheme(scheme))
	utilruntime.Must(bmfov1alpha1.AddToScheme(scheme))
	t.kubeClient, err = crclient.New(restConfig, crclient.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}
	t.kubeClientSet, err = kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}

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

	// Create test users in Keycloak and set up admin org membership before
	// creating clients — the admin token source needs admin to be in an org
	// for the password flow to succeed:
	err = t.createKeycloakTestUsers(ctx)
	if err != nil {
		return err
	}
	err = t.ensureUsersInKeycloakOrganizations(ctx)
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

	// The controller's initial sync loop processes tenants asynchronously —
	// IDP org creation and break-glass credential setup must complete before
	// tests can create their own tenants with a 60s timeout.
	err = t.waitForTenantsSynced(ctx)
	if err != nil {
		return err
	}

	// Add users to Keycloak Organizations (tenant orgs created by the controller):
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
	}
	for _, command := range commands {
		_, err := exec.LookPath(command)
		if err != nil {
			return fmt.Errorf("command '%s' is not available: %w", command, err)
		}
	}
	return nil
}

func (t *Tool) runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = t.projectDir
	t.logger.DebugContext(ctx, "Running command", "command", name, "args", args)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%s %s failed: %w\n%s", name, strings.Join(args, " "), err, output)
	}
	return output, nil
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
		Namespace: "osac",
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

func (t *Tool) waitForTenantsSynced(ctx context.Context) error {
	t.logger.InfoContext(ctx, "Waiting for setup tenants to reach SYNCED")

	uniqueTenants := make(map[string]bool)
	uniqueTenants[usersGroup] = true
	for _, tenants := range OIDCTenants {
		for _, tenant := range tenants {
			uniqueTenants[tenant] = true
		}
	}

	tenantsClient := privatev1.NewTenantsClient(t.internalView.adminConn)
	for tenant := range uniqueTenants {
		bo := backoff.NewExponentialBackOff()
		bo.InitialInterval = 1 * time.Second
		bo.MaxInterval = 5 * time.Second
		bo.MaxElapsedTime = 120 * time.Second
		tenantName := tenant
		err := backoff.Retry(func() error {
			resp, getErr := tenantsClient.Get(ctx, privatev1.TenantsGetRequest_builder{
				Id: tenantName,
			}.Build())
			if getErr != nil {
				return fmt.Errorf("failed to get tenant %q: %w", tenantName, getErr)
			}
			if resp.GetObject().GetStatus().GetState() != privatev1.TenantState_TENANT_STATE_SYNCED {
				return fmt.Errorf("tenant %q not yet synced", tenantName)
			}
			return nil
		}, backoff.WithContext(bo, ctx))
		if err != nil {
			return fmt.Errorf("timed out waiting for tenant %q to reach SYNCED: %w", tenant, err)
		}
		t.logger.DebugContext(ctx, "Tenant synced", slog.String("tenant", tenant))
	}

	t.logger.InfoContext(ctx, "All setup tenants synced")
	return nil
}

// createKeycloakTestUsers creates the test users in Keycloak via the admin API. The prereqs chart
// deploys Keycloak with its own realm but does not include the test-specific users. This method
// creates them idempotently (409 Conflict is acceptable).
func (t *Tool) createKeycloakTestUsers(ctx context.Context) error {
	t.logger.DebugContext(ctx, "Creating test users in Keycloak")

	users := []string{adminUsername, userUsername}
	for user := range OIDCTenants {
		users = append(users, user)
	}

	for _, username := range users {
		payload := map[string]any{
			"username":      username,
			"enabled":       true,
			"email":         fmt.Sprintf("%s@test.local", username),
			"emailVerified": true,
			"firstName":     username,
			"lastName":      "Test",
			"credentials": []map[string]any{{
				"type":      "password",
				"value":     usersPassword,
				"temporary": false,
			}},
		}
		code, body, err := t.KeycloakAdminRequest(ctx, http.MethodPost, "/users", payload)
		if err != nil {
			return fmt.Errorf("failed to create user '%s': %w", username, err)
		}
		if code != http.StatusCreated && code != http.StatusConflict {
			return fmt.Errorf("failed to create user '%s': status=%d body=%s", username, code, string(body))
		}

		// Ensure emailVerified and password are set even if the user already existed
		userId, err := t.keycloakEnsureUserReady(ctx, username)
		if err != nil {
			return err
		}
		_ = userId

		t.logger.InfoContext(ctx, "Created Keycloak test user", "!user", username)
	}
	return nil
}

// keycloakEnsureUserReady updates a Keycloak user to have email, emailVerified, and resets
// their password. Needed because POST /users with 409 Conflict doesn't update attributes.
func (t *Tool) keycloakEnsureUserReady(ctx context.Context, username string) (string, error) {
	query := url.Values{}
	query.Set("username", username)
	query.Set("exact", "true")
	_, body, err := t.KeycloakAdminRequest(ctx, http.MethodGet, "/users?"+query.Encode(), nil)
	if err != nil {
		return "", fmt.Errorf("failed to get user '%s': %w", username, err)
	}
	var usersResult []map[string]any
	if err := json.Unmarshal(body, &usersResult); err != nil || len(usersResult) == 0 {
		return "", fmt.Errorf("user '%s' not found after creation", username)
	}
	userId := usersResult[0]["id"].(string)

	updatePayload := map[string]any{
		"email":         fmt.Sprintf("%s@test.local", username),
		"emailVerified": true,
		"enabled":       true,
	}
	code, body, err := t.KeycloakAdminRequest(ctx, http.MethodPut,
		fmt.Sprintf("/users/%s", userId), updatePayload)
	if err != nil {
		return "", fmt.Errorf("failed to update user '%s': %w", username, err)
	}
	if code != http.StatusOK && code != http.StatusNoContent {
		return "", fmt.Errorf("failed to update user '%s': status=%d body=%s", username, code, string(body))
	}

	credPayload := map[string]any{
		"type":      "password",
		"value":     usersPassword,
		"temporary": false,
	}
	code, body, err = t.KeycloakAdminRequest(ctx, http.MethodPut,
		fmt.Sprintf("/users/%s/reset-password", userId), credPayload)
	if err != nil {
		return "", fmt.Errorf("failed to reset password for '%s': %w", username, err)
	}
	if code != http.StatusOK && code != http.StatusNoContent {
		return "", fmt.Errorf("failed to reset password for '%s': status=%d body=%s", username, code, string(body))
	}

	return userId, nil
}

// ensureUsersInKeycloakOrganizations creates placeholder Keycloak organizations and adds
// the admin and user accounts to them with group membership. This must happen before
// creating gRPC/HTTP clients because the token sources use the password flow, which
// requires org membership in Keycloak 26.
func (t *Tool) ensureUsersInKeycloakOrganizations(ctx context.Context) error {
	t.logger.DebugContext(ctx, "Ensuring test users are in Keycloak organizations")

	userOrgs := map[string]string{
		adminUsername: "system",
		userUsername:  usersGroup,
	}
	for username, orgName := range userOrgs {
		if err := t.ensureUserInOrg(ctx, username, orgName); err != nil {
			return err
		}
	}
	return nil
}

func (t *Tool) ensureUserInOrg(ctx context.Context, username, orgName string) error {
	orgPayload := map[string]any{"name": orgName, "enabled": true}
	code, body, err := t.KeycloakAdminRequest(ctx, http.MethodPost, "/organizations", orgPayload)
	if err != nil {
		return fmt.Errorf("failed to create org '%s': %w", orgName, err)
	}
	if code != http.StatusCreated && code != http.StatusConflict {
		return fmt.Errorf("failed to create org '%s': status=%d body=%s", orgName, code, string(body))
	}

	query := url.Values{}
	query.Set("exact", "true")
	query.Set("search", orgName)
	_, body, err = t.KeycloakAdminRequest(ctx, http.MethodGet, "/organizations?"+query.Encode(), nil)
	if err != nil {
		return fmt.Errorf("failed to get org '%s': %w", orgName, err)
	}
	var orgs []map[string]any
	if err := json.Unmarshal(body, &orgs); err != nil || len(orgs) == 0 {
		return fmt.Errorf("org '%s' not found after creation", orgName)
	}
	orgId := orgs[0]["id"].(string)

	query = url.Values{}
	query.Set("username", username)
	query.Set("exact", "true")
	_, body, err = t.KeycloakAdminRequest(ctx, http.MethodGet, "/users?"+query.Encode(), nil)
	if err != nil {
		return fmt.Errorf("failed to get user '%s': %w", username, err)
	}
	var users []map[string]any
	if err := json.Unmarshal(body, &users); err != nil || len(users) == 0 {
		return fmt.Errorf("user '%s' not found", username)
	}
	userId := users[0]["id"].(string)

	code, _, err = t.KeycloakAdminRequest(ctx, http.MethodPost,
		fmt.Sprintf("/organizations/%s/members", orgId), userId)
	if err != nil {
		return fmt.Errorf("failed to add '%s' to org '%s': %w", username, orgName, err)
	}
	if code != http.StatusCreated && code != http.StatusNoContent && code != http.StatusConflict {
		return fmt.Errorf("failed to add '%s' to org '%s': status=%d", username, orgName, code)
	}

	groupPayload := map[string]any{"name": "/members"}
	code, body, err = t.KeycloakAdminRequest(ctx, http.MethodPost,
		fmt.Sprintf("/organizations/%s/groups", orgId), groupPayload)
	if err != nil {
		return fmt.Errorf("failed to create members group in org '%s': %w", orgName, err)
	}

	var groupId string
	if code == http.StatusCreated {
		var g map[string]any
		if err := json.Unmarshal(body, &g); err == nil {
			groupId, _ = g["id"].(string)
		}
	}
	if groupId == "" {
		_, body, err = t.KeycloakAdminRequest(ctx, http.MethodGet,
			fmt.Sprintf("/organizations/%s/groups", orgId), nil)
		if err != nil {
			return fmt.Errorf("failed to get groups for org '%s': %w", orgName, err)
		}
		var groups []map[string]any
		if err := json.Unmarshal(body, &groups); err == nil {
			for _, g := range groups {
				if name, ok := g["name"].(string); ok && name == "/members" {
					groupId, _ = g["id"].(string)
					break
				}
			}
		}
	}
	if groupId == "" {
		return fmt.Errorf("failed to find /members group in org '%s'", orgName)
	}

	code, _, err = t.KeycloakAdminRequest(ctx, http.MethodPut,
		fmt.Sprintf("/organizations/%s/groups/%s/members/%s", orgId, groupId, userId), nil)
	if err != nil {
		return fmt.Errorf("failed to add '%s' to members group: %w", username, err)
	}
	if code != http.StatusOK && code != http.StatusCreated && code != http.StatusNoContent && code != http.StatusConflict {
		return fmt.Errorf("failed to add '%s' to members group: status=%d", username, code)
	}

	t.logger.InfoContext(ctx, "User added to organization", "!user", username, "!org", orgName)
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
		SetPassword("admin").
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
	hubKcBytes, err := os.ReadFile(t.kcFile)
	if err != nil {
		return fmt.Errorf("failed to read kubeconfig file: %w", err)
	}
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
			Metadata: privatev1.Metadata_builder{
				Name: hubId,
			}.Build(),
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
	_, err := t.runCommand(ctx, "go", "build", "-o", t.cliBinaryPath, "./cmd/osac")
	if err != nil {
		return fmt.Errorf("failed to build CLI binary: %w", err)
	}
	return nil
}

// CLIBinaryPath returns the path to the built osac CLI binary.
func (t *Tool) CLIBinaryPath() string {
	return t.cliBinaryPath
}

// Secret returns the controller client secret from the cluster. The prereqs chart creates
// the fulfillment-controller-credentials Secret with a randomly generated client secret.
func (t *Tool) Secret() string {
	secret := &corev1.Secret{}
	key := crclient.ObjectKey{Namespace: "osac", Name: "fulfillment-controller-credentials"}
	if err := t.kubeClient.Get(context.Background(), key, secret); err != nil {
		return t.secret
	}
	if v, ok := secret.Data["client-secret"]; ok {
		return string(v)
	}
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
// exec.CommandContext directly rather than runCommand because the CLI tests need custom
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
	logsDir := filepath.Join(t.projectDir, "logs")
	_, dumpErr := t.runCommand(ctx, "kind", "export", "logs", logsDir, "--name", t.clusterName)
	if dumpErr != nil {
		errs = append(errs, fmt.Errorf("failed to dump cluster logs: %w", dumpErr))
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
const kubectlCmd = "kubectl"

// Name and namespace of the hub:
const hubId = "local"
const hubNamespace = "osac"

// userAgent is the user agent string for the integration test tool.
const userAgent = "fulfillment-it-tool"

// Service host name and address (host-side, via Kind port mapping):
const (
	keycloakAddr        = "keycloak.keycloak.svc.cluster.local:8443"
	externalServiceAddr = "fulfillment-api.osac.svc.cluster.local:8443"
	internalServiceAddr = "fulfillment-internal-api.osac.svc.cluster.local:8443"
)

// Namespace, name and key of the Kubernetes secret that contains the random secret used for passwords and credentials.
const (
	randomSecretNamespace = "default"
	randomSecretName      = "random"
	randomSecretKey       = "secret"
)

// Name of the Kubernetes service account that is used for emergency administration access.
const emergencyServiceAccount = "admin"

// Keycloak client ID for the controller service account.
const controllerClientId = "osac-controller"

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
