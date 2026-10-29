/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

// Package networking contains startup configuration for provider networking.
package networking

import (
	"log/slog"
	"os"
	"strconv"
)

const ProvisioningEnabledEnv = "OSAC_ENABLE_NETWORKING_PROVISIONING"

// Config is a startup snapshot; changing the environment requires a restart.
type Config struct {
	ProvisioningEnabled bool
}

// LoadConfig defaults missing, empty, or invalid input to true, matching the operators.
func LoadConfig(logger *slog.Logger) Config {
	config := Config{ProvisioningEnabled: true}
	if value := os.Getenv(ProvisioningEnabledEnv); value != "" {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			logger.Error("conversion failed, using default", "envVar", ProvisioningEnabledEnv,
				"default", true)
		} else {
			config.ProvisioningEnabled = enabled
		}
	}
	logger.Info("networking provisioning feature gate", "enabled", config.ProvisioningEnabled)
	return config
}
