/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package network

import (
	"errors"
	"log/slog"
	"net/http"
	"slices"

	"github.com/gorilla/handlers"
	"github.com/spf13/pflag"
)

// CorsMiddlewareBuilder contains the data and logic needed to create A CORS middleware. Don't create instances of this
// object directly, use the NewCorsMiddleware function instead.
type CorsMiddlewareBuilder struct {
	logger         *slog.Logger
	name           string
	allowedOrigins []string
}

// NewCorsMiddleware creates a builder that can then be used to configure and create a CORS middleware.
func NewCorsMiddleware() *CorsMiddlewareBuilder {
	return &CorsMiddlewareBuilder{}
}

// SetLogger sets the logger that the middleware will use to send messages to the log. This is mandatory.
func (b *CorsMiddlewareBuilder) SetLogger(value *slog.Logger) *CorsMiddlewareBuilder {
	b.logger = value
	return b
}

// SetFlags sets the command line flags that should be used to configure the middleware.
//
// The name is used to select the options when there are multiple instances. For example, if it is 'API' then it will
// only take into accounts the flags starting with '--api'.
//
// This is optional.
func (b *CorsMiddlewareBuilder) SetFlags(flags *pflag.FlagSet, name string) *CorsMiddlewareBuilder {
	// Save the name of the listener:
	b.name = name

	// Prepare some variables and helper functions to avoid repeating code:
	if flags == nil {
		return b
	}
	var (
		flag string
		err  error
	)
	failure := func() {
		b.logger.Error(
			"Failed to get flag value",
			slog.String("flag", flag),
			slog.Any("error", err),
		)
	}

	// Address:
	flag = corsFlagName(name, corsAllowedOriginsFlagSuffix)
	if flags.Changed(flag) {
		values, err := flags.GetStringSlice(flag)
		if err != nil {
			failure()
		} else {
			b.AddAllowedOrigins(values...)
		}
	}

	return b
}

// AddAllowedOrigins adds a list of allowed origins to the CORS middleware. This is optional, and the default value is
// '*'.
func (b *CorsMiddlewareBuilder) AddAllowedOrigins(values ...string) *CorsMiddlewareBuilder {
	b.allowedOrigins = append(b.allowedOrigins, values...)
	return b
}

// Build uses the data stored in the builder to create a new CORS middleware.
func (b *CorsMiddlewareBuilder) Build() (result func(http.Handler) http.Handler, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}

	// If no allowed origins are set, use the default value:
	allowedOrigins := slices.Clone(b.allowedOrigins)
	if len(b.allowedOrigins) == 0 {
		allowedOrigins = []string{"*"}
	}
	b.logger.Info(
		"CORS configuration",
		slog.String("listener", b.name),
		slog.Any("allowed_origins", allowedOrigins),
	)

	// Create the middleware:
	result = handlers.CORS(
		handlers.AllowedOrigins(allowedOrigins),
		handlers.AllowedMethods([]string{
			http.MethodDelete,
			http.MethodGet,
			http.MethodHead,
			http.MethodPatch,
			http.MethodPost,
			http.MethodPut,
		}),
		handlers.AllowCredentials(),
		handlers.AllowedHeaders([]string{
			"Authorization",
			"Content-Type",
		}),
	)
	return
}
