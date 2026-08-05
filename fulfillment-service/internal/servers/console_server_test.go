/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	osacv1alpha1 "github.com/osac-project/osac-operator/api/v1alpha1"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	authpkg "github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/auth/jwe"
	"github.com/osac-project/fulfillment-service/internal/console"
	"github.com/osac-project/fulfillment-service/internal/database"
	"github.com/osac-project/fulfillment-service/internal/kubernetes/labels"
)

// mockCIServer implements just the Get method of privatev1.ComputeInstancesServer.
type mockCIServer struct {
	privatev1.UnimplementedComputeInstancesServer
	getResponse *privatev1.ComputeInstancesGetResponse
	getError    error
}

func (m *mockCIServer) Get(ctx context.Context, req *privatev1.ComputeInstancesGetRequest) (*privatev1.ComputeInstancesGetResponse, error) {
	return m.getResponse, m.getError
}

// mockHubServer implements just the Get method of privatev1.HubsServer.
type mockHubServer struct {
	privatev1.UnimplementedHubsServer
	getResponse *privatev1.HubsGetResponse
	getError    error
}

func (m *mockHubServer) Get(ctx context.Context, req *privatev1.HubsGetRequest) (*privatev1.HubsGetResponse, error) {
	return m.getResponse, m.getError
}

// mockBackendForServer is a test backend that returns a mockConn.
type mockBackendForServer struct {
	conn       io.ReadWriteCloser
	connErr    error
	lastTarget console.Target
	targetMu   sync.Mutex
}

func (b *mockBackendForServer) Connect(ctx context.Context, target console.Target) (io.ReadWriteCloser, error) {
	b.targetMu.Lock()
	b.lastTarget = target
	b.targetMu.Unlock()
	return b.conn, b.connErr
}

func (b *mockBackendForServer) getLastTarget() console.Target {
	b.targetMu.Lock()
	defer b.targetMu.Unlock()
	return b.lastTarget
}

type mockConn struct {
	readBuf  *bytes.Buffer
	writeBuf *bytes.Buffer
	closeCh  chan struct{}
	closed   bool
}

func newMockConn(readData string) *mockConn {
	return &mockConn{
		readBuf:  bytes.NewBufferString(readData),
		writeBuf: &bytes.Buffer{},
		closeCh:  make(chan struct{}),
	}
}

func (c *mockConn) Read(p []byte) (int, error) {
	if c.closed {
		return 0, io.EOF
	}
	n, err := c.readBuf.Read(p)
	if err == io.EOF {
		// Block until Close is called instead of returning EOF immediately.
		// This prevents the backend-read goroutine from exiting before the
		// client-write goroutine has processed all input.
		<-c.closeCh
		return 0, io.EOF
	}
	return n, err
}

func (c *mockConn) Write(p []byte) (int, error) {
	return c.writeBuf.Write(p)
}

func (c *mockConn) Close() error {
	if !c.closed {
		c.closed = true
		close(c.closeCh)
	}
	return nil
}

// mockTxManager provides a no-op transaction manager for testing.
type mockTxManager struct{}

func (m *mockTxManager) Begin(ctx context.Context) (database.Tx, error) {
	return &mockTx{}, nil
}

func (m *mockTxManager) Run(ctx context.Context, task any, args ...any) error {
	return nil
}

// mockTx is a no-op transaction for testing.
type mockTx struct{}

func (m *mockTx) Query(ctx context.Context, query string, args ...any) (pgx.Rows, error) {
	return nil, nil
}

func (m *mockTx) QueryRow(ctx context.Context, query string, args ...any) pgx.Row {
	return nil
}

func (m *mockTx) Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (m *mockTx) ReportError(err *error) {}

func (m *mockTx) End(ctx context.Context) error { return nil }

func (m *mockTx) Run(ctx context.Context, task any, args ...any) error {
	return nil
}

// newFakeHubClientFactory returns a HubClientFactory that ignores the config
// and always returns the provided fake client.
func newFakeHubClientFactory(client clnt.Client) HubClientFactory {
	return func(config *rest.Config) (clnt.Client, error) {
		return client, nil
	}
}

// newComputeInstanceCR creates a typed ComputeInstance CR for testing.
func newComputeInstanceCR(id, namespace string, phase osacv1alpha1.ComputeInstancePhaseType) *osacv1alpha1.ComputeInstance {
	obj := &osacv1alpha1.ComputeInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ci-" + id,
			Namespace: namespace,
			Labels: map[string]string{
				labels.ComputeInstanceUuid: id,
			},
		},
	}
	if phase != "" {
		obj.Status.Phase = phase
	}
	return obj
}

// testScheme is a shared scheme for test fake clients with OSAC types registered.
var testScheme = func() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = osacv1alpha1.AddToScheme(s)
	return s
}()

// testKubeconfig is a minimal valid kubeconfig YAML for tests.
var testKubeconfig = []byte(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://test-hub.example.com:6443
    insecure-skip-tls-verify: true
  name: test
contexts:
- context:
    cluster: test
    user: test
  name: test
current-context: test
users:
- name: test
  user:
    token: test-hub-token
`)

// newFakeClient creates a fake K8s client with the given objects.
func newFakeClient(objects ...clnt.Object) clnt.Client {
	return fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(objects...).
		Build()
}

// createTestSealer generates temporary cert/key files for signing and encryption,
// and creates a TicketSealer for test assertions.
func createTestSealer() (*console.TicketSealer, string) {
	tmpDir, err := os.MkdirTemp("", "console-server-test-*")
	Expect(err).NotTo(HaveOccurred())

	signingCertFile := filepath.Join(tmpDir, "signing-tls.crt")
	signingKeyFile := filepath.Join(tmpDir, "signing-tls.key")
	encryptionCertFile := filepath.Join(tmpDir, "encryption-tls.crt")
	encryptionKeyFile := filepath.Join(tmpDir, "encryption-tls.key")

	generateSelfSignedCert(signingCertFile, signingKeyFile, "test-signer")
	generateSelfSignedCert(encryptionCertFile, encryptionKeyFile, "test-encryption")
	_ = encryptionKeyFile // only the cert is needed by the sealer

	tokenSealer, err := jwe.NewSealer().
		SetLogger(logger).
		SetSigningCertFile(signingCertFile).
		SetSigningKeyFile(signingKeyFile).
		SetEncryptionCertFile(encryptionCertFile).
		SetIssuer("https://fulfillment.test.example.com").
		Build()
	Expect(err).NotTo(HaveOccurred())

	return console.NewTicketSealer(tokenSealer), tmpDir
}

// generateSelfSignedCert generates a self-signed RSA certificate and writes
// the cert and key files to disk.
func generateSelfSignedCert(certFile, keyFile, cn string) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	Expect(err).NotTo(HaveOccurred())

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		DNSNames:              []string{cn},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &rsaKey.PublicKey, rsaKey)
	Expect(err).NotTo(HaveOccurred())

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, err := x509.MarshalPKCS8PrivateKey(rsaKey)
	Expect(err).NotTo(HaveOccurred())
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})

	err = os.WriteFile(certFile, certPEM, 0600)
	Expect(err).NotTo(HaveOccurred())
	err = os.WriteFile(keyFile, keyPEM, 0600)
	Expect(err).NotTo(HaveOccurred())
}

var _ = Describe("Console Server", func() {
	var (
		ciServer  *mockCIServer
		hubServer *mockHubServer
	)

	BeforeEach(func() {
		ciServer = &mockCIServer{}
		hubServer = &mockHubServer{}
	})

	// setupHubMock configures the hub server mock and creates a fake K8s client
	// with a ComputeInstance CR in the given phase.
	setupHubMock := func(instanceID, hubNamespace string, phase osacv1alpha1.ComputeInstancePhaseType) clnt.Client {
		hubServer.getResponse = privatev1.HubsGetResponse_builder{
			Object: privatev1.Hub_builder{
				Id: "hub-1",
				Spec: privatev1.HubSpec_builder{
					Kubeconfig: testKubeconfig,
					Namespace:  hubNamespace,
				}.Build(),
			}.Build(),
		}.Build()
		cr := newComputeInstanceCR(instanceID, hubNamespace, phase)
		return newFakeClient(cr)
	}

	Describe("Build", func() {
		It("should fail without logger", func() {
			sealer, tmpDir := createTestSealer()
			defer os.RemoveAll(tmpDir)

			sessionService, err := console.NewSessionService().
				SetLogger(logger).
				SetResolver(&ConsoleTargetResolver{}).
				SetSealer(sealer).
				Build()
			Expect(err).NotTo(HaveOccurred())

			_, err = NewConsoleServer().
				SetSessionService(sessionService).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("logger"))
		})

		It("should fail without session service", func() {
			_, err := NewConsoleServer().
				SetLogger(logger).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("session service"))
		})

		It("should build successfully with all dependencies", func() {
			sealer, tmpDir := createTestSealer()
			defer os.RemoveAll(tmpDir)

			sessionService, err := console.NewSessionService().
				SetLogger(logger).
				SetResolver(&ConsoleTargetResolver{}).
				SetSealer(sealer).
				Build()
			Expect(err).NotTo(HaveOccurred())

			server, err := NewConsoleServer().
				SetLogger(logger).
				SetSessionService(sessionService).
				Build()
			Expect(err).NotTo(HaveOccurred())
			Expect(server).NotTo(BeNil())
		})
	})

	Describe("Create", func() {
		var (
			server  publicv1.ConsoleSessionsServer
			sealer  *console.TicketSealer
			tmpDir  string
			fakeK8s clnt.Client
		)

		BeforeEach(func() {
			sealer, tmpDir = createTestSealer()
			DeferCleanup(func() { os.RemoveAll(tmpDir) })
		})

		buildServer := func() {
			resolver, err := NewConsoleTargetResolver().
				SetLogger(logger).
				SetComputeInstanceLookup(NewPrivateServerCILookup(ciServer)).
				SetHubLookup(NewPrivateServerHubLookup(hubServer)).
				SetHubClientFactory(newFakeHubClientFactory(fakeK8s)).
				Build()
			Expect(err).NotTo(HaveOccurred())

			sessionService, err := console.NewSessionService().
				SetLogger(logger).
				SetResolver(resolver).
				SetSealer(sealer).
				Build()
			Expect(err).NotTo(HaveOccurred())

			server, err = NewConsoleServer().
				SetLogger(logger).
				SetSessionService(sessionService).
				Build()
			Expect(err).NotTo(HaveOccurred())
		}

		It("should create a ticket for a running compute instance", func() {
			ciServer.getResponse = privatev1.ComputeInstancesGetResponse_builder{
				Object: privatev1.ComputeInstance_builder{
					Id: "ci-123",
					Status: privatev1.ComputeInstanceStatus_builder{
						State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
						Hub:   "hub-1",
					}.Build(),
				}.Build(),
			}.Build()

			fakeK8s = setupHubMock("ci-123", "test-ns", osacv1alpha1.ComputeInstancePhaseRunning)
			buildServer()

			ctx := authpkg.ContextWithSubject(context.Background(), &authpkg.Subject{
				User: "testuser",
			})
			ctx = database.TxManagerIntoContext(ctx, &mockTxManager{})
			resp, err := server.Create(ctx, publicv1.ConsoleSessionsCreateRequest_builder{
				Object: publicv1.ConsoleSession_builder{
					ResourceType: publicv1.ConsoleResourceType_CONSOLE_RESOURCE_TYPE_COMPUTE_INSTANCE,
					ResourceId:   "ci-123",
					Type:         publicv1.ConsoleType_CONSOLE_TYPE_VNC,
					ClientId:     "550e8400-e29b-41d4-a716-446655440000",
				}.Build(),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.GetObject().GetTicket()).NotTo(BeEmpty())
			Expect(resp.GetObject().GetExpiresAt()).NotTo(BeNil())
		})

		It("should reject empty resource_id", func() {
			buildServer()

			ctx := authpkg.ContextWithSubject(context.Background(), &authpkg.Subject{User: "testuser"})
			ctx = database.TxManagerIntoContext(ctx, &mockTxManager{})
			_, err := server.Create(ctx, publicv1.ConsoleSessionsCreateRequest_builder{
				Object: publicv1.ConsoleSession_builder{
					ResourceType: publicv1.ConsoleResourceType_CONSOLE_RESOURCE_TYPE_COMPUTE_INSTANCE,
					ResourceId:   "",
					Type:         publicv1.ConsoleType_CONSOLE_TYPE_SERIAL,
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		})

		It("should reject when compute instance is not running", func() {
			ciServer.getResponse = privatev1.ComputeInstancesGetResponse_builder{
				Object: privatev1.ComputeInstance_builder{
					Id: "ci-123",
					Status: privatev1.ComputeInstanceStatus_builder{
						State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
					}.Build(),
				}.Build(),
			}.Build()
			buildServer()

			ctx := authpkg.ContextWithSubject(context.Background(), &authpkg.Subject{User: "testuser"})
			ctx = database.TxManagerIntoContext(ctx, &mockTxManager{})
			_, err := server.Create(ctx, publicv1.ConsoleSessionsCreateRequest_builder{
				Object: publicv1.ConsoleSession_builder{
					ResourceType: publicv1.ConsoleResourceType_CONSOLE_RESOURCE_TYPE_COMPUTE_INSTANCE,
					ResourceId:   "ci-123",
					Type:         publicv1.ConsoleType_CONSOLE_TYPE_SERIAL,
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
		})

		It("should reject unsupported resource type", func() {
			buildServer()

			ctx := authpkg.ContextWithSubject(context.Background(), &authpkg.Subject{User: "testuser"})
			ctx = database.TxManagerIntoContext(ctx, &mockTxManager{})
			_, err := server.Create(ctx, publicv1.ConsoleSessionsCreateRequest_builder{
				Object: publicv1.ConsoleSession_builder{
					ResourceType: publicv1.ConsoleResourceType_CONSOLE_RESOURCE_TYPE_HOST,
					ResourceId:   "host-1",
					Type:         publicv1.ConsoleType_CONSOLE_TYPE_SERIAL,
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(status.Code(err)).To(Equal(codes.Unimplemented))
		})

		It("should reject unsupported console type", func() {
			ciServer.getResponse = privatev1.ComputeInstancesGetResponse_builder{
				Object: privatev1.ComputeInstance_builder{
					Id: "ci-123",
					Status: privatev1.ComputeInstanceStatus_builder{
						State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
						Hub:   "hub-1",
					}.Build(),
				}.Build(),
			}.Build()
			buildServer()

			ctx := authpkg.ContextWithSubject(context.Background(), &authpkg.Subject{User: "testuser"})
			ctx = database.TxManagerIntoContext(ctx, &mockTxManager{})
			_, err := server.Create(ctx, publicv1.ConsoleSessionsCreateRequest_builder{
				Object: publicv1.ConsoleSession_builder{
					ResourceType: publicv1.ConsoleResourceType_CONSOLE_RESOURCE_TYPE_COMPUTE_INSTANCE,
					ResourceId:   "ci-123",
					Type:         publicv1.ConsoleType(99),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		})
	})
})
