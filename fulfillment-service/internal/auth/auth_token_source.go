/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package auth

import (
	"context"
)

// TokenSource is the interface that defines the methods that are common to all the token sources.
//
//go:generate mockgen -source=auth_token_source.go -destination=auth_token_source_mock.go -package=auth TokenSource
type TokenSource interface {
	// Token returns the token for the token source.
	Token(ctx context.Context) (result *Token, err error)

	// Invalidate clears any cached tokens, forcing a fresh token to be generated on the next Token() call.
	// This is useful when the token needs to be refreshed due to external changes (e.g., new realm creation).
	Invalidate(ctx context.Context) error
}
