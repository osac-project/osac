package main

import (
	"crypto/tls"
	"path/filepath"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/certwatcher"
)

// metricsCertificateTLSOpts makes the metrics server serve the certificate
// mounted from the OpenShift service-CA Secret. When the files are absent the
// operator keeps running with controller-runtime's self-signed certificate: a
// missing monitoring certificate must not stop the control plane, and the
// failed scrape is the visible signal. certwatcher reloads the files on
// rotation, so the returned watcher must be added to the manager.
func metricsCertificateTLSOpts(
	certPath, certName, certKey string,
	base []func(*tls.Config),
	log logr.Logger,
) ([]func(*tls.Config), *certwatcher.CertWatcher) {
	if certPath == "" {
		return base, nil
	}
	watcher, err := certwatcher.New(
		filepath.Join(certPath, certName),
		filepath.Join(certPath, certKey),
	)
	if err != nil {
		log.Error(err, "metrics serving certificate is unavailable; falling back to a self-signed certificate",
			"metrics-cert-path", certPath)
		return base, nil
	}
	log.Info("using the mounted metrics serving certificate", "metrics-cert-path", certPath)
	opts := append([]func(*tls.Config){}, base...)
	opts = append(opts, func(config *tls.Config) {
		config.GetCertificate = watcher.GetCertificate
	})
	return opts, watcher
}
