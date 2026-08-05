package consoleproxy

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/coder/websocket"
	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	osacv1alpha1 "github.com/osac-project/osac-operator/api/v1alpha1"
)

var _ = Describe("kubevirtSubresourceURL", func() {
	DescribeTable("builds the correct WebSocket URL",
		func(host, namespace, vmName, subresource, wantURL, wantErr string) {
			u, err := kubevirtSubresourceURL(&rest.Config{Host: host}, namespace, vmName, subresource)

			if wantErr != "" {
				Expect(err).To(MatchError(wantErr))
				return
			}

			Expect(err).NotTo(HaveOccurred())
			Expect(u.String()).To(Equal(wantURL))
		},
		Entry("console preserves API path prefix",
			"https://gateway.example/cluster-a", "tenant-a", "vm-a", "console",
			"wss://gateway.example/cluster-a/apis/subresources.kubevirt.io/v1/namespaces/tenant-a/virtualmachineinstances/vm-a/console",
			""),
		Entry("vnc preserves API path prefix",
			"https://gateway.example/cluster-a", "tenant-a", "vm-a", "vnc",
			"wss://gateway.example/cluster-a/apis/subresources.kubevirt.io/v1/namespaces/tenant-a/virtualmachineinstances/vm-a/vnc",
			""),
		Entry("converts http to ws",
			"http://gateway.example/base/", "tenant-a", "vm-a", "console",
			"ws://gateway.example/base/apis/subresources.kubevirt.io/v1/namespaces/tenant-a/virtualmachineinstances/vm-a/console",
			""),
		Entry("rejects missing scheme",
			"gateway.example/cluster-a", "tenant-a", "vm-a", "console",
			"",
			`unsupported remote host protocol ""`),
	)
})

var _ = Describe("handleSubresource", func() {
	Context("error cases", func() {
		DescribeTable("returns the expected status code",
			func(objects []client.Object, makeResolver func() (ConfigResolver, func()), wantCode int) {
				resolver, cleanup := makeResolver()
				DeferCleanup(cleanup)
				server := newTestServer(objects, resolver)

				req := httptest.NewRequest(http.MethodGet, consolePath, nil)
				req.SetPathValue("namespace", "tenant-a")
				req.SetPathValue("name", "vm-a")

				rec := httptest.NewRecorder()
				server.handleSubresource(rec, req, "console")

				Expect(rec.Code).To(Equal(wantCode))
			},
			Entry("resolver failure returns service unavailable",
				nil,
				func() (ConfigResolver, func()) {
					return fakeConfigResolver{err: errors.New("bad kubeconfig")}, func() {}
				},
				http.StatusServiceUnavailable),
			Entry("missing compute instance returns not found",
				nil,
				func() (ConfigResolver, func()) {
					return fakeConfigResolver{
						config: &rest.Config{Host: "https://fake:6443"},
						source: "test",
					}, func() {}
				},
				http.StatusNotFound),
			Entry("missing VM reference returns service unavailable",
				[]client.Object{
					&osacv1alpha1.ComputeInstance{
						ObjectMeta: metav1.ObjectMeta{Name: "vm-a", Namespace: "tenant-a"},
					},
				},
				func() (ConfigResolver, func()) {
					return fakeConfigResolver{
						config: &rest.Config{Host: "https://fake:6443"},
						source: "test",
					}, func() {}
				},
				http.StatusServiceUnavailable),
			Entry("dial failure returns service unavailable",
				[]client.Object{newComputeInstance("tenant-a", "vm-a", "vm-ns", "kubevirt-vm")},
				func() (ConfigResolver, func()) {
					return fakeConfigResolver{
						config: closedPortConfig(),
						source: "test",
					}, func() {}
				},
				http.StatusServiceUnavailable),
			Entry("upstream error forwarded",
				[]client.Object{newComputeInstance("tenant-a", "vm-a", "vm-ns", "kubevirt-vm")},
				func() (ConfigResolver, func()) {
					upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusConflict)
						_, _ = w.Write([]byte(`{"message":"vm busy"}`))
					}))
					return fakeConfigResolver{config: configForTLSServer(upstream), source: "test"}, upstream.Close
				},
				http.StatusConflict),
		)
	})

	DescribeTable("proxies WebSocket messages successfully",
		func(subresource string) {
			echoServer, echoCfg := startWSEchoServer()
			DeferCleanup(echoServer.Close)

			ci := newComputeInstance("tenant-a", "vm-a", "test-ns", "test-vm")
			server := newTestServer([]client.Object{ci}, fakeConfigResolver{config: echoCfg, source: "test"})

			httpServer := httptest.NewServer(server.newAPIMux())
			DeferCleanup(httpServer.Close)

			wsURL := strings.Replace(httpServer.URL, "http://", "ws://", 1) +
				"/apis/" + apiGroup + "/" + apiVersion + "/namespaces/tenant-a/computeinstances/vm-a/" + subresource

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			DeferCleanup(cancel)

			conn, _, err := websocket.Dial(ctx, wsURL, nil)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { _ = conn.CloseNow() })

			testMessage := []byte("hello " + subresource)
			Expect(conn.Write(ctx, websocket.MessageBinary, testMessage)).To(Succeed())

			msgType, data, err := conn.Read(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(msgType).To(Equal(websocket.MessageBinary))
			Expect(string(data)).To(Equal(string(testMessage)))

			_ = conn.Close(websocket.StatusNormalClosure, "")
		},
		Entry("console", "console"),
		Entry("vnc", "vnc"),
	)
})

var _ = Describe("resolveVMReference", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
	})

	DescribeTable("returns the expected result",
		func(objects []client.Object, wantNS, wantName string, wantCode int, wantReason metav1.StatusReason, wantMessage string) {
			server := &Server{
				hubClient: fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(objects...).
					Build(),
			}

			vmNS, vmName, err := server.resolveVMReference(context.Background(), "tenant-a", "vm-a")

			if wantNS != "" {
				Expect(err).NotTo(HaveOccurred())
				Expect(vmNS).To(Equal(wantNS))
				Expect(vmName).To(Equal(wantName))
				return
			}

			Expect(err).To(HaveOccurred())
			status := statusFromError(err)
			Expect(int(status.Code)).To(Equal(wantCode))
			Expect(status.Reason).To(Equal(wantReason))
			if wantMessage != "" {
				Expect(status.Message).To(ContainSubstring(wantMessage))
			}
		},
		Entry("missing compute instance returns not found",
			[]client.Object{},
			"", "", http.StatusNotFound, metav1.StatusReasonNotFound, ""),
		Entry("missing VM reference returns service unavailable",
			[]client.Object{
				&osacv1alpha1.ComputeInstance{
					ObjectMeta: metav1.ObjectMeta{Name: "vm-a", Namespace: "tenant-a"},
				},
			},
			"", "", http.StatusServiceUnavailable, metav1.StatusReasonServiceUnavailable,
			"virtual machine reference is not available yet"),
		Entry("incomplete VM reference returns service unavailable",
			[]client.Object{
				&osacv1alpha1.ComputeInstance{
					ObjectMeta: metav1.ObjectMeta{Name: "vm-a", Namespace: "tenant-a"},
					Status: osacv1alpha1.ComputeInstanceStatus{
						VirtualMachineReference: &osacv1alpha1.VirtualMachineReferenceType{
							Namespace: "tenant-a",
						},
					},
				},
			},
			"", "", http.StatusServiceUnavailable, metav1.StatusReasonServiceUnavailable,
			"virtual machine reference is incomplete"),
		Entry("returns resolved VM reference",
			[]client.Object{newComputeInstance("tenant-a", "vm-a", "vm-ns", "kubevirt-vm")},
			"vm-ns", "kubevirt-vm", 0, metav1.StatusReason(""), ""),
	)
})

var _ = Describe("forwardUpstreamResponse", func() {
	DescribeTable("forwards the response correctly",
		func(statusCode int, contentType, body string, wantCode int, wantContentType, wantBody string) {
			resp := &http.Response{
				StatusCode: statusCode,
				Header:     http.Header{},
			}
			if contentType != "" {
				resp.Header.Set("Content-Type", contentType)
			}
			if body != "" {
				resp.Body = io.NopCloser(strings.NewReader(body))
			}

			rec := httptest.NewRecorder()
			forwardUpstreamResponse(rec, resp)

			Expect(rec.Code).To(Equal(wantCode))
			if wantContentType != "" {
				Expect(rec.Header().Get("Content-Type")).To(Equal(wantContentType))
			}
			Expect(rec.Body.String()).To(Equal(wantBody))
		},
		Entry("forwards JSON status body",
			http.StatusConflict, "application/json",
			`{"kind":"Status","apiVersion":"v1","status":"Failure","message":"virtual machine instance is not ready","reason":"Conflict","code":409}`,
			http.StatusConflict, "application/json",
			`{"kind":"Status","apiVersion":"v1","status":"Failure","message":"virtual machine instance is not ready","reason":"Conflict","code":409}`),
		Entry("forwards plain text body",
			http.StatusServiceUnavailable, "text/plain",
			"Active VNC connection. Request denied.",
			http.StatusServiceUnavailable, "text/plain",
			"Active VNC connection. Request denied."),
		Entry("handles missing content type",
			http.StatusBadGateway, "", "bad gateway",
			http.StatusBadGateway, "", "bad gateway"),
		Entry("handles nil body",
			http.StatusInternalServerError, "", "",
			http.StatusInternalServerError, "", ""),
	)
})

var _ = Describe("newConnectStatusError", func() {
	It("returns upstream error when response is non-nil", func() {
		resp := &http.Response{
			StatusCode: http.StatusConflict,
			Status:     "409 Conflict",
		}

		err := newConnectStatusError(errors.New("dial failed"), resp)

		var ue *upstreamError
		Expect(errors.As(err, &ue)).To(BeTrue())
		Expect(ue.resp).To(Equal(resp))
	})

	It("returns service unavailable when response is nil", func() {
		err := newConnectStatusError(errors.New("dial failed"), nil)
		Expect(apierrors.IsServiceUnavailable(err)).To(BeTrue())
	})
})

var _ = Describe("dialSubresource", func() {
	DescribeTable("handles connection scenarios",
		func(setup func() (*rest.Config, func()), wantErr bool) {
			cfg, cleanup := setup()
			DeferCleanup(cleanup)

			server := &Server{logger: discardLogger}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			DeferCleanup(cancel)

			conn, err := server.dialSubresource(ctx, cfg, "test-ns", "test-vm", "console")
			if wantErr {
				Expect(err).To(HaveOccurred())
				return
			}
			Expect(err).NotTo(HaveOccurred())
			_ = conn.CloseNow()
		},
		Entry("success",
			func() (*rest.Config, func()) {
				ts, cfg := startWSEchoServer()
				return cfg, ts.Close
			}, false),
		Entry("invalid host",
			func() (*rest.Config, func()) {
				return &rest.Config{Host: "://bad"}, func() {}
			}, true),
		Entry("connection refused",
			func() (*rest.Config, func()) {
				return closedPortConfig(), func() {}
			}, true),
	)
})

// --- Test helpers ---

type fakeConfigResolver struct {
	config *rest.Config
	source string
	err    error
}

func (f fakeConfigResolver) ResolveConfig(_ context.Context, _ string) (*rest.Config, string, error) {
	return f.config, f.source, f.err
}

func newTestServer(objects []client.Object, resolver ConfigResolver) *Server {
	scheme := runtime.NewScheme()
	Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
	Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

	builder := fake.NewClientBuilder().WithScheme(scheme)
	if len(objects) > 0 {
		builder = builder.WithObjects(objects...)
	}

	return &Server{
		logger:         discardLogger,
		hubClient:      builder.Build(),
		configResolver: resolver,
		probes:         newProbeState(),
	}
}

func newComputeInstance(namespace, name, vmNS, vmName string) *osacv1alpha1.ComputeInstance {
	return &osacv1alpha1.ComputeInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Status: osacv1alpha1.ComputeInstanceStatus{
			VirtualMachineReference: &osacv1alpha1.VirtualMachineReferenceType{
				Namespace:                  vmNS,
				KubeVirtVirtualMachineName: vmName,
			},
		},
	}
}

func startWSEchoServer() (*httptest.Server, *rest.Config) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer func() { _ = conn.CloseNow() }()

		for {
			msgType, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			if err := conn.Write(r.Context(), msgType, data); err != nil {
				return
			}
		}
	})

	ts := httptest.NewTLSServer(mux)
	cfg := configForTLSServer(ts)
	return ts, cfg
}

func configForTLSServer(ts *httptest.Server) *rest.Config {
	cert := ts.TLS.Certificates[0]
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	Expect(err).NotTo(HaveOccurred())
	caData := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Raw})

	return &rest.Config{
		Host: ts.URL,
		TLSClientConfig: rest.TLSClientConfig{
			CAData: caData,
		},
	}
}

func closedPortConfig() *rest.Config {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()

	return &rest.Config{
		Host:            fmt.Sprintf("https://127.0.0.1:%d", port),
		TLSClientConfig: rest.TLSClientConfig{Insecure: true},
	}
}

func statusFromError(err error) metav1.Status {
	statusErr, ok := err.(interface{ Status() metav1.Status })
	Expect(ok).To(BeTrue(), "error does not expose Kubernetes status: %T", err)
	return statusErr.Status()
}
