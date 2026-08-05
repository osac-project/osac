// Package consoleproxy implements a WebSocket proxy server that exposes
// KubeVirt VM subresource access (console, VNC) through OSAC ComputeInstance resources.
package consoleproxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	apiGroup   = "console.osac.openshift.io"
	apiVersion = "v1alpha1"
)

var subresources = []string{"console", "vnc"}

type Config struct {
	Logger                 *slog.Logger
	HubConfig              *rest.Config
	HubClient              client.Client
	ConfigResolver         ConfigResolver
	Port                   int
	HealthProbeBindAddress string
	TLSCertFile            string
	TLSKeyFile             string
}

type Server struct {
	logger                 *slog.Logger
	hubConfig              *rest.Config
	hubClient              client.Client
	configResolver         ConfigResolver
	port                   int
	healthProbeBindAddress string
	tlsCertFile            string
	tlsKeyFile             string
	probes                 *probeState
}

func NewServer(cfg Config) (*Server, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.HubConfig == nil {
		return nil, fmt.Errorf("hub config is required")
	}
	if cfg.HubClient == nil {
		return nil, fmt.Errorf("hub client is required")
	}
	if cfg.ConfigResolver == nil {
		return nil, fmt.Errorf("config resolver is required")
	}
	if cfg.Port == 0 {
		return nil, fmt.Errorf("port is required")
	}
	if cfg.TLSCertFile == "" || cfg.TLSKeyFile == "" {
		return nil, fmt.Errorf("TLS cert and key files are required")
	}

	healthProbeBindAddress := cfg.HealthProbeBindAddress
	if healthProbeBindAddress == "" {
		healthProbeBindAddress = ":8081"
	}
	return &Server{
		logger:                 cfg.Logger,
		hubConfig:              cfg.HubConfig,
		hubClient:              cfg.HubClient,
		configResolver:         cfg.ConfigResolver,
		port:                   cfg.Port,
		healthProbeBindAddress: healthProbeBindAddress,
		tlsCertFile:            cfg.TLSCertFile,
		tlsKeyFile:             cfg.TLSKeyFile,
		probes:                 newProbeState(),
	}, nil
}

func (s *Server) Run(ctx context.Context) error {
	apiHandler, authCtrl, err := buildAuthHandler(ctx, s.newAPIMux(), s.hubConfig)
	if err != nil {
		return fmt.Errorf("failed to build auth handler: %w", err)
	}
	apiServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", s.port),
		Handler:           apiHandler,
		ReadHeaderTimeout: 5 * time.Second,
		TLSConfig: &tls.Config{
			ClientAuth: tls.RequestClientCert,
			MinVersion: tls.VersionTLS12,
		},
		// Disable HTTP/2 so WebSocket upgrade works.
		TLSNextProto: map[string]func(*http.Server, *tls.Conn, http.Handler){},
	}
	probeServer := &http.Server{
		Addr:              s.healthProbeBindAddress,
		Handler:           newProbeMux(s.probes),
		ReadHeaderTimeout: 5 * time.Second,
	}

	apiListener, err := net.Listen("tcp", apiServer.Addr)
	if err != nil {
		return fmt.Errorf("failed to listen for API server: %w", err)
	}
	probeListener, err := net.Listen("tcp", probeServer.Addr)
	if err != nil {
		_ = apiListener.Close()
		return fmt.Errorf("failed to listen for health probe server: %w", err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	authCtrl.Run(runCtx)
	s.probes.MarkReady()

	s.logger.Info("Starting API server", "addr", apiServer.Addr)
	s.logger.Info("Starting health probe server", "addr", probeServer.Addr)

	group, groupCtx := errgroup.WithContext(runCtx)
	group.Go(func() error {
		<-groupCtx.Done()

		s.probes.MarkShuttingDown()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		var shutdownErrs []error
		if err := probeServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			shutdownErrs = append(shutdownErrs, fmt.Errorf("shutdown health probe server: %w", err))
		}
		if err := apiServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			shutdownErrs = append(shutdownErrs, fmt.Errorf("shutdown API server: %w", err))
		}
		return errors.Join(shutdownErrs...)
	})
	group.Go(func() error {
		if err := probeServer.Serve(probeListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("health probe server failed: %w", err)
		}
		return nil
	})
	group.Go(func() error {
		if err := apiServer.ServeTLS(apiListener, s.tlsCertFile, s.tlsKeyFile); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("API server failed: %w", err)
		}
		return nil
	})

	return group.Wait()
}

func (s *Server) newAPIMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /apis", handleAPIGroupList)
	mux.HandleFunc("GET /apis/"+apiGroup, handleAPIGroup)

	apiResources := make([]metav1.APIResource, 0, len(subresources))
	for _, sub := range subresources {
		apiResources = append(apiResources, metav1.APIResource{
			Name:       "computeinstances/" + sub,
			Kind:       "ComputeInstance",
			Namespaced: true,
			Verbs:      metav1.Verbs{"get"},
		})
		mux.HandleFunc(
			"GET /apis/"+apiGroup+"/"+apiVersion+"/namespaces/{namespace}/computeinstances/{name}/"+sub,
			func(w http.ResponseWriter, r *http.Request) {
				s.handleSubresource(w, r, sub)
			},
		)
	}
	mux.HandleFunc("GET /apis/"+apiGroup+"/"+apiVersion, handleAPIResourceList(apiResources))

	return mux
}

const (
	probeStateStarting int32 = iota
	probeStateReady
	probeStateShuttingDown
)

type probeState struct {
	state atomic.Int32
}

func newProbeState() *probeState {
	p := &probeState{}
	p.state.Store(probeStateStarting)
	return p
}

func (p *probeState) MarkReady() {
	p.state.Store(probeStateReady)
}

func (p *probeState) MarkShuttingDown() {
	p.state.Store(probeStateShuttingDown)
}

func (p *probeState) handleLivez(w http.ResponseWriter, _ *http.Request) {
	writeProbeOK(w)
}

func (p *probeState) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	if p.state.Load() != probeStateReady {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	writeProbeOK(w)
}

func newProbeMux(probes *probeState) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", probes.handleLivez)
	mux.HandleFunc("GET /livez", probes.handleLivez)
	mux.HandleFunc("GET /readyz", probes.handleReadyz)
	return mux
}

func writeProbeOK(w http.ResponseWriter) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}
