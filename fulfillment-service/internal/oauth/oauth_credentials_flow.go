/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package oauth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
)

type credentialsFlow struct {
	source *TokenSource
	logger *slog.Logger
}

// maxClientSecretFileSize is the upper bound for client secret files. Secrets
// are never this large, so a bigger file indicates a configuration mistake.
const maxClientSecretFileSize = 1 << 20 // 1 MiB

// readClientSecretFile reads the file-backed client secret. It follows
// symlinks because Kubernetes projects Secret volumes as a "..data" symlink
// chain, and validates the descriptor that is actually read. Non-regular files
// are rejected and the read is bounded, so a misconfigured path cannot
// allocate unbounded memory. The pre-open Stat resolves symlinks and avoids a
// blocking open(2) for a path that is already a FIFO, while the descriptor Stat
// remains authoritative for the bytes actually read. The configured path and
// its symlink targets must be owned by trusted administrators: a path swapped
// between the checks can still block the open.
func readClientSecretFile(path string) (string, error) {
	cleanPath := filepath.Clean(path)
	info, err := os.Stat(cleanPath)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("'%s' is not a regular file", path)
	}
	file, err := os.Open(cleanPath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("'%s' is not a regular file", path)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxClientSecretFileSize+1))
	if data != nil {
		defer clear(data)
	}
	if readErr != nil {
		return "", readErr
	}
	if len(data) > maxClientSecretFileSize {
		return "", fmt.Errorf("'%s' exceeds the maximum client secret file size (%d bytes)", path, maxClientSecretFileSize)
	}
	return string(bytes.TrimSpace(data)), nil
}

func (f *credentialsFlow) run(ctx context.Context) (result *auth.Token, err error) {
	secret := f.source.clientSecret
	if path := f.source.clientSecretFile; path != "" {
		secret, err = readClientSecretFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to read client secret file: %w", err)
		}
		if secret == "" {
			return nil, errors.New("client secret file is empty")
		}
	}
	tokenRequest := tokenEndpointRequest{
		Audience:     f.source.audience,
		ClientId:     f.source.clientId,
		ClientSecret: secret,
		GrantType:    "client_credentials",
		Scope:        f.source.scopes,
	}
	tokenResponse, err := f.source.sendTokenForm(ctx, tokenRequest)
	if err != nil {
		return
	}
	result = &auth.Token{
		Access:  tokenResponse.AccessToken,
		Refresh: tokenResponse.RefreshToken,
		Expiry:  f.source.secondsToTime(tokenResponse.ExpiresIn),
	}
	return
}
