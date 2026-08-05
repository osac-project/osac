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
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/golang-jwt/jwt/v5"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	"github.com/osac-project/fulfillment-service/internal/logging"
	"github.com/osac-project/fulfillment-service/internal/terminal"
)

var _ = Describe("ParseTokenClaims", func() {
	It("Parses a valid JWT token", func() {
		// Create a simple JWT token
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"sub":      "1234567890",
			"username": "testuser",
			"iat":      float64(1516239022),
		})
		tokenString, err := token.SignedString([]byte("test-secret"))
		Expect(err).ToNot(HaveOccurred())

		// Parse the token
		claims, err := ParseTokenClaims(tokenString)
		Expect(err).ToNot(HaveOccurred())
		Expect(claims).ToNot(BeNil())
		Expect(claims["sub"]).To(Equal("1234567890"))
		Expect(claims["username"]).To(Equal("testuser"))
		Expect(claims["iat"]).To(Equal(float64(1516239022)))
	})

	It("Returns error for invalid JWT token", func() {
		claims, err := ParseTokenClaims("not-a-valid-token")
		Expect(err).To(HaveOccurred())
		Expect(claims).To(BeNil())
	})

	It("Returns error for malformed JWT token", func() {
		claims, err := ParseTokenClaims("header.payload")
		Expect(err).To(HaveOccurred())
		Expect(claims).To(BeNil())
	})

	It("Returns empty claims for token with no claims", func() {
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{})
		tokenString, err := token.SignedString([]byte("test-secret"))
		Expect(err).ToNot(HaveOccurred())

		claims, err := ParseTokenClaims(tokenString)
		Expect(err).ToNot(HaveOccurred())
		Expect(claims).ToNot(BeNil())
		Expect(claims).To(BeEmpty())
	})
})

var _ = Describe("DisplayTokenClaims", func() {
	var (
		ctx     context.Context
		console *terminal.Console
		output  *bytes.Buffer
	)

	BeforeEach(func() {
		ctx = logging.LoggerIntoContext(context.Background(), slog.Default())
		output = &bytes.Buffer{}
		var err error
		console, err = terminal.NewConsole().
			SetLogger(slog.Default()).
			SetStdout(output).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	parseOutput := func() jwt.MapClaims {
		var result jwt.MapClaims
		err := json.Unmarshal(output.Bytes(), &result)
		Expect(err).ToNot(HaveOccurred())
		return result
	}

	It("Displays claims without RFC3339 conversion by default", func() {
		claims := jwt.MapClaims{
			"iat":      float64(1609459200),
			"username": "testuser",
		}

		DisplayTokenClaims(ctx, console, claims, false, false)

		result := parseOutput()
		Expect(result["iat"]).To(Equal(float64(1609459200)))
		Expect(result["username"]).To(Equal("testuser"))
	})

	It("Converts iat claim to RFC3339 when rfc3339 is true", func() {
		claims := jwt.MapClaims{
			"iat":      float64(1609459200), // 2021-01-01 00:00:00 UTC
			"username": "testuser",
		}

		DisplayTokenClaims(ctx, console, claims, true, true)

		result := parseOutput()
		Expect(result["iat"]).To(BeAssignableToTypeOf(""))
		Expect(result["iat"]).To(ContainSubstring("2021-01-01"))
		Expect(result["username"]).To(Equal("testuser"))
	})

	It("Converts multiple time claims to RFC3339", func() {
		claims := jwt.MapClaims{
			"iat":       float64(1609459200),
			"exp":       float64(1609545600),
			"nbf":       float64(1609459200),
			"auth_time": float64(1609459200),
			"username":  "testuser",
		}

		DisplayTokenClaims(ctx, console, claims, true, true)

		result := parseOutput()
		Expect(result["iat"]).To(ContainSubstring("2021-01-01"))
		Expect(result["exp"]).To(ContainSubstring("2021-01-02"))
		Expect(result["nbf"]).To(ContainSubstring("2021-01-01"))
		Expect(result["auth_time"]).To(ContainSubstring("2021-01-01"))
		Expect(result["username"]).To(Equal("testuser"))
	})

	It("Converts time to UTC when utc flag is true", func() {
		claims := jwt.MapClaims{
			"iat": float64(1609459200), // 2021-01-01 00:00:00 UTC
		}

		DisplayTokenClaims(ctx, console, claims, true, true)

		result := parseOutput()
		timestamp := result["iat"].(string)
		parsedTime, err := time.Parse(time.RFC3339, timestamp)
		Expect(err).ToNot(HaveOccurred())
		Expect(parsedTime.Location()).To(Equal(time.UTC))
	})

	It("Leaves non-time claims unchanged", func() {
		claims := jwt.MapClaims{
			"username": "testuser",
			"sub":      "1234567890",
			"roles":    []interface{}{"admin", "user"},
		}

		DisplayTokenClaims(ctx, console, claims, true, true)

		result := parseOutput()
		Expect(result["username"]).To(Equal("testuser"))
		Expect(result["sub"]).To(Equal("1234567890"))
		Expect(result["roles"]).To(HaveLen(2))
	})

	It("Handles non-numeric time claims gracefully", func() {
		claims := jwt.MapClaims{
			"iat":      "not-a-number",
			"username": "testuser",
		}

		DisplayTokenClaims(ctx, console, claims, true, true)

		result := parseOutput()
		Expect(result["iat"]).To(Equal("not-a-number"))
		Expect(result["username"]).To(Equal("testuser"))
	})

	It("Preserves all claims in the output", func() {
		claims := jwt.MapClaims{
			"iat":      float64(1609459200),
			"exp":      float64(1609545600),
			"username": "testuser",
			"sub":      "1234567890",
			"roles":    []interface{}{"admin"},
		}

		DisplayTokenClaims(ctx, console, claims, true, true)

		result := parseOutput()
		Expect(result).To(HaveLen(len(claims)))
	})

	It("Handles json.Number values for time claims with RFC3339 conversion", func() {
		claims := jwt.MapClaims{
			"iat":      json.Number("1609459200"), // 2021-01-01 00:00:00 UTC
			"exp":      json.Number("1609545600"), // 2021-01-02 00:00:00 UTC
			"username": "testuser",
			"sub":      "1234567890",
		}

		DisplayTokenClaims(ctx, console, claims, true, true)

		result := parseOutput()
		Expect(result["iat"]).To(BeAssignableToTypeOf(""))
		Expect(result["iat"]).To(ContainSubstring("2021-01-01"))
		Expect(result["exp"]).To(BeAssignableToTypeOf(""))
		Expect(result["exp"]).To(ContainSubstring("2021-01-02"))
		Expect(result["username"]).To(Equal("testuser"))
		Expect(result["sub"]).To(Equal("1234567890"))
	})
})
