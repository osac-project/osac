/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/zalando/go-keyring"
)

// SecretStore is the interface for loading and saving opaque secret data. There are two implementations: one backed
// by the operating system keyring and one backed by a JSON file. The keyring implementation is preferred when the
// operating system supports it, falling back to the file-based implementation otherwise.
type SecretStore interface {
	// Load deserializes the secret data previously stored into the given object. If nothing has been stored yet
	// the object is left unchanged.
	Load(ctx context.Context, object any) error

	// Save serializes the given object and writes it to the store. Passing nil clears all data from the store.
	Save(ctx context.Context, object any) error
}

// SecretStoreBuilder contains the data and logic needed to build a secret store that automatically selects the best
// available backend. Don't create instances of this type directly, use the NewSecretStore function instead.
type SecretStoreBuilder struct {
	logger *slog.Logger
	dir    string
}

// NewSecretStore creates a builder that can then be used to configure and create a secret store. The resulting store
// will be backed by the operating system keyring if available, falling back to a file-based store otherwise.
func NewSecretStore() *SecretStoreBuilder {
	return &SecretStoreBuilder{}
}

// SetLogger sets the logger that will be used by the secret store. This is mandatory.
func (b *SecretStoreBuilder) SetLogger(value *slog.Logger) *SecretStoreBuilder {
	b.logger = value
	return b
}

// SetDir sets the directory where the settings are stored. This is mandatory. It affects both the file-based fallback
// store (which writes to this directory) and the keyring-backed store (which uses the directory path to scope its
// keyring key, ensuring that independent configurations do not collide).
func (b *SecretStoreBuilder) SetDir(value string) *SecretStoreBuilder {
	b.dir = value
	return b
}

// Build uses the data stored in the builder to create and configure a new secret store. It probes the operating system
// keyring by attempting to read the secrets key: if the call succeeds or returns ErrNotFound the keyring is usable;
// any other error indicates the backend is not available and a file-based store is returned instead.
func (b *SecretStoreBuilder) Build() (result SecretStore, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}

	// Select the store based on the availability of the keyring:
	_, probeErr := keyring.Get(keyringService, keyringKey)
	if probeErr == nil || errors.Is(probeErr, keyring.ErrNotFound) {
		result, err = NewKeyringSecretStore().
			SetLogger(b.logger).
			SetDir(b.dir).
			Build()
	} else {
		result, err = NewFileSecretStore().
			SetLogger(b.logger).
			SetDir(b.dir).
			Build()
	}

	return
}

// KeyringSecretStoreBuilder contains the data and logic needed to build a secret store backed by the operating system
// keyring. Don't create instances of this type directly, use the NewKeyringSecretStore function instead.
type KeyringSecretStoreBuilder struct {
	logger *slog.Logger
	dir    string
}

// NewKeyringSecretStore creates a builder that can then be used to configure and create a keyring-backed secret store.
func NewKeyringSecretStore() *KeyringSecretStoreBuilder {
	return &KeyringSecretStoreBuilder{}
}

// SetLogger sets the logger that will be used by the secret store. This is mandatory.
func (b *KeyringSecretStoreBuilder) SetLogger(value *slog.Logger) *KeyringSecretStoreBuilder {
	b.logger = value
	return b
}

// SetDir sets the configuration directory. The keyring key is scoped to this directory so that independent
// configurations stored in different directories do not share secrets. This is mandatory.
func (b *KeyringSecretStoreBuilder) SetDir(value string) *KeyringSecretStoreBuilder {
	b.dir = value
	return b
}

// Build uses the data stored in the builder to create and configure a new keyring-backed secret store.
func (b *KeyringSecretStoreBuilder) Build() (result SecretStore, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.dir == "" {
		err = errors.New("directory is mandatory")
		return
	}

	// The keyring key is always 'secrets:<dir>' where 'dir' is the absolute path of the configuration directory.
	key := keyringKey + ":" + b.dir

	// Create the store:
	result = &keyringSecretStore{
		logger: b.logger,
		key:    key,
	}
	return
}

// keyringSecretStore stores secret data as a single entry in the operating system keyring. The key field is
// computed at build time and varies depending on the configuration directory, so that independent configurations
// do not share secrets.
type keyringSecretStore struct {
	logger *slog.Logger
	key    string
}

func (s *keyringSecretStore) Load(ctx context.Context, object any) error {
	data, err := keyring.Get(keyringService, s.key)
	if errors.Is(err, keyring.ErrNotFound) {
		s.logger.DebugContext(
			ctx,
			"No secrets found in keyring",
		)
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to read from keyring: %w", err)
	}
	err = json.Unmarshal([]byte(data), object)
	if err != nil {
		return fmt.Errorf("failed to parse keyring data: %w", err)
	}
	s.logger.DebugContext(
		ctx,
		"Loaded secrets from keyring",
		slog.String("service", keyringService),
		slog.String("key", s.key),
		slog.Any("!settings", object),
	)
	return nil
}

func (s *keyringSecretStore) Save(ctx context.Context, object any) error {
	if object == nil {
		err := keyring.Delete(keyringService, s.key)
		if err != nil && !errors.Is(err, keyring.ErrNotFound) {
			return fmt.Errorf("failed to delete from keyring: %w", err)
		}
		s.logger.DebugContext(
			ctx,
			"Cleared secrets from keyring",
		)
		return nil
	}
	data, err := json.MarshalIndent(object, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal secrets: %w", err)
	}
	err = keyring.Set(keyringService, s.key, string(data))
	if err != nil {
		return fmt.Errorf("failed to write to keyring: %w", err)
	}
	s.logger.DebugContext(
		ctx,
		"Saved secrets to keyring",
		slog.String("service", keyringService),
		slog.String("key", s.key),
		slog.Any("!settings", object),
	)
	return nil
}

// FileSecretStoreBuilder contains the data and logic needed to build a secret store backed by a file. Don't create
// instances of this type directly, use the NewFileSecretStore function instead.
type FileSecretStoreBuilder struct {
	logger *slog.Logger
	dir    string
}

// NewFileSecretStore creates a builder that can then be used to configure and create a file-backed secret store.
func NewFileSecretStore() *FileSecretStoreBuilder {
	return &FileSecretStoreBuilder{}
}

// SetLogger sets the logger that will be used by the secret store. This is mandatory.
func (b *FileSecretStoreBuilder) SetLogger(value *slog.Logger) *FileSecretStoreBuilder {
	b.logger = value
	return b
}

// SetDir sets the directory where the secrets file will be written. When not set, the default user configuration
// directory is used.
func (b *FileSecretStoreBuilder) SetDir(value string) *FileSecretStoreBuilder {
	b.dir = value
	return b
}

// Build uses the data stored in the builder to create and configure a new file-backed secret store.
func (b *FileSecretStoreBuilder) Build() (result SecretStore, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}

	// Use the default directory if not set:
	dir := b.dir
	if dir == "" {
		var configDir string
		configDir, err = os.UserConfigDir()
		if err != nil {
			return
		}
		dir = filepath.Join(configDir, "osac")
	}
	file := filepath.Join(dir, "secrets.json")

	// Create the store:
	result = &fileSecretStore{
		logger: b.logger,
		file:   file,
	}
	return
}

// fileSecretStore stores secret data as a file named secrets.json in the same directory as the main configuration
// file. This is used as a fallback when the operating system keyring is not available.
type fileSecretStore struct {
	logger *slog.Logger
	file   string
}

func (s *fileSecretStore) Load(ctx context.Context, object any) error {
	data, err := os.ReadFile(s.file)
	if os.IsNotExist(err) {
		s.logger.DebugContext(
			ctx,
			"No secrets file found",
			slog.String("file", s.file),
		)
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to read secrets file '%s': %w", s.file, err)
	}
	err = json.Unmarshal(data, object)
	if err != nil {
		return fmt.Errorf("failed to parse secrets file '%s': %w", s.file, err)
	}
	s.logger.DebugContext(
		ctx,
		"Loaded secrets from file",
		slog.String("file", s.file),
		slog.Any("!settings", object),
	)
	return nil
}

func (s *fileSecretStore) Save(ctx context.Context, object any) error {
	if object == nil {
		err := os.Remove(s.file)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove secrets file '%s': %w", s.file, err)
		}
		s.logger.DebugContext(
			ctx,
			"Cleared secrets file",
			slog.String("file", s.file),
		)
		return nil
	}
	data, err := json.MarshalIndent(object, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal secrets: %w", err)
	}
	dir := filepath.Dir(s.file)
	err = os.MkdirAll(dir, os.FileMode(0755))
	if err != nil {
		return fmt.Errorf("failed to create directory '%s': %w", dir, err)
	}
	err = os.WriteFile(s.file, data, 0600)
	if err != nil {
		return fmt.Errorf("failed to write secrets file '%s': %w", s.file, err)
	}
	s.logger.DebugContext(
		ctx,
		"Saved secrets to file",
		slog.String("file", s.file),
		slog.Any("!settings", object),
	)
	return nil
}

// Keyring service and key used to store all secret data as a single entry in the operating system keyring.
const (
	keyringService = "osac"
	keyringKey     = "secrets"
)
