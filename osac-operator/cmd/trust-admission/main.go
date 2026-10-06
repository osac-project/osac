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

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/osac-project/osac/osac-operator/internal/trustadmission"
)

func main() {
	var tenantNamespace, tlsCertFile, tlsKeyFile string
	flag.StringVar(&tenantNamespace, "tenant-namespace", "", "Namespace containing the tenant trust targets")
	flag.StringVar(&tlsCertFile, "tls-cert-file", "", "Path to the webhook TLS certificate")
	flag.StringVar(&tlsKeyFile, "tls-key-file", "", "Path to the webhook TLS private key")
	flag.Parse()

	if err := run(tenantNamespace, tlsCertFile, tlsKeyFile); err != nil {
		slog.Error("tenant trust admission service failed", "error", err)
		os.Exit(1)
	}
}

func run(tenantNamespace, tlsCertFile, tlsKeyFile string) error {
	if tenantNamespace == "" || tlsCertFile == "" || tlsKeyFile == "" {
		return fmt.Errorf("tenant namespace and TLS certificate and key files are required")
	}

	config, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("build in-cluster config: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("build Kubernetes client: %w", err)
	}
	store, err := trustadmission.NewKubernetesStore(clientset.CoreV1(), tenantNamespace)
	if err != nil {
		return fmt.Errorf("create expected-bundle store: %w", err)
	}
	handler, err := trustadmission.NewHandler(store, trustadmission.Config{TenantNamespace: tenantNamespace})
	if err != nil {
		return fmt.Errorf("create admission handler: %w", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/validate", &admission.Webhook{Handler: handler})
	mux.Handle(trustadmission.ControlPath, &trustadmission.ControlHandler{
		Store: store, Kube: clientset, Namespace: tenantNamespace,
	})
	mux.HandleFunc("/healthz", func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/readyz", func(writer http.ResponseWriter, _ *http.Request) {
		if err := store.Ready(context.Background()); err != nil {
			http.Error(writer, "protected store unavailable", http.StatusServiceUnavailable)
			return
		}
		writer.WriteHeader(http.StatusOK)
	})

	server := &http.Server{
		Addr: ":8443", Handler: mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 * 1024,
		TLSConfig:         webhookTLSConfig(tlsCertFile, tlsKeyFile),
	}
	return server.ListenAndServeTLS("", "")
}

func webhookTLSConfig(tlsCertFile, tlsKeyFile string) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			certificate, err := tls.LoadX509KeyPair(tlsCertFile, tlsKeyFile)
			if err != nil {
				return nil, fmt.Errorf("load webhook TLS key pair: %w", err)
			}
			return &certificate, nil
		},
	}
}
