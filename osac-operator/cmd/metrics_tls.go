package main

import (
	"crypto/tls"
	"fmt"
	"path/filepath"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/certwatcher"
)

// metricsCertificateTLSOpts makes the metrics server serve the certificate
// mounted from the OpenShift service-CA Secret. The chart mounts that Secret
// without optional: true, so the files exist whenever --metrics-cert-path is
// set; a missing or unreadable file is a startup error. Falling back to
// controller-runtime's self-signed certificate would leave Prometheus unable to
// verify the scrape for the life of the process, because there is no retry.
// certwatcher reloads the files on rotation, so the returned watcher must be
// added to the manager.
func metricsCertificateTLSOpts(
	certPath, certName, certKey string,
	base []func(*tls.Config),
	log logr.Logger,
) ([]func(*tls.Config), *certwatcher.CertWatcher, error) {
	if certPath == "" {
		return base, nil, nil
	}
	watcher, err := certwatcher.New(
		filepath.Join(certPath, certName),
		filepath.Join(certPath, certKey),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("load metrics serving certificate from %s: %w", certPath, err)
	}
	log.Info("using the mounted metrics serving certificate", "metrics-cert-path", certPath)
	opts := append([]func(*tls.Config){}, base...)
	opts = append(opts, func(config *tls.Config) {
		config.GetCertificate = watcher.GetCertificate
	})
	return opts, watcher, nil
}
