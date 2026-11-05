/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package m360

import (
	"log/slog"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
)

// Builder builds the billing.Adapter backed by the M360 RevOS API.
type Builder struct {
	cfg    billing.Config
	logger *slog.Logger
}

// New starts building the M360 adapter from cfg.
func New(cfg billing.Config) *Builder {
	return &Builder{cfg: cfg}
}

// SetLogger sets the adapter logger and returns the Builder.
func (b *Builder) SetLogger(value *slog.Logger) *Builder {
	b.logger = value
	return b
}

// Build fails with a NOT_CONFIGURED *billing.Error when the configuration is incomplete.
func (b *Builder) Build() (billing.Adapter, error) {
	if err := b.cfg.Validate(); err != nil {
		return nil, err
	}
	logger := b.logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &adapter{client: newClient(b.cfg, logger)}, nil
}

type adapter struct {
	client *client
}
