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

// Package main implements a standalone in-cluster test-double binary for Kind
// component integration tests. It exposes three listeners:
//   - :8443 TLS OIDC HTTPS discovery and client-credentials token issuance
//   - :8444 Dedicated TLS gRPC (compatible with experimentalcredentials.NewTLSWithALPNDisabled)
//     serving StorageTiers, StorageBackends, Secrets, Tenants, NetworkClasses, and Health
//   - :8080 Plaintext HTTP AAP double (/api/controller/*) and admin control
//     (/healthz, /admin/stats, /admin/state, /admin/reset)
package main

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// FixtureTier represents a storage tier entry in the dynamic in-memory catalog.
type FixtureTier struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	BackendID string `json:"backendId"`
	Protocol  string `json:"protocol"`
}

// FixtureBackend represents a storage backend entry in the dynamic in-memory catalog.
type FixtureBackend struct {
	ID                     string `json:"id"`
	Name                   string `json:"name"`
	Provider               string `json:"provider"`
	CspManaged             bool   `json:"cspManaged"`
	Endpoint               string `json:"endpoint"`
	Username               string `json:"username"`
	InlineCredentialMarker string `json:"inlineCredentialMarker"`
	PasswordSecretID       string `json:"passwordSecretId"`
}

// FixtureState holds the mutable catalog state controlled via /admin/state.
type FixtureState struct {
	Tiers     []FixtureTier    `json:"tiers"`
	Backends  []FixtureBackend `json:"backends"`
	FailTiers bool             `json:"failTiers"`
}

// FixtureStats tracks OIDC, gRPC, and AAP call counts and recorded identifiers.
type FixtureStats struct {
	OIDCTokenRequests   int32    `json:"oidcTokenRequests"`
	TiersListCalls      int32    `json:"tiersListCalls"`
	BackendsListCalls   int32    `json:"backendsListCalls"`
	BackendsGetCalls    int32    `json:"backendsGetCalls"`
	SecretsGetCalls     int32    `json:"secretsGetCalls"`
	SecretsRequestedIDs []string `json:"secretsRequestedIds"`
	AAPLaunchCalls      int32    `json:"aapLaunchCalls"`
	AAPRequests         []string `json:"aapRequests"`
}

type fixtureServer struct {
	privatev1.UnimplementedStorageTiersServer

	issuerURL          string
	expectedClientID   string
	expectedClientPass string
	expectedBearer     string

	mu    sync.RWMutex
	state FixtureState
	stats FixtureStats
}

func (s *fixtureServer) List(
	_ context.Context,
	_ *privatev1.StorageTiersListRequest,
) (*privatev1.StorageTiersListResponse, error) {
	s.mu.Lock()
	s.stats.TiersListCalls++
	failTiers := s.state.FailTiers
	tiers := append([]FixtureTier(nil), s.state.Tiers...)
	s.mu.Unlock()

	if failTiers {
		return nil, status.Error(codes.Unavailable, "simulated tier catalog failure")
	}

	items := make([]*privatev1.StorageTier, 0, len(tiers))
	for _, t := range tiers {
		var backends []*privatev1.BackendAssociation
		if t.BackendID != "" {
			backends = []*privatev1.BackendAssociation{
				privatev1.BackendAssociation_builder{
					BackendId:            t.BackendID,
					MaxReadBandwidthMbs:  100,
					MaxWriteBandwidthMbs: 100,
				}.Build(),
			}
		}
		id := t.ID
		if id == "" {
			id = t.Name
		}
		items = append(items, privatev1.StorageTier_builder{
			Id:       id,
			Metadata: privatev1.Metadata_builder{Name: t.Name}.Build(),
			Spec: privatev1.StorageTierSpec_builder{
				Backends: backends,
				Protocol: parseProtocol(t.Protocol),
			}.Build(),
		}.Build())
	}

	return privatev1.StorageTiersListResponse_builder{
		Items: items,
		Total: int32(len(items)),
	}.Build(), nil
}

func parseProtocol(p string) privatev1.StorageProtocol {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "block":
		return privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK
	case "nfs":
		return privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS
	default:
		return privatev1.StorageProtocol_STORAGE_PROTOCOL_UNSPECIFIED
	}
}

func buildProtoBackend(b FixtureBackend) *privatev1.StorageBackend {
	id := b.ID
	if id == "" {
		id = b.Name
	}
	name := b.Name
	if name == "" {
		name = b.ID
	}

	var creds *privatev1.StorageBackendCredentials
	if b.Username != "" || b.InlineCredentialMarker != "" || b.PasswordSecretID != "" {
		var secretRef *privatev1.SecretLocalReference
		if b.PasswordSecretID != "" {
			secretRef = privatev1.SecretLocalReference_builder{
				Id: b.PasswordSecretID,
			}.Build()
		}
		creds = privatev1.StorageBackendCredentials_builder{
			Username:       b.Username,
			Password:       b.InlineCredentialMarker,
			PasswordSecret: secretRef,
		}.Build()
	}

	return privatev1.StorageBackend_builder{
		Id:       id,
		Metadata: privatev1.Metadata_builder{Name: name}.Build(),
		Spec: privatev1.StorageBackendSpec_builder{
			Provider:    b.Provider,
			CspManaged:  b.CspManaged,
			Endpoint:    b.Endpoint,
			Credentials: creds,
		}.Build(),
	}.Build()
}

type storageBackendsAdapter struct {
	privatev1.UnimplementedStorageBackendsServer
	srv *fixtureServer
}

func (a *storageBackendsAdapter) List(
	_ context.Context,
	_ *privatev1.StorageBackendsListRequest,
) (*privatev1.StorageBackendsListResponse, error) {
	a.srv.mu.Lock()
	a.srv.stats.BackendsListCalls++
	backends := append([]FixtureBackend(nil), a.srv.state.Backends...)
	a.srv.mu.Unlock()

	items := make([]*privatev1.StorageBackend, 0, len(backends))
	for _, b := range backends {
		items = append(items, buildProtoBackend(b))
	}
	return privatev1.StorageBackendsListResponse_builder{
		Items: items,
		Total: int32(len(items)),
	}.Build(), nil
}

func (a *storageBackendsAdapter) Get(
	_ context.Context,
	req *privatev1.StorageBackendsGetRequest,
) (*privatev1.StorageBackendsGetResponse, error) {
	a.srv.mu.Lock()
	a.srv.stats.BackendsGetCalls++
	backends := append([]FixtureBackend(nil), a.srv.state.Backends...)
	a.srv.mu.Unlock()

	targetID := req.GetId()
	for _, b := range backends {
		if b.ID == targetID || (b.ID == "" && b.Name == targetID) {
			return privatev1.StorageBackendsGetResponse_builder{
				Object: buildProtoBackend(b),
			}.Build(), nil
		}
	}
	return nil, status.Errorf(codes.NotFound, "storage backend %q not found", targetID)
}

type secretsAdapter struct {
	privatev1.UnimplementedSecretsServer
	srv *fixtureServer
}

func (a *secretsAdapter) Get(
	_ context.Context,
	req *privatev1.SecretsGetRequest,
) (*privatev1.SecretsGetResponse, error) {
	a.srv.mu.Lock()
	a.srv.stats.SecretsGetCalls++
	a.srv.stats.SecretsRequestedIDs = append(a.srv.stats.SecretsRequestedIDs, req.GetId())
	a.srv.mu.Unlock()

	return nil, status.Errorf(codes.NotFound, "secret %q not found", req.GetId())
}

type tenantsAdapter struct {
	privatev1.UnimplementedTenantsServer
}

func (a *tenantsAdapter) Get(
	_ context.Context,
	req *privatev1.TenantsGetRequest,
) (*privatev1.TenantsGetResponse, error) {
	return privatev1.TenantsGetResponse_builder{
		Object: privatev1.Tenant_builder{
			Id: req.GetId(),
		}.Build(),
	}.Build(), nil
}

func (a *tenantsAdapter) Update(
	_ context.Context,
	req *privatev1.TenantsUpdateRequest,
) (*privatev1.TenantsUpdateResponse, error) {
	return privatev1.TenantsUpdateResponse_builder{
		Object: req.GetObject(),
	}.Build(), nil
}

func (a *tenantsAdapter) Signal(
	_ context.Context,
	_ *privatev1.TenantsSignalRequest,
) (*privatev1.TenantsSignalResponse, error) {
	return &privatev1.TenantsSignalResponse{}, nil
}

type networkClassesAdapter struct {
	privatev1.UnimplementedNetworkClassesServer
}

func (a *networkClassesAdapter) List(
	_ context.Context,
	_ *privatev1.NetworkClassesListRequest,
) (*privatev1.NetworkClassesListResponse, error) {
	return privatev1.NetworkClassesListResponse_builder{
		Items: nil,
		Total: 0,
	}.Build(), nil
}

func (s *fixtureServer) unaryAuthInterceptor(
	ctx context.Context,
	req any,
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (any, error) {
	if strings.HasPrefix(info.FullMethod, "/grpc.health.v1.Health/") {
		return handler(ctx, req)
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing metadata")
	}
	authHeaders := md.Get("authorization")
	wantHeader := "Bearer " + s.expectedBearer
	authenticated := false
	for _, h := range authHeaders {
		if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(h)), []byte(wantHeader)) == 1 {
			authenticated = true
			break
		}
	}
	if !authenticated {
		return nil, status.Error(codes.Unauthenticated, "invalid or missing bearer authorization")
	}
	return handler(ctx, req)
}

func (s *fixtureServer) oidcHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/realms/osac/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		issuer := strings.TrimRight(s.issuerURL, "/")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":         issuer,
			"token_endpoint": issuer + "/token",
		})
	})

	mux.HandleFunc("/realms/osac/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form body", http.StatusBadRequest)
			return
		}
		if r.FormValue("grant_type") != "client_credentials" {
			http.Error(w, "unsupported grant_type", http.StatusBadRequest)
			return
		}

		clientID, clientCredential, ok := r.BasicAuth()
		if !ok {
			clientID = r.FormValue("client_id")
			clientCredential = r.FormValue("client_secret")
		}
		idMatch := subtle.ConstantTimeCompare([]byte(clientID), []byte(s.expectedClientID)) == 1
		credMatch := subtle.ConstantTimeCompare([]byte(clientCredential), []byte(s.expectedClientPass)) == 1
		if !idMatch || !credMatch {
			http.Error(w, "unauthorized client credentials", http.StatusUnauthorized)
			return
		}

		s.mu.Lock()
		s.stats.OIDCTokenRequests++
		s.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": s.expectedBearer,
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})
	return mux
}

func (s *fixtureServer) adminAndAAPHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("/admin/stats", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.mu.RLock()
		snapshot := FixtureStats{
			OIDCTokenRequests:   s.stats.OIDCTokenRequests,
			TiersListCalls:      s.stats.TiersListCalls,
			BackendsListCalls:   s.stats.BackendsListCalls,
			BackendsGetCalls:    s.stats.BackendsGetCalls,
			SecretsGetCalls:     s.stats.SecretsGetCalls,
			SecretsRequestedIDs: append([]string(nil), s.stats.SecretsRequestedIDs...),
			AAPLaunchCalls:      s.stats.AAPLaunchCalls,
			AAPRequests:         append([]string(nil), s.stats.AAPRequests...),
		}
		s.mu.RUnlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(snapshot)
	})

	mux.HandleFunc("/admin/state", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost && r.Method != http.MethodPut {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		defer func() { _ = r.Body.Close() }()
		var next FixtureState
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&next); err != nil {
			http.Error(w, fmt.Sprintf("decode state: %v", err), http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.state = FixtureState{
			Tiers:     append([]FixtureTier(nil), next.Tiers...),
			Backends:  append([]FixtureBackend(nil), next.Backends...),
			FailTiers: next.FailTiers,
		}
		s.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"updated"}`))
	})

	mux.HandleFunc("/admin/reset", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.mu.Lock()
		s.stats = FixtureStats{}
		s.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"reset"}`))
	})

	mux.HandleFunc("/api/controller/", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.stats.AAPLaunchCalls++
		s.stats.AAPRequests = append(s.stats.AAPRequests, r.Method+" "+r.URL.Path)
		s.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"detail": "unexpected AAP call in CSP-managed storage test"}`))
	})

	return mux
}

func main() {
	var (
		tlsCertPath           string
		tlsKeyPath            string
		oidcAddr              string
		grpcAddr              string
		httpAddr              string
		issuerURL             string
		clientID              string
		clientCredentialValue string
		accessMarker          string
	)

	flag.StringVar(&tlsCertPath, "tls-cert", "/tls/tls.crt", "Path to TLS server certificate file")
	flag.StringVar(&tlsKeyPath, "tls-key", "/tls/tls.key", "Path to TLS server private key file")
	flag.StringVar(&oidcAddr, "oidc-addr", ":8443", "Listen address for TLS OIDC HTTPS server")
	flag.StringVar(&grpcAddr, "grpc-addr", ":8444", "Listen address for dedicated TLS gRPC server")
	flag.StringVar(&httpAddr, "http-addr", ":8080", "Listen address for HTTP AAP double and admin server")
	flag.StringVar(
		&issuerURL,
		"issuer-url",
		"https://csp-storage-fixture.osac.svc.cluster.local:8443/realms/osac",
		"OIDC issuer URL advertised by discovery",
	)
	flag.StringVar(&clientID, "client-id", "osac-fixture-client", "Expected OAuth client identifier")
	flag.StringVar(
		&clientCredentialValue,
		"client-credential",
		"osac-fixture-client-credential",
		"Expected OAuth client credential value",
	)
	flag.StringVar(
		&accessMarker,
		"access-marker",
		"osac-fixture-bearer-access-marker",
		"Issued and verified OAuth bearer marker",
	)
	flag.Parse()

	cert, err := tls.LoadX509KeyPair(tlsCertPath, tlsKeyPath)
	if err != nil {
		log.Fatalf("load TLS key pair (%s, %s): %v", tlsCertPath, tlsKeyPath, err)
	}

	srv := &fixtureServer{
		issuerURL:          issuerURL,
		expectedClientID:   clientID,
		expectedClientPass: clientCredentialValue,
		expectedBearer:     accessMarker,
	}

	grpcTLSConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
	grpcListener, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		log.Fatalf("listen gRPC on %s: %v", grpcAddr, err)
	}
	grpcServer := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(grpcTLSConfig)),
		grpc.UnaryInterceptor(srv.unaryAuthInterceptor),
	)
	privatev1.RegisterStorageTiersServer(grpcServer, srv)
	privatev1.RegisterStorageBackendsServer(grpcServer, &storageBackendsAdapter{srv: srv})
	privatev1.RegisterSecretsServer(grpcServer, &secretsAdapter{srv: srv})
	privatev1.RegisterTenantsServer(grpcServer, &tenantsAdapter{})
	privatev1.RegisterNetworkClassesServer(grpcServer, &networkClassesAdapter{})
	healthpb.RegisterHealthServer(grpcServer, health.NewServer())

	oidcTLSConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
	oidcListener, err := tls.Listen("tcp", oidcAddr, oidcTLSConfig)
	if err != nil {
		log.Fatalf("listen OIDC TLS on %s: %v", oidcAddr, err)
	}
	oidcHTTPServer := &http.Server{
		Handler:           srv.oidcHandler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	httpListener, err := net.Listen("tcp", httpAddr)
	if err != nil {
		log.Fatalf("listen HTTP on %s: %v", httpAddr, err)
	}
	adminHTTPServer := &http.Server{
		Handler:           srv.adminAndAAPHandler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 3)
	go func() {
		log.Printf("starting TLS gRPC server on %s", grpcAddr)
		if serveErr := grpcServer.Serve(grpcListener); serveErr != nil {
			errCh <- fmt.Errorf("gRPC server: %w", serveErr)
		}
	}()
	go func() {
		log.Printf("starting TLS OIDC server on %s (issuer=%s)", oidcAddr, issuerURL)
		if serveErr := oidcHTTPServer.Serve(oidcListener); serveErr != nil && serveErr != http.ErrServerClosed {
			errCh <- fmt.Errorf("OIDC server: %w", serveErr)
		}
	}()
	go func() {
		log.Printf("starting HTTP AAP + admin server on %s", httpAddr)
		if serveErr := adminHTTPServer.Serve(httpListener); serveErr != nil && serveErr != http.ErrServerClosed {
			errCh <- fmt.Errorf("HTTP server: %w", serveErr)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		log.Printf("received signal %v, shutting down", sig)
	case serveErr := <-errCh:
		log.Fatalf("server failed: %v", serveErr)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = adminHTTPServer.Shutdown(shutdownCtx)
	_ = oidcHTTPServer.Shutdown(shutdownCtx)
	grpcServer.GracefulStop()
}
