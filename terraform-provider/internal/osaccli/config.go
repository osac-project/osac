//
// Copyright (c) 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
// the License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

// Package osaccli reads the configuration written by the `osac login` command of the OSAC CLI, so that the Terraform
// provider can reuse the credentials of an existing CLI session instead of implementing its own login flow.
//
// The structures and file formats in this package mirror
// fulfillment-service/internal/config, which is the authoritative implementation. That package is internal to the
// fulfillment-service module and therefore can't be imported here; keep the JSON tags in sync when the CLI formats
// change.
package osaccli

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/zalando/go-keyring"
)

// Keyring service and key prefix used by the OSAC CLI to store all secret data as a single entry in the operating
// system keyring. The key is the prefix followed by the absolute path of the configuration directory, so that
// independent configurations don't collide. Older CLI versions used the prefix alone as the key, so it is used as a
// fallback when reading. Must match the values in fulfillment-service/internal/config.
const (
	KeyringService   = "osac"
	keyringKeyPrefix = "secrets"
)

// GeneralSettings mirrors the non-secret fields that `osac login` writes to config.json.
type GeneralSettings struct {
	TokenScript string   `json:"token_script,omitempty"`
	Plaintext   bool     `json:"plaintext,omitempty"`
	Insecure    bool     `json:"insecure,omitempty"`
	CaFiles     []CaFile `json:"ca_files,omitempty"`
	Address     string   `json:"address,omitempty"`
	Private     bool     `json:"private,omitempty"`
	Flow        string   `json:"flow,omitempty"`
	Issuer      string   `json:"issuer,omitempty"`
	Scopes      []string `json:"scopes,omitempty"`
	RedirectUri string   `json:"redirect_uri,omitempty"`
	Tenant      string   `json:"tenant,omitempty"`
	Project     string   `json:"project,omitempty"`
}

// SecretSettings mirrors the secret fields that `osac login` writes to the operating system keyring, with a
// secrets.json file fallback when no keyring is available.
type SecretSettings struct {
	AccessToken  string    `json:"access_token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenExpiry  time.Time `json:"token_expiry,omitempty"`
	ClientId     string    `json:"client_id,omitempty"`
	ClientSecret string    `json:"client_secret,omitempty"`
	User         string    `json:"user,omitempty"`
	Password     string    `json:"password,omitempty"`
}

// CaFile mirrors a CA certificate reference written by `osac login`: the path of the file and, when the file was a
// regular file at login time, a copy of its content.
type CaFile struct {
	Name    string `json:"name,omitempty"`
	Content string `json:"content,omitempty"`
}

// Settings is the complete configuration loaded from an OSAC CLI configuration directory.
type Settings struct {
	General GeneralSettings
	Secrets SecretSettings

	// dir is the directory the configuration was loaded from.
	dir string

	// secretBackend records where the secrets were loaded from, so that refreshed tokens are written back to the
	// same place the CLI reads them from.
	secretBackend string

	// keyringKey is the keyring key the secrets were loaded from, so that refreshed tokens are written back to the
	// same entry. It is empty when nothing was found, in which case the current CLI key format is used on write.
	keyringKey string
}

// DefaultDir returns the default OSAC CLI configuration directory, the same one the `osac` binary uses:
// os.UserConfigDir()/osac (typically ~/.config/osac on Linux).
func DefaultDir() (string, error) {
	userConfigDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to determine user configuration directory: %w", err)
	}
	return filepath.Join(userConfigDir, "osac"), nil
}

// Load reads the configuration from the given directory. The directory must have been populated by a previous
// `osac login`. Secrets are read from the operating system keyring when available, with the secrets.json file
// fallback used by the CLI when it isn't, using the same probe the CLI uses.
func Load(ctx context.Context, logger *slog.Logger, dir string) (result *Settings, err error) {
	// Load the general settings:
	generalFile := filepath.Join(dir, "config.json")
	data, err := os.ReadFile(filepath.Clean(generalFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf(
				"no OSAC configuration in '%s': run 'osac login' first", dir,
			)
		}
		return nil, fmt.Errorf("failed to read config file '%s': %w", generalFile, err)
	}
	var general GeneralSettings
	err = json.Unmarshal(data, &general)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config file '%s': %w", generalFile, err)
	}
	if general.Address == "" {
		return nil, fmt.Errorf(
			"no server address in '%s': run 'osac login' first", generalFile,
		)
	}

	// Load the secret settings, preferring the keyring the same way the CLI does:
	result = &Settings{
		General: general,
		dir:     dir,
	}
	_, probeErr := keyring.Get(KeyringService, keyringKeyPrefix)
	if probeErr == nil || errors.Is(probeErr, keyring.ErrNotFound) {
		result.secretBackend = "keyring"
		key := keyringKeyPrefix + ":" + dir
		secretData, getErr := keyring.Get(KeyringService, key)
		if errors.Is(getErr, keyring.ErrNotFound) {
			// Fall back to the key format used by older CLI versions:
			secretData, getErr = keyring.Get(KeyringService, keyringKeyPrefix)
			if getErr == nil {
				result.keyringKey = keyringKeyPrefix
			}
		} else if getErr == nil {
			result.keyringKey = key
		}
		if getErr != nil && !errors.Is(getErr, keyring.ErrNotFound) {
			return nil, fmt.Errorf("failed to read secrets from keyring: %w", getErr)
		}
		if secretData != "" {
			err = json.Unmarshal([]byte(secretData), &result.Secrets)
			if err != nil {
				return nil, fmt.Errorf("failed to parse keyring secrets: %w", err)
			}
		}
	} else {
		result.secretBackend = "file"
		secretFile := filepath.Join(dir, "secrets.json")
		secretData, readErr := os.ReadFile(filepath.Clean(secretFile))
		if readErr != nil && !os.IsNotExist(readErr) {
			return nil, fmt.Errorf("failed to read secrets file '%s': %w", secretFile, readErr)
		}
		if len(secretData) > 0 {
			err = json.Unmarshal(secretData, &result.Secrets)
			if err != nil {
				return nil, fmt.Errorf("failed to parse secrets file '%s': %w", secretFile, err)
			}
		}
	}
	logger.DebugContext(
		ctx,
		"Loaded OSAC CLI configuration",
		slog.String("dir", dir),
		slog.String("address", result.General.Address),
		slog.String("flow", result.General.Flow),
		slog.String("secret_backend", result.secretBackend),
	)
	return result, nil
}

// SaveSecrets writes the secret settings back to the backend they were loaded from, so that refreshed tokens are
// shared with the CLI. This mirrors what the CLI itself does when it refreshes tokens.
func (s *Settings) SaveSecrets(ctx context.Context, logger *slog.Logger) error {
	switch s.secretBackend {
	case "keyring":
		data, err := json.MarshalIndent(&s.Secrets, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal secrets: %w", err)
		}
		key := s.keyringKey
		if key == "" {
			key = keyringKeyPrefix + ":" + s.dir
		}
		err = keyring.Set(KeyringService, key, string(data))
		if err != nil {
			return fmt.Errorf("failed to write secrets to keyring: %w", err)
		}
	case "file":
		data, err := json.MarshalIndent(&s.Secrets, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal secrets: %w", err)
		}
		secretFile := filepath.Join(s.dir, "secrets.json")
		err = os.WriteFile(secretFile, data, 0600)
		if err != nil {
			return fmt.Errorf("failed to write secrets file '%s': %w", secretFile, err)
		}
	default:
		return fmt.Errorf("unknown secret backend '%s'", s.secretBackend)
	}
	logger.DebugContext(ctx, "Saved refreshed secrets", slog.String("backend", s.secretBackend))
	return nil
}

// CaPool builds the pool of certificate authorities that the CLI stored at login time. For each CA file the content
// saved at login time is used as a fallback, and regular files are re-read from disk so certificate rotation is
// picked up, mirroring the behavior of the CLI. When the settings have no CA files the system pool is returned.
func (s *Settings) CaPool() (*x509.CertPool, error) {
	pool, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("failed to load system certificate pool: %w", err)
	}
	for _, caFile := range s.General.CaFiles {
		added := false
		if caFile.Name != "" {
			data, readErr := os.ReadFile(filepath.Clean(caFile.Name))
			if readErr == nil {
				added = appendCertificates(pool, data)
			}
		}
		if !added && caFile.Content != "" {
			appendCertificates(pool, []byte(caFile.Content))
		}
	}
	return pool, nil
}

// appendCertificates adds all the PEM certificates contained in the given data to the pool and returns true if at
// least one was added.
func appendCertificates(pool *x509.CertPool, data []byte) bool {
	added := false
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			continue
		}
		pool.AddCert(certificate)
		added = true
	}
	return added
}

// SecretBackendIfAny returns the name of the secret backend the settings were loaded from.
func (s *Settings) Backend() string {
	return s.secretBackend
}
