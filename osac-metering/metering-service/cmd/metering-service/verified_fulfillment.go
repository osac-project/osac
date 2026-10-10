package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/oauth"
	"google.golang.org/grpc/status"
)

var fulfillmentClientBundleObserved = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "osac_fulfillment_client_bundle_observed",
	Help: "The SHA-256 of the CA bundle last verified by the fulfillment client.",
}, []string{"sha256"})

type verifiedFulfillmentConn struct {
	address   string
	caFile    string
	tokenFile string
	mu        sync.Mutex
	closed    bool
	current   atomic.Pointer[grpc.ClientConn]
	observed  atomic.Value
}

const (
	verifiedFulfillmentStartupTimeout = 30 * time.Second
	verifiedFulfillmentRetryInterval  = time.Second
)

func waitForVerifiedFulfillment(ctx context.Context, reload func(context.Context) error) error {
	return waitForVerifiedFulfillmentWithPolicy(ctx, reload, verifiedFulfillmentStartupTimeout, verifiedFulfillmentRetryInterval)
}

func waitForVerifiedFulfillmentWithPolicy(
	ctx context.Context, reload func(context.Context) error, timeout, retryInterval time.Duration,
) error {
	connectCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var lastErr error
	for {
		lastErr = reload(connectCtx)
		if lastErr == nil {
			return nil
		}
		// Retry only connection-probe timeouts. Invalid CA data and other
		// configuration failures should fail immediately.
		if !errors.Is(lastErr, context.DeadlineExceeded) {
			return lastErr
		}
		if connectCtx.Err() != nil {
			return fmt.Errorf("fulfillment service did not become ready within %s: %w", timeout, errors.Join(connectCtx.Err(), lastErr))
		}

		timer := time.NewTimer(retryInterval)
		select {
		case <-connectCtx.Done():
			timer.Stop()
			return fmt.Errorf("fulfillment service did not become ready within %s: %w", timeout, errors.Join(connectCtx.Err(), lastErr))
		case <-timer.C:
		}
	}
}

func (v *verifiedFulfillmentConn) Invoke(ctx context.Context, method string, args, reply any, opts ...grpc.CallOption) error {
	conn := v.current.Load()
	if conn == nil {
		return status.Error(codes.Unavailable, "verified fulfillment connection is not ready")
	}
	return conn.Invoke(ctx, method, args, reply, opts...)
}

func (v *verifiedFulfillmentConn) NewStream(ctx context.Context, desc *grpc.StreamDesc, method string, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	conn := v.current.Load()
	if conn == nil {
		return nil, status.Error(codes.Unavailable, "verified fulfillment connection is not ready")
	}
	return conn.NewStream(ctx, desc, method, opts...)
}

func (v *verifiedFulfillmentConn) GetState() connectivity.State {
	if conn := v.current.Load(); conn != nil {
		return conn.GetState()
	}
	return connectivity.Shutdown
}

func (v *verifiedFulfillmentConn) observedHash() string {
	if hash := v.observed.Load(); hash != nil {
		return hash.(string)
	}
	return ""
}

func (v *verifiedFulfillmentConn) reload(ctx context.Context) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return fmt.Errorf("fulfillment client is closed")
	}

	bundle, err := os.ReadFile(v.caFile)
	if err != nil {
		return fmt.Errorf("reading fulfillment CA file: %w", err)
	}
	digest := sha256.Sum256(bundle)
	hash := hex.EncodeToString(digest[:])
	if v.current.Load() != nil && v.observedHash() == hash {
		return nil
	}
	pool, err := verifiedCAPool(bundle)
	if err != nil {
		return err
	}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
		RootCAs: pool, MinVersion: tls.VersionTLS12,
	}))}
	if v.tokenFile != "" {
		opts = append(opts, grpc.WithPerRPCCredentials(oauth.TokenSource{
			TokenSource: &fileTokenSource{tokenFile: v.tokenFile},
		}))
	}
	candidate, err := grpc.NewClient(v.address, opts...)
	if err != nil {
		return fmt.Errorf("building fulfillment client: %w", err)
	}
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	candidate.Connect()
	for candidate.GetState() != connectivity.Ready {
		state := candidate.GetState()
		if !candidate.WaitForStateChange(probeCtx, state) {
			_ = candidate.Close()
			return fmt.Errorf("verified fulfillment connection failed: %w", probeCtx.Err())
		}
	}
	previousHash := v.observedHash()
	previous := v.current.Swap(candidate)
	v.observed.Store(hash)
	if previousHash != "" {
		fulfillmentClientBundleObserved.DeleteLabelValues(previousHash)
	}
	fulfillmentClientBundleObserved.WithLabelValues(hash).Set(1)
	if previous != nil {
		_ = previous.Close()
	}
	return nil
}

func verifiedCAPool(bundle []byte) (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	count := 0
	for rest := bytes.TrimSpace(bundle); len(rest) > 0; {
		block, next := pem.Decode(rest)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, fmt.Errorf("fulfillment CA file contains invalid PEM")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil || (!cert.IsCA && cert.Version != 1) ||
			(cert.KeyUsage != 0 && cert.KeyUsage&x509.KeyUsageCertSign == 0) {
			return nil, fmt.Errorf("fulfillment CA file contains an invalid CA certificate")
		}
		pool.AddCert(cert)
		count++
		rest = bytes.TrimSpace(next)
	}
	if count == 0 {
		return nil, fmt.Errorf("fulfillment CA file has no valid certificates")
	}
	return pool, nil
}

func (v *verifiedFulfillmentConn) watch(ctx context.Context, logger logr.Logger) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			previousHash := v.observedHash()
			if err := v.reload(ctx); err != nil {
				logger.Error(err, "fulfillment CA reload failed; retaining last verified client")
				continue
			}
			if hash := v.observedHash(); hash != previousHash {
				logger.Info("verified fulfillment CA bundle", "sha256", hash)
			}
		}
	}
}

func (v *verifiedFulfillmentConn) close() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.closed = true
	if conn := v.current.Swap(nil); conn != nil {
		_ = conn.Close()
	}
}
