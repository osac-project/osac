/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package tokenutil

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/osac-project/fulfillment-service/internal/logging"
	"github.com/osac-project/fulfillment-service/internal/terminal"
)

// ParseTokenClaims parses a JWT token string and returns the claims without verification.
// This is useful for displaying token information to the user.
func ParseTokenClaims(tokenString string) (jwt.MapClaims, error) {
	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	parsedToken, _, err := parser.ParseUnverified(tokenString, jwt.MapClaims{})
	if err != nil {
		return nil, err
	}

	claims, ok := parsedToken.Claims.(jwt.MapClaims)
	if !ok {
		return nil, nil
	}

	return claims, nil
}

// DisplayTokenClaims displays decoded token claims to the console as JSON.
// If rfc3339 is true, time claims (iat, exp, nbf, auth_time) are converted to RFC3339 format.
// If utc is true, time claims are displayed in UTC timezone.
func DisplayTokenClaims(ctx context.Context, console *terminal.Console, claims jwt.MapClaims, rfc3339, utc bool) {
	if rfc3339 {
		logger := logging.LoggerFromContext(ctx)
		claims = replaceTimeClaims(ctx, logger, claims, utc)
	}
	console.RenderJson(ctx, claims)
}

// replaceTimeClaims converts time claims to RFC3339 format for better readability.
func replaceTimeClaims(ctx context.Context, logger *slog.Logger, claims jwt.MapClaims, utc bool) jwt.MapClaims {
	result := jwt.MapClaims{}
	for name, value := range claims {
		switch name {
		case "iat", "exp", "nbf", "auth_time":
			result[name] = replaceTimeClaim(ctx, logger, name, value, utc)
		default:
			result[name] = value
		}
	}
	return result
}

// replaceTimeClaim converts a single time claim from Unix timestamp to RFC3339 format.
func replaceTimeClaim(ctx context.Context, logger *slog.Logger, name string, value any, utc bool) any {
	// Handle different numeric types
	var seconds int64
	switch v := value.(type) {
	case json.Number:
		var err error
		seconds, err = v.Int64()
		if err != nil {
			logger.ErrorContext(
				ctx,
				"Failed to parse claim as seconds",
				slog.String("name", name),
				slog.Any("error", err),
			)
			return value
		}
	case float64:
		seconds = int64(v)
	case int64:
		seconds = v
	default:
		return value
	}

	t := time.Unix(seconds, 0)
	if utc {
		t = t.UTC()
	}
	return t.Format(time.RFC3339)
}
