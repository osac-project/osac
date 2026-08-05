/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package jwe

import (
	"context"
	"crypto/rsa"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
)

// certKeyReloader watches a PEM certificate + key pair and reloads when
// either file's mtime changes. Used by the Sealer (signing key) and
// encryption key loader.
type certKeyReloader struct {
	logger      *slog.Logger
	certFile    string
	keyFile     string // empty for public-key-only use (encryption cert)
	mu          sync.Mutex
	certModTime time.Time
	keyModTime  time.Time
	publicKey   *rsa.PublicKey
	privateKey  *rsa.PrivateKey // nil for public-key-only use
	jwkKey      jwk.Key         // JWK representation of the public key
}

// ensureLoaded checks the cert and key file mtimes and reloads if changed.
func (r *certKeyReloader) ensureLoaded(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Phase A: stat both files, decide if reload is needed.
	certInfo, err := os.Stat(r.certFile)
	if err != nil {
		return fmt.Errorf("failed to stat cert file %q: %w", r.certFile, err)
	}
	certChanged := r.certModTime.IsZero() || certInfo.ModTime().After(r.certModTime)

	var keyInfo os.FileInfo
	keyChanged := false
	if r.keyFile != "" {
		keyInfo, err = os.Stat(r.keyFile)
		if err != nil {
			return fmt.Errorf("failed to stat key file %q: %w", r.keyFile, err)
		}
		keyChanged = r.keyModTime.IsZero() || keyInfo.ModTime().After(r.keyModTime)
	}

	if !certChanged && !keyChanged {
		return nil
	}

	// Phase B: parse everything into local variables.
	r.logger.InfoContext(ctx, "Loading certificate",
		slog.String("cert_file", r.certFile),
		slog.Time("mtime", certInfo.ModTime()),
	)

	certPEM, err := os.ReadFile(r.certFile)
	if err != nil {
		return fmt.Errorf("failed to read cert file %q: %w", r.certFile, err)
	}

	cert, err := parseCertificate(certPEM)
	if err != nil {
		return fmt.Errorf("failed to parse certificate from %q: %w", r.certFile, err)
	}

	pubKey, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return fmt.Errorf("certificate public key is not RSA (got %T)", cert.PublicKey)
	}

	// Build JWK from the public key with kid = thumbprint.
	k, err := jwk.Import(pubKey)
	if err != nil {
		return fmt.Errorf("failed to create JWK from public key: %w", err)
	}
	if err := jwk.AssignKeyID(k); err != nil {
		return fmt.Errorf("failed to assign kid to JWK: %w", err)
	}
	if err := k.Set(jwk.AlgorithmKey, jwa.RS256().String()); err != nil {
		return fmt.Errorf("failed to set alg on JWK: %w", err)
	}
	if err := k.Set(jwk.KeyUsageKey, "sig"); err != nil {
		return fmt.Errorf("failed to set use on JWK: %w", err)
	}

	// Load private key (Sealer only).
	var privKey *rsa.PrivateKey
	if r.keyFile != "" {
		keyPEM, err := os.ReadFile(r.keyFile)
		if err != nil {
			return fmt.Errorf("failed to read key file %q: %w", r.keyFile, err)
		}
		privKey, err = parsePrivateKey(keyPEM)
		if err != nil {
			return fmt.Errorf("failed to parse private key from %q: %w", r.keyFile, err)
		}
		if !privKey.PublicKey.Equal(pubKey) {
			return fmt.Errorf("private key does not match certificate public key from %q", r.certFile)
		}
	}

	// Phase C: all parsing succeeded -- swap fields atomically.
	r.publicKey = pubKey
	r.jwkKey = k
	r.privateKey = privKey
	r.certModTime = certInfo.ModTime()
	if keyInfo != nil {
		r.keyModTime = keyInfo.ModTime()
	}
	return nil
}

// privateKeyReloader watches a PEM private key file and reloads when its
// mtime changes. Used by the Opener for the JWE decryption key.
type privateKeyReloader struct {
	logger     *slog.Logger
	keyFile    string
	mu         sync.Mutex
	timestamp  time.Time
	privateKey *rsa.PrivateKey
}

// ensureLoaded checks the key file's mtime and reloads if changed.
func (r *privateKeyReloader) ensureLoaded(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	info, err := os.Stat(r.keyFile)
	if err != nil {
		return fmt.Errorf("failed to stat key file %q: %w", r.keyFile, err)
	}

	if !r.timestamp.IsZero() && !info.ModTime().After(r.timestamp) {
		return nil
	}

	r.logger.InfoContext(ctx, "Loading private key",
		slog.String("key_file", r.keyFile),
		slog.Time("mtime", info.ModTime()),
	)

	keyPEM, err := os.ReadFile(r.keyFile)
	if err != nil {
		return fmt.Errorf("failed to read key file %q: %w", r.keyFile, err)
	}

	privKey, err := parsePrivateKey(keyPEM)
	if err != nil {
		return fmt.Errorf("failed to parse private key from %q: %w", r.keyFile, err)
	}

	r.privateKey = privKey
	r.timestamp = info.ModTime()
	return nil
}
