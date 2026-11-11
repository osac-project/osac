/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package it

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	. "github.com/onsi/gomega"
)

func TestUserDeletionKeycloakErrorOutput(t *testing.T) {
	const testEndpoint = "https://redaction.example.invalid/users/fixture-user"
	const fixtureDetail = "fixture-sensitive-detail"
	fixtureError := fmt.Errorf("failed to send request: %w", &url.Error{
		Op: "Get", URL: testEndpoint, Err: errors.New(fixtureDetail),
	})
	for _, tc := range []struct {
		name     string
		err      error
		failures int
	}{
		{name: "successful request"},
		{name: "wrapped HTTP failure", err: fixtureError, failures: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var messages []string
			g := NewGomega(func(message string, _ ...int) {
				messages = append(messages, message)
			})
			g.Expect(userDeletionKeycloakError(tc.err)).ToNot(HaveOccurred())
			if len(messages) != tc.failures {
				t.Fatal("unexpected assertion failure count")
			}
			for _, message := range messages {
				if strings.Contains(message, testEndpoint) || strings.Contains(message, fixtureDetail) {
					t.Fatal("request details reached assertion output")
				}
				if !strings.Contains(message, "Keycloak admin request failed") {
					t.Fatal("assertion output omitted the safe diagnostic")
				}
			}
		})
	}
}
