/*
Copyright (c) 2026 Red Hat Inc.

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

package servers

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/osac-project/osac/fulfillment-service/internal/gitops"
)

const defaultModelsGitReadMaxAge = 10 * time.Second

func newModelsGitWriterFromEnv() (gitops.Writer, error) {
	config := gitops.RepoConfig{
		URL:         os.Getenv("AI_GRID_GIT_URL"),
		Branch:      envOrDefault("AI_GRID_GIT_BRANCH", "main"),
		AuthorName:  "osac-fulfillment-service",
		AuthorEmail: "osac-fulfillment-service@noreply.invalid",
	}
	tokenFile := os.Getenv("AI_GRID_GIT_TOKEN_FILE")
	if config.URL == "" {
		if tokenFile != "" {
			return nil, errors.New("AI_GRID_GIT_TOKEN_FILE is set but AI_GRID_GIT_URL is not")
		}
		return gitops.Unconfigured{}, nil
	}
	u, err := url.Parse(config.URL)
	if err != nil || u.User != nil {
		return nil, errors.New("AI_GRID_GIT_URL must be a URL without credentials")
	}
	if tokenFile != "" {
		token, err := os.ReadFile(tokenFile) // #nosec G304 G703 -- path is trusted deployment configuration
		if err != nil {
			return nil, fmt.Errorf("read AI_GRID_GIT_TOKEN_FILE: %w", err)
		}
		config.Token = strings.TrimSpace(string(token))
	}
	readMaxAge := os.Getenv("AI_GRID_GIT_READ_MAX_AGE")
	if readMaxAge != "" {
		config.ReadMaxAge, err = time.ParseDuration(readMaxAge)
		if err != nil || config.ReadMaxAge < 0 {
			return nil, fmt.Errorf("AI_GRID_GIT_READ_MAX_AGE %q must be a non-negative duration", readMaxAge)
		}
	} else {
		config.ReadMaxAge = defaultModelsGitReadMaxAge
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return gitops.StartBackgroundWriter(context.Background(), func(ctx context.Context) (gitops.Writer, error) {
		return gitops.NewRepoWriter(ctx, config)
	}, time.Second), nil
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
