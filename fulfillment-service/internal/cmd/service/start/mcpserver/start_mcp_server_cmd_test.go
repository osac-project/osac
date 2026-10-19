/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"time"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/metadata"

	"github.com/golang-jwt/jwt/v5"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	testutils "github.com/osac-project/osac/fulfillment-service/internal/testing"
)

var _ = Describe("Cmd", func() {
	It("Has the expected use string", func() {
		cmd := Cmd()
		Expect(cmd.Use).To(Equal("mcp-server [FLAG...]"))
	})

	It("Has the HTTP listener flags", func() {
		cmd := Cmd()
		Expect(cmd.Flags().Lookup("http-listener-address")).ToNot(BeNil())
	})

	It("Has the CORS flags", func() {
		cmd := Cmd()
		Expect(cmd.Flags().Lookup("http-cors-allowed-origins")).ToNot(BeNil())
	})

	It("Has the gRPC client flags", func() {
		cmd := Cmd()
		Expect(cmd.Flags().Lookup("grpc-server-address")).ToNot(BeNil())
	})

	It("Has a --grpc-authn-trusted-token-issuers flag", func() {
		cmd := Cmd()
		Expect(cmd.Flags().Lookup("grpc-authn-trusted-token-issuers")).ToNot(BeNil())
	})

	It("Has a --ca-file flag", func() {
		cmd := Cmd()
		Expect(cmd.Flags().Lookup("ca-file")).ToNot(BeNil())
	})

	It("Has an --oauth-authorization-server flag", func() {
		cmd := Cmd()
		Expect(cmd.Flags().Lookup("oauth-authorization-server")).ToNot(BeNil())
	})

	It("Has an --oauth-resource-url flag", func() {
		cmd := Cmd()
		Expect(cmd.Flags().Lookup("oauth-resource-url")).ToNot(BeNil())
	})

	It("Accepts no arguments", func() {
		cmd := Cmd()
		Expect(cmd.Args).ToNot(BeNil())
	})
})

var _ = Describe("newServer", func() {
	It("publishes portable schemas for slice fields", func() {
		ctx := context.Background()
		server := newServer(ServerDeps{})
		client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil)
		serverTransport, clientTransport := mcp.NewInMemoryTransports()

		serverSession, err := server.Connect(ctx, serverTransport, nil)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(serverSession.Close()).To(Succeed()) })

		clientSession, err := client.Connect(ctx, clientTransport, nil)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(clientSession.Close()).To(Succeed()) })
		Expect(clientSession.InitializeResult().Instructions).To(Equal(serverInstructions))
		Expect(len(serverInstructions)).To(BeNumerically("<=", 512))
		Expect(serverInstructions).To(ContainSubstring("authoritative interface"))
		Expect(serverInstructions).To(ContainSubstring("ask before using another interface"))

		response, err := clientSession.ListTools(ctx, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(response.Tools).To(HaveLen(4))

		tools := make(map[string]*mcp.Tool, len(response.Tools))
		for _, tool := range response.Tools {
			tools[tool.Name] = tool
			Expect(hasMultiTypeSchema(tool.InputSchema)).To(BeFalse(), tool.Name+" input schema")
			Expect(hasMultiTypeSchema(tool.OutputSchema)).To(BeFalse(), tool.Name+" output schema")
		}
		Expect(tools).To(HaveKey("list_resources"))
		Expect(tools).To(HaveKey("get_resource"))
		Expect(tools).To(HaveKey("create_compute_instance"))
		Expect(tools).To(HaveKey("delete_compute_instance"))
		Expect(tools["list_resources"].Description).To(ContainSubstring("Use this tool to discover"))
		Expect(tools["get_resource"].Description).To(ContainSubstring("Use this tool to inspect"))
		Expect(tools["create_compute_instance"].Description).To(ContainSubstring("only after the user confirms"))
		Expect(tools["create_compute_instance"].Description).To(ContainSubstring("get_resource"))
		Expect(tools["create_compute_instance"].Description).To(ContainSubstring("boot_disk"))
		Expect(tools["create_compute_instance"].Description).To(ContainSubstring("storage_tier_id"))
		Expect(tools["create_compute_instance"].Description).To(ContainSubstring("network_attachments"))
		Expect(tools["create_compute_instance"].Description).To(ContainSubstring("subnet_id"))
		Expect(tools["delete_compute_instance"].Description).To(ContainSubstring("only when the user explicitly requests"))
		expectToolAnnotations(tools["list_resources"], "List OSAC deployment resources", true, false, true, true)
		expectToolAnnotations(tools["get_resource"], "Get OSAC deployment resource", true, false, true, true)
		expectToolAnnotations(tools["create_compute_instance"], "Create OSAC compute instance", false, false, false, true)
		expectToolAnnotations(tools["delete_compute_instance"], "Delete OSAC compute instance", false, true, false, true)
		expectComputeInstanceCreateSchema(tools["create_compute_instance"].InputSchema)
		expectNullableArraySchema(tools["list_resources"].OutputSchema, "items")
		expectResourceTypeSchema(tools["list_resources"].InputSchema)
		expectResourcePaginationSchema(tools["list_resources"].InputSchema)
		expectResourceTypeSchema(tools["get_resource"].InputSchema)
	})
})

func expectToolAnnotations(
	tool *mcp.Tool,
	title string,
	readOnly bool,
	destructive bool,
	idempotent bool,
	openWorld bool,
) {
	annotations := tool.Annotations
	Expect(annotations).ToNot(BeNil(), tool.Name+" annotations")
	Expect(annotations.Title).To(Equal(title), tool.Name+" annotation title")
	Expect(annotations.ReadOnlyHint).To(Equal(readOnly), tool.Name+" readOnlyHint")
	Expect(annotations.DestructiveHint).ToNot(BeNil(), tool.Name+" destructiveHint")
	Expect(*annotations.DestructiveHint).To(Equal(destructive), tool.Name+" destructiveHint")
	Expect(annotations.IdempotentHint).To(Equal(idempotent), tool.Name+" idempotentHint")
	Expect(annotations.OpenWorldHint).ToNot(BeNil(), tool.Name+" openWorldHint")
	Expect(*annotations.OpenWorldHint).To(Equal(openWorld), tool.Name+" openWorldHint")
}

func hasMultiTypeSchema(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		if _, ok := typed["type"].([]any); ok {
			return true
		}
		for _, child := range typed {
			if hasMultiTypeSchema(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if hasMultiTypeSchema(child) {
				return true
			}
		}
	}
	return false
}

func expectResourceTypeSchema(schema any) {
	document, ok := schema.(map[string]any)
	Expect(ok).To(BeTrue())
	properties, ok := document["properties"].(map[string]any)
	Expect(ok).To(BeTrue())
	resourceType, ok := properties["resource_type"].(map[string]any)
	Expect(ok).To(BeTrue())
	Expect(resourceType["enum"]).To(ConsistOf(
		string(ResourceTypeComputeInstanceCatalogItem),
		string(ResourceTypeComputeInstanceTemplate),
		string(ResourceTypeInstanceType),
		string(ResourceTypeDiskImage),
		string(ResourceTypeStorageTier),
		string(ResourceTypeVirtualNetwork),
		string(ResourceTypeSubnet),
		string(ResourceTypeSecurityGroup),
		string(ResourceTypeComputeInstance),
	))
}

func expectResourcePaginationSchema(schema any) {
	document, ok := schema.(map[string]any)
	Expect(ok).To(BeTrue())
	properties, ok := document["properties"].(map[string]any)
	Expect(ok).To(BeTrue())
	offset, ok := properties["offset"].(map[string]any)
	Expect(ok).To(BeTrue())
	Expect(offset["minimum"]).To(BeNumerically("==", 0))
	pageSize, ok := properties["page_size"].(map[string]any)
	Expect(ok).To(BeTrue())
	Expect(pageSize["minimum"]).To(BeNumerically("==", 0))
	Expect(pageSize["maximum"]).To(BeNumerically("==", maxResourcePageSize))
}

func expectComputeInstanceCreateSchema(schema any) {
	document, ok := schema.(map[string]any)
	Expect(ok).To(BeTrue())
	properties, ok := document["properties"].(map[string]any)
	Expect(ok).To(BeTrue())
	Expect(properties).To(HaveKey("instance_type_id"))
	Expect(properties).To(HaveKey("boot_disk"))
	Expect(properties).To(HaveKey("network_attachments"))
	Expect(properties).ToNot(HaveKey("set"))
	expectNullableArraySchema(schema, "network_attachments")
	for _, name := range []string{"name", "catalog_item", "instance_type_id"} {
		property, ok := properties[name].(map[string]any)
		Expect(ok).To(BeTrue(), name)
		Expect(property["type"]).To(Equal("string"), name)
	}

	// The create input is not the protobuf JSON returned by get_resource.
	bootDisk, ok := properties["boot_disk"].(map[string]any)
	Expect(ok).To(BeTrue())
	Expect(bootDisk["description"]).To(ContainSubstring("size_gib"))
	Expect(bootDisk["description"]).To(ContainSubstring("storage_tier_id"))
	Expect(bootDisk["description"]).To(ContainSubstring("list_resources(storage_tier)"))
	bootDiskProperties, ok := bootDisk["properties"].(map[string]any)
	Expect(ok).To(BeTrue())
	Expect(bootDiskProperties).To(HaveKey("size_gib"))
	Expect(bootDiskProperties).To(HaveKey("storage_tier_id"))
	Expect(bootDiskProperties).ToNot(HaveKey("sizeGib"))
	Expect(bootDiskProperties).ToNot(HaveKey("storageTier"))
	sizeGiB, ok := bootDiskProperties["size_gib"].(map[string]any)
	Expect(ok).To(BeTrue())
	Expect(sizeGiB["anyOf"]).To(ConsistOf(
		map[string]any{"type": "null"},
		map[string]any{"type": "integer"},
	))
	storageTier, ok := bootDiskProperties["storage_tier_id"].(map[string]any)
	Expect(ok).To(BeTrue())
	Expect(storageTier["type"]).To(Equal("string"))

	// Check the advertised item shape, not only the outer array type.
	attachments, ok := properties["network_attachments"].(map[string]any)
	Expect(ok).To(BeTrue())
	Expect(attachments["description"]).To(ContainSubstring("maximum one"))
	Expect(attachments["description"]).To(ContainSubstring("subnet_id"))
	item, ok := attachments["items"].(map[string]any)
	Expect(ok).To(BeTrue())
	Expect(item["type"]).To(Equal("object"))
	itemProperties, ok := item["properties"].(map[string]any)
	Expect(ok).To(BeTrue())
	Expect(itemProperties).To(HaveKey("subnet_id"))
	Expect(itemProperties).To(HaveKey("security_group_ids"))
	subnet, ok := itemProperties["subnet_id"].(map[string]any)
	Expect(ok).To(BeTrue())
	Expect(subnet["type"]).To(Equal("string"))
	securityGroups, ok := itemProperties["security_group_ids"].(map[string]any)
	Expect(ok).To(BeTrue())
	Expect(securityGroups["items"]).To(HaveKeyWithValue("type", "string"))
}

func expectNullableArraySchema(schema any, name string) {
	document, ok := schema.(map[string]any)
	Expect(ok).To(BeTrue())
	properties, ok := document["properties"].(map[string]any)
	Expect(ok).To(BeTrue())
	property, ok := properties[name].(map[string]any)
	Expect(ok).To(BeTrue())
	Expect(property).ToNot(HaveKey("type"))
	Expect(property["anyOf"]).To(ConsistOf(
		map[string]any{"type": "null"},
		map[string]any{"type": "array"},
	))
}

var _ = Describe("newTokenVerifier", func() {
	var ctrl *gomock.Controller

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
	})

	It("Maps a validated token to a caller-specific API token source", func() {
		expiration := time.Now().Add(time.Hour)
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"sub": "alice",
			"exp": float64(expiration.Unix()),
		})
		validator := auth.NewMockJwtValidator(ctrl)
		validator.EXPECT().Validate(gomock.Any(), "raw-bearer-value").Return(token, nil)

		verifier := newTokenVerifier(validator, testExchanger)
		info, err := verifier(context.Background(), "raw-bearer-value", nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(info.UserID).To(Equal("alice"))
		Expect(info.Expiration.Unix()).To(Equal(expiration.Unix()))
		source, ok := info.Extra[exchangeTokenExtraKey].(apiTokenSource)
		Expect(ok).To(BeTrue())
		apiToken, err := source(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(apiToken).To(Equal("api-bearer-value"))
	})

	It("Wraps a validation failure with sdkauth.ErrInvalidToken", func() {
		validator := auth.NewMockJwtValidator(ctrl)
		validator.EXPECT().Validate(gomock.Any(), gomock.Any()).Return(nil, errors.New("token signature is not valid"))

		verifier := newTokenVerifier(validator, testExchanger)
		_, err := verifier(context.Background(), "bad-token", nil)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, sdkauth.ErrInvalidToken)).To(BeTrue())
	})

	It("Rejects a validated token with no subject claim", func() {
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"exp": float64(time.Now().Add(time.Hour).Unix()),
		})
		validator := auth.NewMockJwtValidator(ctrl)
		validator.EXPECT().Validate(gomock.Any(), gomock.Any()).Return(token, nil)

		verifier := newTokenVerifier(validator, testExchanger)
		_, err := verifier(context.Background(), "raw-bearer-value", nil)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, sdkauth.ErrInvalidToken)).To(BeTrue())
	})

	It("Rejects a validated token with no expiration claim", func() {
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"sub": "alice",
		})
		validator := auth.NewMockJwtValidator(ctrl)
		validator.EXPECT().Validate(gomock.Any(), gomock.Any()).Return(token, nil)

		verifier := newTokenVerifier(validator, testExchanger)
		_, err := verifier(context.Background(), "raw-bearer-value", nil)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, sdkauth.ErrInvalidToken)).To(BeTrue())
	})
})

var testExchanger = TokenExchangerFunc(func(_ context.Context, _ string, _ *jwt.Token) (string, error) {
	return "api-bearer-value", nil
})

var _ = Describe("forwardAPIToken", func() {
	It("Adds only the exchanged API bearer token to outgoing gRPC metadata", func() {
		ctx := context.Background()
		req := &mcp.CallToolRequest{
			Extra: &mcp.RequestExtra{
				TokenInfo: &sdkauth.TokenInfo{
					Extra: map[string]any{
						exchangeTokenExtraKey: apiTokenSource(func(context.Context) (string, error) {
							return "api-bearer-value", nil
						}),
					},
				},
			},
		}

		result, err := forwardAPIToken(ctx, req)
		Expect(err).ToNot(HaveOccurred())
		md, ok := metadata.FromOutgoingContext(result)
		Expect(ok).To(BeTrue())
		Expect(md.Get("authorization")).To(Equal([]string{"Bearer api-bearer-value"}))
	})

	It("Fails closed when the request has no Extra", func() {
		ctx := context.Background()
		_, err := forwardAPIToken(ctx, &mcp.CallToolRequest{})
		Expect(err).To(HaveOccurred())
	})

	It("Fails closed when Extra has no TokenInfo", func() {
		ctx := context.Background()
		req := &mcp.CallToolRequest{Extra: &mcp.RequestExtra{}}
		_, err := forwardAPIToken(ctx, req)
		Expect(err).To(HaveOccurred())
	})

	It("Fails closed when TokenInfo has no exchange source", func() {
		ctx := context.Background()
		req := &mcp.CallToolRequest{
			Extra: &mcp.RequestExtra{
				TokenInfo: &sdkauth.TokenInfo{},
			},
		}
		_, err := forwardAPIToken(ctx, req)
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("NewHandler", func() {
	var ctrl *gomock.Controller

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
	})

	It("accepts MCP-audience tokens and rejects API tokens at HTTP ingress", func() {
		const issuer = "https://issuer.example.com"
		jwksCache := auth.NewMockJwksCache(ctrl)
		jwksCache.EXPECT().Get(gomock.Any(), issuer, "123").Return(testutils.JwtPublicKey(), nil).AnyTimes()
		validator, err := newMCPJWTValidator(slog.Default(), jwksCache, "https://mcp.example.com")
		Expect(err).ToNot(HaveOccurred())
		handler, err := NewHandler(ServerDeps{}, validator, "", "", testExchanger)
		Expect(err).ToNot(HaveOccurred())

		for _, tc := range []struct {
			audience string
			accepted bool
		}{
			{audience: "https://mcp.example.com/", accepted: true},
			{audience: "https://mcp.example.com", accepted: false},
			{audience: auth.Audience, accepted: false},
		} {
			token := testutils.MakeTokenObject(nil, jwt.MapClaims{"iss": issuer, "aud": tc.audience})
			request := httptest.NewRequest(http.MethodPost, "/", nil)
			request.Header.Set("Authorization", "Bearer "+token.Raw)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if tc.accepted {
				Expect(recorder.Code).ToNot(Equal(http.StatusUnauthorized))
			} else {
				Expect(recorder.Code).To(Equal(http.StatusUnauthorized))
			}
		}
	})

	It("Rejects an unauthenticated request with no resource_metadata hint when OAuth discovery is unconfigured", func() {
		validator := auth.NewMockJwtValidator(ctrl)
		handler, err := NewHandler(ServerDeps{}, validator, "", "", testExchanger)
		Expect(err).ToNot(HaveOccurred())

		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/", nil)
		handler.ServeHTTP(recorder, request)

		Expect(recorder.Code).To(Equal(http.StatusUnauthorized))
		Expect(recorder.Header().Get("WWW-Authenticate")).ToNot(ContainSubstring("resource_metadata"))
	})

	It("Rejects configuration with only one of the two OAuth discovery flags set", func() {
		validator := auth.NewMockJwtValidator(ctrl)
		_, err := NewHandler(ServerDeps{}, validator, "https://keycloak.example.com/realms/osac", "", testExchanger)
		Expect(err).To(HaveOccurred())

		_, err = NewHandler(ServerDeps{}, validator, "", "https://mcp.example.com", testExchanger)
		Expect(err).To(HaveOccurred())
	})

	It("Rejects path-prefixed and noncanonical OAuth resource URLs", func() {
		validator := auth.NewMockJwtValidator(ctrl)
		for _, resourceURL := range []string{
			"https://mcp.example.com/mcp",
			"https://mcp.example.com/?tenant=one",
			"https://mcp.example.com/#fragment",
			"http://mcp.example.com",
		} {
			_, err := NewHandler(ServerDeps{}, validator, "https://keycloak.example.com/realms/osac", resourceURL, testExchanger)
			Expect(err).To(MatchError(ContainSubstring("root HTTPS URL")), resourceURL)
		}
	})

	It("Adds a resource_metadata hint to 401s and serves the metadata document, unauthenticated, when both flags are set", func() {
		validator := auth.NewMockJwtValidator(ctrl)
		handler, err := NewHandler(
			ServerDeps{}, validator, "https://keycloak.example.com/realms/osac", "https://mcp.example.com", testExchanger,
		)
		Expect(err).ToNot(HaveOccurred())

		// The main endpoint's 401 now carries a discovery hint:
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/", nil)
		handler.ServeHTTP(recorder, request)
		Expect(recorder.Code).To(Equal(http.StatusUnauthorized))
		Expect(recorder.Header().Get("WWW-Authenticate")).To(ContainSubstring(
			`resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource"`,
		))

		// The metadata document itself is served without requiring a bearer token:
		recorder = httptest.NewRecorder()
		request = httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource", nil)
		handler.ServeHTTP(recorder, request)
		Expect(recorder.Code).To(Equal(http.StatusOK))
		var metadata struct {
			Resource             string   `json:"resource"`
			AuthorizationServers []string `json:"authorization_servers"`
		}
		Expect(json.Unmarshal(recorder.Body.Bytes(), &metadata)).ToNot(HaveOccurred())
		Expect(metadata.Resource).To(Equal("https://mcp.example.com/"))
		Expect(metadata.AuthorizationServers).To(Equal([]string{"https://keycloak.example.com/realms/osac"}))
	})

	It("Keeps the canonical trailing slash in resource metadata while using the root well-known hint", func() {
		validator := auth.NewMockJwtValidator(ctrl)
		handler, err := NewHandler(
			ServerDeps{}, validator, "https://keycloak.example.com/realms/osac", "https://mcp.example.com/", testExchanger,
		)
		Expect(err).ToNot(HaveOccurred())

		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/", nil)
		handler.ServeHTTP(recorder, request)
		Expect(recorder.Header().Get("WWW-Authenticate")).To(ContainSubstring(
			`resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource"`,
		))

		recorder = httptest.NewRecorder()
		request = httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource", nil)
		handler.ServeHTTP(recorder, request)
		var metadata struct {
			Resource string `json:"resource"`
		}
		Expect(json.Unmarshal(recorder.Body.Bytes(), &metadata)).ToNot(HaveOccurred())
		Expect(metadata.Resource).To(Equal("https://mcp.example.com/"))
	})

	It("Still routes an authenticated request through to the streamable transport when both flags are set", func() {
		expiration := time.Now().Add(time.Hour)
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"sub": "alice",
			"exp": float64(expiration.Unix()),
		})
		validator := auth.NewMockJwtValidator(ctrl)
		validator.EXPECT().Validate(gomock.Any(), "valid-token").Return(token, nil)
		handler, err := NewHandler(
			ServerDeps{}, validator, "https://keycloak.example.com/realms/osac", "https://mcp.example.com", testExchanger,
		)
		Expect(err).ToNot(HaveOccurred())

		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/", nil)
		request.Header.Set("Authorization", "Bearer valid-token")
		handler.ServeHTTP(recorder, request)

		// A valid token clears bearer-token verification, so the request reaches the streamable handler and fails
		// for a reason specific to that layer (an empty/non-JSON-RPC body), not for lack of authentication.
		Expect(recorder.Code).ToNot(Equal(http.StatusUnauthorized))
	})
})
