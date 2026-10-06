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

	"github.com/osac-project/osac/fulfillment-service/internal/packages"
)

// contextKey is the type used to store the settings in the context.
type contextKey int

const (
	contextSettingsKey contextKey = iota
	contextTenantKey
)

// SettingsFromContext returns the settings from the context. It panics if the given context doesn't contain settings.
func SettingsFromContext(ctx context.Context) *Settings {
	settings := ctx.Value(contextSettingsKey).(*Settings)
	if settings == nil {
		panic("failed to get settings from context")
	}
	return settings
}

// TrySettingsFromContext returns the settings from the context if they are present, or nil if not.
func TrySettingsFromContext(ctx context.Context) *Settings {
	settings, _ := ctx.Value(contextSettingsKey).(*Settings)
	return settings
}

// PackageNamesFromContext returns the active package names from the context, falling back to the public packages
// when the settings are not available (for example, before login or during tests).
func PackageNamesFromContext(ctx context.Context) []string {
	if ctx != nil {
		if cfg := TrySettingsFromContext(ctx); cfg != nil {
			return cfg.PackageNames()
		}
	}
	return packages.Public
}

// SettingsIntoContext creates a new context that contains the given settings.
func SettingsIntoContext(ctx context.Context, settings *Settings) context.Context {
	return context.WithValue(ctx, contextSettingsKey, settings)
}

// TenantFromContext returns the resolved tenant from the context. Returns an empty string if no tenant is set.
func TenantFromContext(ctx context.Context) string {
	v, _ := ctx.Value(contextTenantKey).(string)
	return v
}

// TenantIntoContext creates a new context that contains the given tenant.
func TenantIntoContext(ctx context.Context, tenant string) context.Context {
	return context.WithValue(ctx, contextTenantKey, tenant)
}
