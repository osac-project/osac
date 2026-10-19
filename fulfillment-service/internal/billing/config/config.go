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

package config

import (
	"errors"
	"os"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/pflag"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/billing/providers/m360"
)

// Config contains optional billing-provider settings shared by the Fulfillment
// API server and controller processes.
type Config struct {
	ProviderKey    string
	M360APIURL     string
	M360APIVersion string
	M360APIKeyFile string
}

// AddFlags registers the optional billing-provider settings.
func (c *Config) AddFlags(flags *pflag.FlagSet) {
	flags.StringVar(&c.ProviderKey, "billing-provider", "", "Billing provider key; empty disables billing")
	flags.StringVar(&c.M360APIURL, "billing-m360-api-url", "", "M360 external API base URL")
	flags.StringVar(&c.M360APIVersion, "billing-m360-api-version", "", "M360 external API version")
	flags.StringVar(&c.M360APIKeyFile, "billing-m360-api-key-file", "", "Path to a mounted file containing the M360 API key")
}

// Build validates the optional settings and constructs the selected provider.
func (c Config) Build(registerers ...prometheus.Registerer) (string, billing.Provider, error) {
	if c.ProviderKey == "" {
		if c.M360APIURL != "" || c.M360APIVersion != "" || c.M360APIKeyFile != "" {
			return "", nil, errors.New("M360 settings require billing-provider=m360")
		}
		return "", nil, nil
	}
	if c.ProviderKey != "m360" {
		return "", nil, errors.New("unsupported billing provider")
	}
	if strings.TrimSpace(c.M360APIURL) == "" || strings.TrimSpace(c.M360APIVersion) == "" ||
		strings.TrimSpace(c.M360APIKeyFile) == "" {
		return "", nil, errors.New("M360 API URL, API version, and API key file are required")
	}
	key, err := os.ReadFile(c.M360APIKeyFile)
	if err != nil {
		return "", nil, errors.New("failed to read M360 API key file")
	}
	var registerer prometheus.Registerer
	if len(registerers) > 0 {
		registerer = registerers[0]
	}
	provider, err := m360.NewClient(m360.Config{
		BaseURL: c.M360APIURL, APIVersion: c.M360APIVersion,
		APIKey: strings.TrimSpace(string(key)), MetricsRegisterer: registerer,
	})
	if err != nil {
		return "", nil, err
	}
	return c.ProviderKey, provider, nil
}
