/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package grpcserver

import (
	"log/slog"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/billing/m360"
)

// newBillingProvider resolves the deployment's billing state once at startup. It is the only place that reads the
// billing environment and credential file, and it makes no provider call: with no provider configured no adapter is
// built, and with one configured the adapter is only constructed here and used when a request needs it.
//
// A provider that is named but unusable, whether its settings are incomplete or its credential file is unreadable,
// stays configured and carries the error, so the status reports UNAVAILABLE rather than NOT_CONFIGURED.
func newBillingProvider(getenv func(string) string, readFile func(string) ([]byte, error),
	logger *slog.Logger) *billing.Provider {
	key := getenv("OSAC_BILLING_PROVIDER")
	if key == "" {
		return billing.NewProvider("", nil, nil)
	}
	cfg, err := billing.LoadConfig(getenv, readFile)
	if err != nil {
		// The value is raw deployment config, so log it only when it is the known key.
		logged := "unrecognized"
		if key == billing.KeyM360 {
			logged = key
		}
		logger.Warn("Billing provider is configured but unusable", slog.String("provider", logged),
			slog.String("error", err.Error()))
		return billing.NewProvider(key, nil, err)
	}
	adapter, err := m360.New(cfg).SetLogger(logger).Build()
	return billing.NewProvider(key, adapter, err)
}
