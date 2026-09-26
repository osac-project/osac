/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

// This file contains functions that extract information from the context.

package auth

import (
	"context"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// contextKey is the type used to store the authentication information in the context.
type contextKey int

const (
	subjectContextKey contextKey = iota
	tokenContextKey   contextKey = iota
)

// ContextWithSubject creates a new context containing the given subject.
func ContextWithSubject(parent context.Context, subject *Subject) context.Context {
	return context.WithValue(parent, subjectContextKey, subject)
}

// SubjectFromContext extracts the subject from the context. Panics if there is no subject in the
// context.
func SubjectFromContext(ctx context.Context) *Subject {
	subject := ctx.Value(subjectContextKey)
	switch subject := subject.(type) {
	case *Subject:
		return subject
	default:
		panic("failed to get subject from context")
	}
}

// IsControllerServiceAccount reports whether the authenticated caller is the
// fulfillment controller service account. This is intentionally narrower than
// IsAdmin: it is used only for controller-owned lifecycle operations that must
// remain unavailable to ordinary administrators and users.
func IsControllerServiceAccount(ctx context.Context) bool {
	subject, ok := ctx.Value(subjectContextKey).(*Subject)
	return ok && (subject.User == "service-account-osac-controller" ||
		strings.HasSuffix(subject.User, ":service-account-osac-controller"))
}

// ContextWithToken creates a new context containing the given validated JWT token.
func ContextWithToken(parent context.Context, token *jwt.Token) context.Context {
	return context.WithValue(parent, tokenContextKey, token)
}

// TokenFromContext extracts the validated JWT token from the context. Returns nil if there is no token in the context.
func TokenFromContext(ctx context.Context) *jwt.Token {
	value := ctx.Value(tokenContextKey)
	if value == nil {
		return nil
	}
	token, ok := value.(*jwt.Token)
	if !ok {
		return nil
	}
	return token
}
