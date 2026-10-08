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
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	"github.com/osac-project/osac/fulfillment-service/internal/network"
	shtdwn "github.com/osac-project/osac/fulfillment-service/internal/shutdown"
	"github.com/osac-project/osac/fulfillment-service/internal/trust"
	"github.com/osac-project/osac/fulfillment-service/internal/version"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// Cmd creates and returns the `start mcp-server` command.
func Cmd() *cobra.Command {
	runner := &runnerContext{}
	command := &cobra.Command{
		Use:                   "mcp-server [FLAG...]",
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
		Args:                  cobra.NoArgs,
		RunE:                  runner.run,
	}
	flags := command.Flags()
	network.AddListenerFlags(flags, network.HttpListenerName, network.DefaultHttpAddress)
	network.AddCorsFlags(flags, network.HttpListenerName)
	network.AddGrpcClientFlags(flags, network.GrpcClientName, network.DefaultGrpcAddress)
	flags.StringSliceVar(
		&runner.args.trustedTokenIssuers,
		"grpc-authn-trusted-token-issuers",
		[]string{},
		trustedTokenIssuersFlagHelp,
	)
	flags.StringSliceVar(
		&runner.args.caFiles,
		"ca-file",
		[]string{},
		caFileFlagHelp,
	)
	flags.StringVar(
		&runner.args.oauthAuthorizationServer,
		"oauth-authorization-server",
		"",
		oauthAuthorizationServerFlagHelp,
	)
	flags.StringVar(
		&runner.args.oauthResourceURL,
		"oauth-resource-url",
		"",
		oauthResourceURLFlagHelp,
	)
	flags.StringVar(&runner.args.oauthTokenEndpoint, "oauth-token-endpoint", "", "HTTPS OAuth token exchange endpoint")
	flags.StringVar(&runner.args.oauthExchangeClientID, "oauth-exchange-client-id", "", "Confidential token exchange client ID")
	flags.StringVar(&runner.args.oauthExchangeClientSecretFile, "oauth-exchange-client-secret-file", "", "Path to token exchange client secret")
	return command
}

// runnerContext contains the data and logic needed to run the `start mcp-server` command.
type runnerContext struct {
	logger *slog.Logger
	flags  *pflag.FlagSet
	args   struct {
		trustedTokenIssuers           []string
		caFiles                       []string
		oauthAuthorizationServer      string
		oauthResourceURL              string
		oauthTokenEndpoint            string
		oauthExchangeClientID         string
		oauthExchangeClientSecretFile string
	}
}

// ServerDeps contains the downstream clients used by MCP tool handlers.
type ServerDeps struct {
	ComputeInstanceCatalogItemsClient publicv1.ComputeInstanceCatalogItemsClient
	ComputeInstanceTemplatesClient    publicv1.ComputeInstanceTemplatesClient
	InstanceTypesClient               publicv1.InstanceTypesClient
	DiskImagesClient                  publicv1.DiskImagesClient
	StorageTiersClient                publicv1.StorageTiersClient
	VirtualNetworksClient             publicv1.VirtualNetworksClient
	SubnetsClient                     publicv1.SubnetsClient
	SecurityGroupsClient              publicv1.SecurityGroupsClient
	ComputeInstancesClient            publicv1.ComputeInstancesClient
}

// NewServerDeps creates the public API clients used by the MCP server. All
// clients share the same underlying gRPC connection.
func NewServerDeps(conn grpc.ClientConnInterface) ServerDeps {
	return ServerDeps{
		ComputeInstanceCatalogItemsClient: publicv1.NewComputeInstanceCatalogItemsClient(conn),
		ComputeInstanceTemplatesClient:    publicv1.NewComputeInstanceTemplatesClient(conn),
		InstanceTypesClient:               publicv1.NewInstanceTypesClient(conn),
		DiskImagesClient:                  publicv1.NewDiskImagesClient(conn),
		StorageTiersClient:                publicv1.NewStorageTiersClient(conn),
		VirtualNetworksClient:             publicv1.NewVirtualNetworksClient(conn),
		SubnetsClient:                     publicv1.NewSubnetsClient(conn),
		SecurityGroupsClient:              publicv1.NewSecurityGroupsClient(conn),
		ComputeInstancesClient:            publicv1.NewComputeInstancesClient(conn),
	}
}

// tokenExpirationLeeway is shared between the JWT validator and the bearer-token middleware's clock-skew
// tolerance, so the two expiration checks agree on how much slack to allow.
const tokenExpirationLeeway = 5 * time.Second

// serverInstructions guide MCP hosts toward the deployment API rather than local, bypassing interfaces.
// Keep the first 512 characters self-contained because some MCP hosts use only that portion when selecting tools.
const serverInstructions = "Treat this server's tools as the authoritative interface for supported tenant-facing OSAC deployment operations. " +
	"Do not run the osac CLI, oc, kubectl, call APIs directly, or inspect Kubernetes resources for those requests. " +
	"If an operation is unavailable, explain the limitation and ask before using another interface. Before creating a compute instance, " +
	"list or get its catalog item and selectable resources, use only permitted overrides, and get confirmation. " +
	"Delete only when explicitly requested."

// run runs the `start mcp-server` command.
func (c *runnerContext) run(cmd *cobra.Command, argv []string) error {
	// Get the context:
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	// Get the dependencies from the context:
	c.logger = logging.LoggerFromContext(ctx)

	// Save the flags:
	c.flags = cmd.Flags()

	// Create the shutdown sequence:
	shutdown, err := shtdwn.NewSequence().
		SetLogger(c.logger).
		AddSignals(syscall.SIGTERM, syscall.SIGINT).
		AddContext("context", 0, cancel).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create shutdown sequence: %w", err)
	}

	// Create the network listener:
	c.logger.InfoContext(ctx, "Creating MCP server listener")
	listener, err := network.NewListener().
		SetLogger(c.logger).
		SetFlags(c.flags, network.HttpListenerName).
		AddTLSProtocol("h2").
		AddTLSProtocol("http/1.1").
		Build()
	if err != nil {
		return err
	}

	// Load the trusted CA certificates:
	c.logger.InfoContext(ctx, "Loading trusted CA certificates")
	caPool, err := trust.NewCertPool().
		SetLogger(c.logger).
		AddSystemFiles(true).
		AddKubernetesFiles(true).
		AddFiles(c.args.caFiles...).
		Build()
	if err != nil {
		return fmt.Errorf("failed to load trusted CA certificates: %w", err)
	}

	// Create the JWT validator used to verify incoming bearer tokens:
	c.logger.InfoContext(ctx, "Creating JWKS cache")
	jwksCache, err := auth.NewJwksCache().
		SetLogger(c.logger).
		SetCaPool(caPool).
		AddIssuers(c.args.trustedTokenIssuers...).
		AddKubernetesIssuer(true).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create JWKS cache: %w", err)
	}
	c.logger.InfoContext(ctx, "Creating JWT validator")
	jwtValidator, err := newMCPJWTValidator(c.logger, jwksCache, c.args.oauthResourceURL)
	if err != nil {
		return fmt.Errorf("failed to create JWT validator: %w", err)
	}
	apiValidator, err := auth.NewJwtValidator().
		SetLogger(c.logger).
		SetJwksCache(jwksCache).
		SetExpirationLeeway(tokenExpirationLeeway).
		AddAudience(auth.Audience).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create API JWT validator: %w", err)
	}
	secret, err := os.ReadFile(c.args.oauthExchangeClientSecretFile)
	if err != nil {
		return errors.New("failed to read OAuth token exchange client secret")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: caPool.Pool(), MinVersion: tls.VersionTLS12}
	exchanger, err := newOAuthTokenExchanger(
		c.args.oauthTokenEndpoint, c.args.oauthExchangeClientID, strings.TrimSpace(string(secret)),
		&http.Client{Transport: transport, Timeout: 10 * time.Second}, apiValidator,
	)
	if err != nil {
		return err
	}

	// Calculate the user agent:
	userAgent := fmt.Sprintf("%s/%s", userAgent, version.Get())

	// The public gRPC client has no service identity. Tool calls attach an exchanged token for their caller.
	c.logger.InfoContext(ctx, "Creating gRPC client")
	grpcClient, err := network.NewGrpcClient().
		SetLogger(c.logger).
		SetFlags(c.flags, network.GrpcClientName).
		SetCaPool(caPool).
		SetUserAgent(userAgent).
		Build()
	if err != nil {
		return err
	}
	defer grpcClient.Close()

	// Build the MCP server and wrap it with bearer-token authentication:
	handler, err := NewHandler(
		NewServerDeps(grpcClient), jwtValidator, c.args.oauthAuthorizationServer, c.args.oauthResourceURL, exchanger,
	)
	if err != nil {
		return fmt.Errorf("failed to create MCP handler: %w", err)
	}

	// Add the CORS support:
	corsMiddleware, err := network.NewCorsMiddleware().
		SetLogger(c.logger).
		SetFlags(c.flags, network.HttpListenerName).
		AddAllowedHeaders("MCP-Protocol-Version").
		RejectDisallowedOrigins().
		Build()
	if err != nil {
		return fmt.Errorf("failed to create CORS middleware: %w", err)
	}
	handler = corsMiddleware(handler)

	// Start serving:
	c.logger.InfoContext(
		ctx,
		"Start serving",
		slog.String("address", listener.Addr().String()),
	)
	var protocols http.Protocols
	protocols.SetHTTP1(true)
	protocols.SetHTTP2(true)
	protocols.SetUnencryptedHTTP2(true)
	httpServer := &http.Server{
		Addr:              listener.Addr().String(),
		Handler:           handler,
		Protocols:         &protocols,
		ReadHeaderTimeout: 10 * time.Second,
	}
	shutdown.AddHttpServer(network.HttpListenerName, 0, httpServer)
	go func() {
		err := httpServer.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			c.logger.ErrorContext(
				ctx,
				"MCP server failed",
				slog.Any("error", err),
			)
			shutdown.Start(1)
		}
	}()

	// Keep running till the shutdown sequence completes:
	c.logger.InfoContext(ctx, "Waiting for shutdown sequence to complete")
	return shutdown.Wait()
}

func newMCPJWTValidator(logger *slog.Logger, jwksCache auth.JwksCache, resourceURL string) (auth.JwtValidator, error) {
	if resourceURL == "" {
		return nil, errors.New("OAuth resource URL is required for MCP token validation")
	}
	resourceURL, err := canonicalMCPResourceURL(resourceURL)
	if err != nil {
		return nil, err
	}
	return auth.NewJwtValidator().
		SetLogger(logger).
		SetJwksCache(jwksCache).
		SetExpirationLeeway(tokenExpirationLeeway).
		AddAudience(resourceURL).
		Build()
}

func canonicalMCPResourceURL(resourceURL string) (string, error) {
	parsed, err := url.Parse(resourceURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("OAuth resource URL must be a root HTTPS URL without a query or fragment")
	}
	return strings.TrimSuffix(resourceURL, "/") + "/", nil
}

// newServer creates the MCP server and registers its tools.
func newServer(deps ServerDeps) *mcp.Server {
	resources := newResourceRegistry(deps)
	resourceTypes := resources.resourceTypes()
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "osac-deployment-mcp",
		Version: version.Get(),
	}, &mcp.ServerOptions{
		Instructions: serverInstructions,
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "list_resources",
		Description: "Use this tool to discover or compare tenant-visible OSAC deployment options and existing compute instances. " +
			"resource_type must be one of: " +
			supportedResourceTypesDescription(resourceTypes) + ".",
		Annotations: &mcp.ToolAnnotations{
			Title:           "List OSAC deployment resources",
			ReadOnlyHint:    true,
			DestructiveHint: toolAnnotationBool(false),
			IdempotentHint:  true,
			OpenWorldHint:   toolAnnotationBool(true),
		},
		InputSchema:  resourceToolSchema[ListResourcesInput](resourceTypes),
		OutputSchema: compatibleToolSchema[ListResourcesOutput](),
	}, handleListResources(resources))
	mcp.AddTool(server, &mcp.Tool{
		Name: "get_resource",
		Description: "Use this tool to inspect one selected resource, verify catalog or reference details, or report current compute instance state. " +
			"resource_type must be one of: " +
			supportedResourceTypesDescription(resourceTypes) + ".",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Get OSAC deployment resource",
			ReadOnlyHint:    true,
			DestructiveHint: toolAnnotationBool(false),
			IdempotentHint:  true,
			OpenWorldHint:   toolAnnotationBool(true),
		},
		InputSchema:  resourceToolSchema[GetResourceInput](resourceTypes),
		OutputSchema: compatibleToolSchema[GetResourceOutput](),
	}, handleGetResource(resources))
	mcp.AddTool(server, &mcp.Tool{
		Name: "create_compute_instance",
		Description: "Use this tool only after the user confirms the requested VM and you have inspected the catalog item and any selected " +
			"override resources. Use this tool's snake_case fields and resource IDs, not get_resource's camelCase reference objects. " +
			"For a disk override, boot_disk uses size_gib and/or storage_tier_id (a string ID), not a nested storageTier object. " +
			"If overriding networking, network_attachments must contain maximum one object with subnet_id and " +
			"optional security_group_ids, not an array of subnet ID strings. Creates a compute instance asynchronously; " +
			"acceptance does not mean the VM is ready.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Create OSAC compute instance",
			ReadOnlyHint:    false,
			DestructiveHint: toolAnnotationBool(false),
			IdempotentHint:  false,
			OpenWorldHint:   toolAnnotationBool(true),
		},
		InputSchema:  compatibleToolSchema[CreateComputeInstanceInput](),
		OutputSchema: compatibleToolSchema[CreateComputeInstanceOutput](),
	}, handleCreateComputeInstance(deps.ComputeInstancesClient))
	mcp.AddTool(server, &mcp.Tool{
		Name: "delete_compute_instance",
		Description: "Use this tool only when the user explicitly requests deletion or cleanup. Deletes a compute instance by ID " +
			"asynchronously.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Delete OSAC compute instance",
			ReadOnlyHint:    false,
			DestructiveHint: toolAnnotationBool(true),
			IdempotentHint:  false,
			OpenWorldHint:   toolAnnotationBool(true),
		},
		InputSchema:  compatibleToolSchema[DeleteComputeInstanceInput](),
		OutputSchema: compatibleToolSchema[DeleteComputeInstanceOutput](),
	}, handleDeleteComputeInstance(deps.ComputeInstancesClient))
	return server
}

func toolAnnotationBool(value bool) *bool {
	return &value
}

// compatibleToolSchema converts inferred multi-type schemas to anyOf branches.
// Several MCP hosts reject JSON Schema's otherwise-valid array form
// of type, while accepting the equivalent anyOf representation.
func compatibleToolSchema[T any]() json.RawMessage {
	schema, err := jsonschema.For[T](nil)
	if err != nil {
		panic(fmt.Errorf("infer tool schema: %w", err))
	}
	encoded, err := json.Marshal(schema)
	if err != nil {
		panic(fmt.Errorf("encode tool schema: %w", err))
	}
	var document any
	if err := json.Unmarshal(encoded, &document); err != nil {
		panic(fmt.Errorf("decode tool schema: %w", err))
	}
	normalizeMultiTypeSchemas(document)
	encoded, err = json.Marshal(document)
	if err != nil {
		panic(fmt.Errorf("encode compatible tool schema: %w", err))
	}
	return encoded
}

func resourceToolSchema[T any](supportedResourceTypes []ResourceType) json.RawMessage {
	schema := compatibleToolSchema[T]()
	var document map[string]any
	if err := json.Unmarshal(schema, &document); err != nil {
		panic(fmt.Errorf("decode resource tool schema: %w", err))
	}
	properties, ok := document["properties"].(map[string]any)
	if !ok {
		panic("resource tool schema has no properties")
	}
	resourceType, ok := properties["resource_type"].(map[string]any)
	if !ok {
		panic("resource tool schema has no resource_type property")
	}
	resourceTypes := make([]string, len(supportedResourceTypes))
	for i, resourceType := range supportedResourceTypes {
		resourceTypes[i] = string(resourceType)
	}
	resourceType["enum"] = resourceTypes
	if offset, ok := properties["offset"].(map[string]any); ok {
		offset["minimum"] = 0
	}
	if pageSize, ok := properties["page_size"].(map[string]any); ok {
		pageSize["minimum"] = 0 // Zero selects the default page size.
		pageSize["maximum"] = maxResourcePageSize
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		panic(fmt.Errorf("encode resource tool schema: %w", err))
	}
	return encoded
}

func normalizeMultiTypeSchemas(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for _, child := range typed {
			normalizeMultiTypeSchemas(child)
		}
		types, ok := typed["type"].([]any)
		if !ok || len(types) < 2 {
			return
		}
		branches := make([]any, 0, len(types))
		for _, typ := range types {
			branches = append(branches, map[string]any{"type": typ})
		}
		delete(typed, "type")
		typed["anyOf"] = branches
	case []any:
		for _, child := range typed {
			normalizeMultiTypeSchemas(child)
		}
	}
}

// oauthProtectedResourcePath is where the RFC 9728 protected-resource-metadata document is served, when OAuth
// discovery is configured. A spec-compliant MCP client that receives a 401 with this path in the WWW-Authenticate
// header's resource_metadata hint fetches it to learn which Authorization Server protects this resource, then
// drives a real interactive browser login on its own — no manual bearer-token configuration needed.
const oauthProtectedResourcePath = "/.well-known/oauth-protected-resource"

// NewHandler builds the MCP HTTP handler with bearer-token authentication and optional OAuth resource discovery.
func NewHandler(
	deps ServerDeps, validator auth.JwtValidator, oauthAuthorizationServer, oauthResourceURL string, exchanger TokenExchanger,
) (http.Handler, error) {
	if exchanger == nil {
		return nil, errors.New("MCP token exchanger is required")
	}
	if (oauthAuthorizationServer == "") != (oauthResourceURL == "") {
		return nil, errors.New(
			"'--oauth-authorization-server' and '--oauth-resource-url' must be set together, or not at all",
		)
	}
	if oauthResourceURL != "" {
		var err error
		oauthResourceURL, err = canonicalMCPResourceURL(oauthResourceURL)
		if err != nil {
			return nil, fmt.Errorf("'--oauth-resource-url': %w", err)
		}
	}
	server := newServer(deps)
	streamableHandler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{
			Stateless: true,
		},
	)
	var resourceMetadataURL string
	if oauthResourceURL != "" {
		resourceMetadataURL = strings.TrimSuffix(oauthResourceURL, "/") + oauthProtectedResourcePath
	}
	authenticatedHandler := sdkauth.RequireBearerToken(newTokenVerifier(validator, exchanger), &sdkauth.RequireBearerTokenOptions{
		// Matches the JWT validator's own expiration leeway, so the SDK's independent expiration check
		// doesn't reject tokens the validator itself still considers valid.
		ClockSkew:           tokenExpirationLeeway,
		ResourceMetadataURL: resourceMetadataURL,
	})(streamableHandler)
	if oauthAuthorizationServer == "" {
		return authenticatedHandler, nil
	}
	// The metadata document itself must never require the very bearer token clients are trying to discover how to
	// obtain, so it's mounted unauthenticated, on a mux alongside (not wrapped by) the authenticated MCP endpoint.
	mux := http.NewServeMux()
	mux.Handle(oauthProtectedResourcePath, sdkauth.ProtectedResourceMetadataHandler(&oauthex.ProtectedResourceMetadata{
		Resource:             oauthResourceURL,
		AuthorizationServers: []string{oauthAuthorizationServer},
	}))
	mux.Handle("/", authenticatedHandler)
	return mux, nil
}

const exchangeTokenExtraKey = "exchange_token"

type apiTokenSource func(context.Context) (string, error)

// newTokenVerifier adapts an auth.JwtValidator to the shape the MCP SDK's bearer-token middleware requires.
func newTokenVerifier(validator auth.JwtValidator, exchanger TokenExchanger) sdkauth.TokenVerifier {
	return func(ctx context.Context, token string, _ *http.Request) (*sdkauth.TokenInfo, error) {
		parsed, err := validator.Validate(ctx, token)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", sdkauth.ErrInvalidToken, err)
		}
		subject, err := parsed.Claims.GetSubject()
		if err != nil || subject == "" {
			return nil, fmt.Errorf("%w: token has no subject claim", sdkauth.ErrInvalidToken)
		}
		expiration, err := parsed.Claims.GetExpirationTime()
		if err != nil || expiration == nil {
			return nil, fmt.Errorf("%w: token has no expiration claim", sdkauth.ErrInvalidToken)
		}
		return &sdkauth.TokenInfo{
			UserID:     subject,
			Expiration: expiration.Time,
			Extra: map[string]any{exchangeTokenExtraKey: apiTokenSource(func(ctx context.Context) (string, error) {
				return exchanger.Exchange(ctx, token, parsed)
			})},
		}, nil
	}
}

// forwardAPIToken exchanges the caller's MCP token once for this tool call and attaches only the API token.
func forwardAPIToken(ctx context.Context, req *mcp.CallToolRequest) (context.Context, error) {
	if req == nil || req.Extra == nil || req.Extra.TokenInfo == nil {
		return nil, errors.New("MCP caller token is missing")
	}
	source, ok := req.Extra.TokenInfo.Extra[exchangeTokenExtraKey].(apiTokenSource)
	if !ok || source == nil {
		return nil, errors.New("MCP caller token exchange is unavailable")
	}
	apiToken, err := source(ctx)
	if err != nil {
		return nil, err
	}
	if apiToken == "" {
		return nil, errors.New("OAuth token exchange returned no API token")
	}
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+apiToken), nil
}

// userAgent is the user agent string for the MCP server.
const userAgent = "fulfillment-mcp-server"

const shortHelp = `Starts the MCP server`

const longHelp = `
Starts the MCP server.

**Experimental.** This command exposes a subset of the fulfillment-service API as Model
Context Protocol tools, for use by AI agents. Its tool set and transport are subject to
change without notice.
`

const trustedTokenIssuersFlagHelp = `
_ISSUERS_ - Comma separated list of token issuers that are trusted to authenticate callers.
`

const caFileFlagHelp = `
_FILE|DIRECTORY_ - File or directory containing trusted CA certificates.
`

const oauthAuthorizationServerFlagHelp = `
_URL_ - Issuer URL of the OAuth authorization server (Keycloak realm) that protects this MCP server, advertised via
RFC 9728 protected-resource-metadata discovery so spec-compliant MCP clients (e.g. Cursor, Claude Desktop) can drive
a real interactive login instead of requiring a manually configured bearer token. Must be set together with
{{ bt }}--oauth-resource-url{{ bt }}, or not at all.
`

const oauthResourceURLFlagHelp = `
_URL_ - This MCP server's own canonical externally-reachable URL, advertised as the "resource" in the protected-
resource-metadata document. Must be set together with {{ bt }}--oauth-authorization-server{{ bt }}, or not at all.
`
