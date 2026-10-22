/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package mcpserver

import (
	"errors"

	"github.com/golang-jwt/jwt/v5"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
)

const callerContextExtraKey = "caller_context"

type safeCallerContext struct {
	Subject  string   `json:"subject"`
	Username string   `json:"username,omitempty"`
	Tenants  []string `json:"tenants,omitempty"`
}

func safeCallerContextFromToken(token *jwt.Token) (safeCallerContext, error) {
	if token == nil {
		return safeCallerContext{}, errors.New("MCP caller token is missing")
	}
	subject, err := token.Claims.GetSubject()
	if err != nil || subject == "" {
		return safeCallerContext{}, errors.New("MCP caller token has no subject")
	}
	authContext, err := auth.ExtractAuthContext(token)
	if err != nil {
		return safeCallerContext{}, errors.New("MCP caller token has no safe identity context")
	}
	return safeCallerContext{
		Subject:  subject,
		Username: authContext.Username,
	}, nil
}
