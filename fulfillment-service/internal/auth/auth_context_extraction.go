/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package auth

import (
	"fmt"

	"github.com/golang-jwt/jwt/v5"
)

// ExtractAuthContext extracts an AuthContext from the validated JWT token.
// This preserves all identity claims needed for OPA policy evaluation.
// This function is used by both GrpcAuthzInterceptor (for actual operations) and
// SelfSubjectAccessReviews.Create (for hypothetical permission checks) to ensure
// consistent identity extraction.
func ExtractAuthContext(token *jwt.Token) (*AuthContext, error) {
	// Get the claims from the token:
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("unexpected claims type")
	}

	// Check if this the token corresponds to a Kubernetes service account:
	_, kube := claims["kubernetes.io"]

	authContext := &AuthContext{}

	if kube {
		authContext.AuthMethod = "serviceaccount"
		authContext.Username, _ = claims["sub"].(string)
		authContext.Groups = claimAsAnySlice(claims, "groups")
	} else {
		authContext.AuthMethod = "jwt"

		username, _ := claims["preferred_username"].(string)
		if username == "" {
			username, _ = claims["username"].(string)
		}
		authContext.Username = username
		authContext.Groups = claimAsAnySlice(claims, "groups")

		// Handle organization claim - can be array or object
		if orgValue := claims["organization"]; orgValue != nil {
			if orgArray := claimAsAnySlice(claims, "organization"); orgArray != nil {
				authContext.Organization = orgArray
			} else if orgObj, ok := orgValue.(map[string]any); ok {
				authContext.Organization = orgObj
			}
		}

		authContext.Organizations = claimAsAnySlice(claims, "organizations")

		if realmAccess, ok := claims["realm_access"].(map[string]any); ok {
			authContext.RealmAccess = realmAccess
		}
	}

	return authContext, nil
}

// claimAsAnySlice extracts a JWT claim as a slice of any. Returns nil if the claim doesn't exist or isn't a slice.
func claimAsAnySlice(claims jwt.MapClaims, name string) []any {
	value, ok := claims[name]
	if !ok || value == nil {
		return nil
	}
	switch v := value.(type) {
	case []any:
		return v
	case []string:
		result := make([]any, len(v))
		for i, s := range v {
			result[i] = s
		}
		return result
	default:
		return nil
	}
}
