/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package login

import (
	"context"
	"crypto/x509"
	"embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"google.golang.org/grpc"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/config"
	"github.com/osac-project/fulfillment-service/internal/exit"
	"github.com/osac-project/fulfillment-service/internal/logging"
	"github.com/osac-project/fulfillment-service/internal/network"
	"github.com/osac-project/fulfillment-service/internal/oauth"
	"github.com/osac-project/fulfillment-service/internal/terminal"
)

//go:embed templates
var templatesFS embed.FS

func Cmd() *cobra.Command {
	// Create the runner and the command:
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:                   "login [FLAG...] ADDRESS",
		DisableFlagsInUseLine: true,
		Short:                 shortHelp,
		Long:                  longHelp,
		RunE:                  runner.run,
	}

	// Define the flags:
	flags := result.Flags()
	flags.BoolVar(
		&runner.args.plaintext,
		"plaintext",
		false,
		plaintextFlagHelp,
	)
	flags.BoolVar(
		&runner.args.insecure,
		"insecure",
		false,
		insecureFlagHelp,
	)
	flags.StringArrayVar(
		&runner.args.caFiles,
		"ca-file",
		[]string{},
		caFilesFlagHelp,
	)
	flags.StringVar(
		&runner.args.address,
		"address",
		os.Getenv("OSAC_ADDRESS"),
		addressFlagHelp,
	)
	flags.BoolVar(
		&runner.args.private,
		"private",
		false,
		privateFlagHelp,
	)
	flags.StringVar(
		&runner.args.token,
		"token",
		os.Getenv("OSAC_TOKEN"),
		tokenFlagHelp,
	)
	flags.StringVar(
		&runner.args.tokenScript,
		"token-script",
		os.Getenv("OSAC_TOKEN_SCRIPT"),
		tokenScriptFlagHelp,
	)
	flags.StringVar(
		&runner.args.issuer,
		"issuer",
		"",
		issuerFlagHelp,
	)
	flags.StringVar(
		&runner.args.flow,
		"flow",
		defaultFlow,
		flowFlagHelp,
	)
	flags.StringVar(
		&runner.args.clientId,
		"client-id",
		defaultClientId,
		clientIdFlagHelp,
	)
	flags.StringVar(
		&runner.args.clientSecret,
		"client-secret",
		"",
		clientSecretFlagHelp,
	)
	flags.StringSliceVar(
		&runner.args.scopes,
		"scopes",
		[]string{},
		scopesFlagHelp,
	)
	flags.StringVar(
		&runner.args.redirectUri,
		"redirect-uri",
		defaultRedirectUri,
		redirectUriFlagHelp,
	)
	flags.StringVar(
		&runner.args.user,
		"user",
		"",
		userFlagHelp,
	)
	flags.StringVar(
		&runner.args.password,
		"password",
		"",
		passwordFlagHelp,
	)

	// Define the depreacated alternatives for the OAuth flags:
	flags.StringVar(
		&runner.args.issuer,
		"oauth-issuer",
		"",
		"Alternative for the '--issuer' flag.",
	)
	flags.MarkDeprecated("oauth-issuer", "use '--issuer' instead")
	flags.StringVar(
		&runner.args.flow,
		"oauth-flow",
		defaultFlow,
		"Deprecated alternative for the '--flow' flag.",
	)
	flags.MarkDeprecated("oauth-flow", "use '--flow' instead")
	flags.StringVar(
		&runner.args.clientId,
		"oauth-client-id",
		defaultClientId,
		"Deprecated alternative for the '--client-id' flag.",
	)
	flags.MarkDeprecated("oauth-client-id", "use '--client-id' instead")
	flags.StringVar(
		&runner.args.clientSecret,
		"oauth-client-secret",
		"",
		"Deprecated alternative for the '--client-secret' flag.",
	)
	flags.MarkDeprecated("oauth-client-secret", "use '--client-secret' instead")
	flags.StringSliceVar(
		&runner.args.scopes,
		"oauth-scopes",
		[]string{},
		"Deprecated alternative for the '--scopes' flag.",
	)
	flags.MarkDeprecated("oauth-scopes", "use '--scopes' instead")
	flags.StringVar(
		&runner.args.redirectUri,
		"oauth-redirect-uri",
		defaultRedirectUri,
		"Deprecated alternative for the '--redirect-uri' flag.",
	)
	flags.MarkDeprecated("oauth-redirect-uri", "use '--redirect-uri' instead")
	flags.StringVar(
		&runner.args.user,
		"oauth-user",
		"",
		"Deprecated alternative for the '--user' flag.",
	)
	flags.MarkDeprecated("oauth-user", "use '--user' instead")
	flags.StringVar(
		&runner.args.password,
		"oauth-password",
		"",
		"Deprecated alternative for the '--password' flag.",
	)
	flags.MarkDeprecated("oauth-password", "use '--password' instead")

	// Mark hidden flags:
	flags.MarkHidden("address")
	flags.MarkHidden("private")
	flags.MarkHidden("token")
	flags.MarkHidden("token-script")

	return result
}

type runnerContext struct {
	logger     *slog.Logger
	console    *terminal.Console
	flags      *pflag.FlagSet
	address    string
	plaintext  bool
	caPool     *x509.CertPool
	tokenStore auth.TokenStore
	args       struct {
		plaintext    bool
		insecure     bool
		caFiles      []string
		address      string
		private      bool
		token        string
		tokenScript  string
		issuer       string
		flow         string
		clientId     string
		clientSecret string
		scopes       []string
		redirectUri  string
		user         string
		password     string
	}
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	var err error

	// Get the context:
	ctx := cmd.Context()

	// Get the logger, console and flags:
	c.logger = logging.LoggerFromContext(ctx)
	c.console = terminal.ConsoleFromContext(ctx)
	c.flags = cmd.Flags()

	// Load the templates for the console messages:
	err = c.console.AddTemplates(templatesFS, "templates")
	if err != nil {
		return fmt.Errorf("failed to load templates: %w", err)
	}

	// Infer the OAuth flow from other flags when it hasn't been explicitly set:
	err = c.inferFlow(ctx)
	if err != nil {
		return err
	}

	// The address used to be specified with a command line flag, but now we also take it from the arguments:
	c.address = c.args.address
	if c.address == "" {
		if len(args) == 1 {
			c.address = args[0]
		} else {
			return fmt.Errorf("address is mandatory")
		}
	}

	// Parse the address:
	c.address, c.plaintext, err = c.parseAddress(c.address)
	if err != nil {
		return fmt.Errorf("failed to parse address: %w", err)
	}

	// Check if the plaintext flag has been explcitly set, and if it conflicts with the result of parsing the
	// address. If it does conflict, then explain the issue to the user.
	if c.flags.Changed("plaintext") && c.plaintext != c.args.plaintext {
		c.console.Render(ctx, "plaintext_conflict.txt", map[string]any{
			"Address":   c.address,
			"Plaintext": c.plaintext,
		})
		return exit.Error(1)
	}

	// Create the CA pool:
	c.caPool, err = network.NewCertPool().
		SetLogger(c.logger).
		AddSystemFiles(true).
		AddKubernetesFiles(true).
		AddFiles(c.args.caFiles...).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create CA pool: %w", err)
	}

	// Create an anonymous gRPC client that we will use to fetch the metadata:
	grpcConn, err := network.NewGrpcClient().
		SetLogger(c.logger).
		SetPlaintext(c.plaintext).
		SetInsecure(c.args.insecure).
		SetCaPool(c.caPool).
		SetAddress(c.address).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create anonymous gRPC connection: %w", err)
	}
	defer func() {
		if grpcConn != nil {
			err := grpcConn.Close()
			if err != nil {
				c.logger.ErrorContext(
					ctx,
					"Failed to close gRPC connection",
					slog.Any("error", err),
				)
			}
		}
	}()

	// Fetch the capabilities:
	capabilities, err := c.fetchCapabilities(ctx, grpcConn)
	if err != nil {
		return fmt.Errorf("failed to fetch capabilities: %w", err)
	}
	c.logger.DebugContext(
		ctx,
		"Fetched capabilities",
		slog.Any("capabilities", capabilities),
	)

	// Select the token issuer. The result may be no issuer, which means that no authentication will be used, it
	// will all be anonoymous.
	tokenIssuer, err := c.selectTokenIssuer(ctx, capabilities)
	if err != nil {
		return fmt.Errorf("failed to select token issuer: %w", err)
	}

	// Get the settings from the context, reset them to discard any previous configuration, and create a token store:
	cfg := config.SettingsFromContext(ctx)
	cfg.Reset()
	c.tokenStore = cfg.TokenStore()

	// Create the token source only if a token issuer has been selected.
	tokenSource, err := c.createTokenSource(ctx, tokenIssuer)
	if err != nil {
		return fmt.Errorf("failed to create token source: %w", err)
	}

	// If we got a token source, then try to obtain a token using it, as this will trigger the authentication flow
	// and verify that it works correctly.
	if tokenSource != nil {
		_, err = tokenSource.Token(ctx)
		if err != nil {
			return fmt.Errorf("failed to obtain token using token source: %w", err)
		}
	}

	// Save the basic details of the configuration:
	cfg.SetPlaintext(c.plaintext)
	cfg.SetInsecure(c.args.insecure)
	cfg.SetAddress(c.address)
	cfg.SetPrivate(c.args.private)

	// Store the CA entries. For regular files we save both the absolute path and the content so that the
	// certificate can be reloaded from disk after rotation, with the stored content as a fallback. For directories
	// we only store the absolute path so that their contents are scanned on every invocation, allowing files to be
	// added or removed inside the directory.
	for _, caFile := range c.args.caFiles {
		caPath := filepath.Clean(caFile)
		caPath, err = filepath.Abs(caPath)
		if err != nil {
			return fmt.Errorf("failed to resolve absolute path for '%s': %w", caFile, err)
		}
		caInfo, err := os.Stat(caPath)
		if err != nil {
			return fmt.Errorf("CA path '%s' is not accessible: %w", caPath, err)
		}
		if caInfo.IsDir() {
			cfg.AddCaFile(config.CaFile{
				Name: caPath,
			})
		} else {
			caContent, err := os.ReadFile(caPath)
			if err != nil {
				return fmt.Errorf("failed to read CA file '%s': %w", caPath, err)
			}
			cfg.AddCaFile(config.CaFile{
				Name:    caPath,
				Content: string(caContent),
			})
		}
	}

	// Save the authenticatoin configuration. Note that the OAuth settings are only saved when they are actually
	// used, and they won't be actually used if the user selected to use a static token or a token script.
	if c.args.token != "" {
		cfg.SetAccessToken(c.args.token)
	} else if c.args.tokenScript != "" {
		cfg.SetTokenScript(c.args.tokenScript)
	} else if tokenIssuer != "" {
		cfg.SetIssuer(tokenIssuer)
		cfg.SetFlow(oauth.Flow(c.args.flow))
		cfg.SetClientId(c.args.clientId)
		cfg.SetClientSecret(c.args.clientSecret)
		cfg.SetScopes(c.args.scopes)
		cfg.SetRedirectUri(c.args.redirectUri)
		cfg.SetUser(c.args.user)
		cfg.SetPassword(c.args.password)
	}

	// Replace the gRPC anonymous connection with the authenticated one:
	err = grpcConn.Close()
	if err != nil {
		return fmt.Errorf("failed to close anonymous gRPC connection: %w", err)
	}
	grpcConn, err = network.NewGrpcClient().
		SetLogger(c.logger).
		SetPlaintext(c.plaintext).
		SetInsecure(c.args.insecure).
		SetCaPool(c.caPool).
		SetAddress(c.address).
		SetTokenSource(tokenSource).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create authenticated gRPC connection: %w", err)
	}

	// Check if the configuration is working by invoking the health check method:
	healthClient := healthv1.NewHealthClient(grpcConn)
	healthResponse, err := healthClient.Check(ctx, &healthv1.HealthCheckRequest{})
	if err != nil {
		return err
	}
	if healthResponse.Status != healthv1.HealthCheckResponse_SERVING {
		return fmt.Errorf("server is not serving, status is '%s'", healthResponse.Status)
	}

	// Everything is working, so we can save the configuration:
	err = cfg.Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to save configuration: %w", err)
	}

	return nil
}

// parseAddress parses the address and returns the address and whether accoding to that address the connection should
// use plaintext, without TLS.
func (c *runnerContext) parseAddress(text string) (address string, plaintext bool, err error) {
	parser, err := network.NewAddressParser().
		SetLogger(c.logger).
		Build()
	if err != nil {
		err = fmt.Errorf("failed to create address parser: %w", err)
		return
	}
	address, plaintext, err = parser.Parse(text)
	return
}

func (c *runnerContext) fetchCapabilities(ctx context.Context,
	grpcConn *grpc.ClientConn) (result *publicv1.CapabilitiesGetResponse, err error) {
	capabilitiesClient := publicv1.NewCapabilitiesClient(grpcConn)
	result, err = capabilitiesClient.Get(ctx, publicv1.CapabilitiesGetRequest_builder{}.Build())
	return
}

func (c *runnerContext) selectTokenIssuer(ctx context.Context, capabilities *publicv1.CapabilitiesGetResponse) (result string, err error) {
	advertisedIssuers := capabilities.GetAuthn().GetTrustedTokenIssuers()
	if len(advertisedIssuers) > 0 {
		result = advertisedIssuers[0]
		if len(advertisedIssuers) > 1 {
			c.logger.WarnContext(
				ctx,
				"Server advertises multiple issuers, selecting the first one",
				slog.Any("advertised", advertisedIssuers),
				slog.Any("selected", result),
			)
		}
	} else {
		c.logger.WarnContext(
			ctx,
			"Server advertises no issuers",
			slog.Any("selected", result),
		)
	}
	return
}

// createTokenSource creates a token source from the configuration. The token source will be nil if no token, token
// script or token issuer has been specified.
func (c *runnerContext) createTokenSource(ctx context.Context, tokenIssuer string) (result auth.TokenSource, err error) {
	// Use a token if specified:
	if c.args.token != "" {
		result, err = auth.NewStaticTokenSource().
			SetLogger(c.logger).
			SetToken(&auth.Token{
				Access: c.args.token,
			}).
			Build()
		if err != nil {
			err = fmt.Errorf("failed to create static token source: %w", err)
		}
		return
	}

	// Use a token script if specified::
	if c.args.tokenScript != "" {
		result, err = auth.NewScriptTokenSource().
			SetLogger(c.logger).
			SetScript(c.args.tokenScript).
			SetStore(c.tokenStore).
			Build()
		if err != nil {
			err = fmt.Errorf("failed to create script token source: %w", err)
		}
		return
	}

	// If a token issuer has been selected, then use OAuth to create a token source:
	if tokenIssuer != "" {
		result, err = oauth.NewTokenSource().
			SetLogger(c.logger).
			SetStore(c.tokenStore).
			SetListener(&oauthFlowListener{
				runner: c,
			}).
			SetInsecure(c.args.insecure).
			SetCaPool(c.caPool).
			SetInteractive(true).
			SetIssuer(tokenIssuer).
			SetFlow(oauth.Flow(c.args.flow)).
			SetClientId(c.args.clientId).
			SetClientSecret(c.args.clientSecret).
			SetScopes(c.args.scopes...).
			SetRedirectUri(c.args.redirectUri).
			SetUsername(c.args.user).
			SetPassword(c.args.password).
			Build()
		if err != nil {
			err = fmt.Errorf("failed to create OAuth token source: %w", err)
		}
		return
	}

	// Finally, if there is no token, toke script or token issuer, return nil:
	result = nil
	return
}

// inferFlow infers the OAuth flow from other command line flags when the user hasn't explicitly set the '--flow' flag.
// If '--client-secret' is provided, the flow is inferred to be 'credentials'. If '--user' or '--password' is provided,
// the flow is inferred to be 'password'. If both sets of flags are present without an explicit '--flow', an error is
// returned asking the user to disambiguate.
func (c *runnerContext) inferFlow(ctx context.Context) error {
	if c.flags.Changed("flow") || c.flags.Changed("oauth-flow") {
		return nil
	}
	credentialsHint := c.flags.Changed("client-secret") || c.flags.Changed("oauth-client-secret")
	passwordHint := c.flags.Changed("user") || c.flags.Changed("oauth-user") || c.flags.Changed("password") ||
		c.flags.Changed("oauth-password")
	if credentialsHint && passwordHint {
		c.console.Render(ctx, "ambiguous_flow.txt", nil)
		return exit.Error(1)
	}
	if credentialsHint {
		c.args.flow = string(oauth.CredentialsFlow)
	} else if passwordHint {
		c.args.flow = string(oauth.PasswordFlow)
	}
	return nil
}

type oauthFlowListener struct {
	runner *runnerContext
}

func (l *oauthFlowListener) Start(ctx context.Context, event oauth.FlowStartEvent) error {
	switch event.Flow {
	case oauth.CodeFlow:
		return l.startCodeFlow(ctx, event)
	case oauth.DeviceFlow:
		return l.startDeviceFlow(ctx, event)
	case oauth.CredentialsFlow, oauth.PasswordFlow:
		// These flows don't require user interaction, so there is nothing to do here.
		return nil
	default:
		return fmt.Errorf(
			"unsupported flow '%s', must be '%s', '%s', '%s' or '%s'",
			event.Flow, oauth.CodeFlow, oauth.DeviceFlow, oauth.CredentialsFlow, oauth.PasswordFlow,
		)
	}
}

func (l *oauthFlowListener) startCodeFlow(ctx context.Context, event oauth.FlowStartEvent) error {
	l.runner.console.Render(ctx, "start_code_flow.txt", map[string]any{
		"AuthorizationUri": event.AuthorizationUri,
	})
	return nil
}

func (l *oauthFlowListener) startDeviceFlow(ctx context.Context, event oauth.FlowStartEvent) error {
	// If the authorizatoin server has provided a complete URL, with the code included, then use it, otherwise use
	// the URL without the code:
	verficationUri := event.VerificationUriComplete
	if verficationUri == "" {
		verficationUri = event.VerificationUri
	}

	// Calculate the expiration time to show to the user::
	now := time.Now()
	expiresIn := humanize.RelTime(now, now.Add(event.ExpiresIn), "from now", "")
	l.runner.console.Render(ctx, "start_device_flow.txt", map[string]any{
		"VerificationUri": verficationUri,
		"UserCode":        event.UserCode,
		"ExpiresIn":       expiresIn,
	})
	return nil
}

func (l *oauthFlowListener) End(ctx context.Context, event oauth.FlowEndEvent) error {
	if event.Outcome {
		l.runner.console.Render(ctx, "auth_success.txt", nil)
	} else {
		l.runner.console.Render(ctx, "auth_failure.txt", nil)
	}
	return nil
}

// defaultFlow is the default OAuth flow to use.
const defaultFlow = string(oauth.DeviceFlow)

// defaultClientId is the default OAuth client identifier to use.
const defaultClientId = "osac-cli"

// defaultRedirectUri is the default redirect URI used for the OAuth code flow. The value 'http://localhost:0' means
// binding to localhost on a randomly selected port.
const defaultRedirectUri = "http://localhost:0"

const shortHelp = `Save connection and authentication details`

const longHelp = `
Save connection and authentication details.

The recommended way to specify the server address is with a URL that includes the scheme, host name, and optionally the
port number. For example, {{ bt }}https://osac.example.com{{ bt }} connects to {{ bt }}osac.example.com{{ bt }} using
TLS on port 443. The {{ bt }}https{{ bt }} scheme indicates that TLS should be used, while {{ bt }}http{{ bt }} indicates
plaintext.

Alternatively, the server address can be given as a host name and port number. For example,
{{ bt }}osac.example.com:8000{{ bt }} connects to {{ bt }}osac.example.com{{ bt }} using TLS on port 8000.

Note that the connection always uses _gRPC_ on top of _HTTP/2_, regardless of the format used to specify the server
address.
`

const plaintextFlagHelp = `
_[BOOLEAN]_ - Controls whether TLS is used for communication with the API server. Disabling TLS is insecure and should
only be used for testing. TLS is also automatically disabled when the server address uses the {{ bt }}http{{ bt }} scheme
instead of the default {{ bt }}https{{ bt }}.
`

const insecureFlagHelp = `
_[BOOLEAN]_ - Controls verification of TLS certificates and host names for the OAuth and API servers. Disabling
verification is insecure and should only be used for testing. To trust a custom CA certificate, consider using the
{{ bt }}--ca-file{{ bt }} flag instead.
`

const caFilesFlagHelp = `
_FILE|DIRECTORY_ - File or directory containing trusted CA certificates. When a directory is specified, all files with
the {{ bt }}.cer{{ bt }}, {{ bt }}.crt{{ bt }}, or {{ bt }}.pem{{ bt }} extension are read recursively.

CA certificates trusted by the operating system are loaded automatically.

When running inside a _Kubernetes_ pod, the cluster and service CA certificates are also loaded automatically, so there
is no need to specify them explicitly.

This flag can be specified multiple times to add several CA files or directories.
`

const addressFlagHelp = `
_URL|HOST:PORT_ - Server address.
`

const privateFlagHelp = `
_[BOOLEAN]_ - Enables use of the private API.
`

const tokenFlagHelp = `
_TOKEN_ - Authentication token.
`

const tokenScriptFlagHelp = `
_SCRIPT_ - Shell command executed to obtain the token. For example, to automatically retrieve the token for the
Kubernetes {{ bt }}client{{ bt }} service account in the {{ bt }}example{{ bt }} namespace:

{{ bt 3 }}shell
kubectl create token -n example client --duration 1h
{{ bt 3 }}

Note that it is important to quote this command correctly, as it is passed to your shell for execution.
`

const issuerFlagHelp = `
_URL_ - OAuth issuer URL. This is optional; by default, the issuer advertised by the server is used.
`

const flowFlagHelp = `
_FLOW_ - OAuth flow to use. Must be one of {{ bt }}code{{ bt }}, {{ bt }}device{{ bt }}, {{ bt }}credentials{{ bt }} or
{{ bt }}password{{ bt }}.

When this flag is omitted, the flow is inferred from other flags. If {{ bt }}--client-secret{{ bt }} is provided, the
flow is set to {{ bt }}credentials{{ bt }}. If {{ bt }}--user{{ bt }} or {{ bt }}--password{{ bt }} is provided, the
flow is set to {{ bt }}password{{ bt }}. If both {{ bt }}--client-secret{{ bt }} and {{ bt }}--user{{ bt }} or {{ bt
}}--password{{ bt }} are provided, the flow cannot be inferred and this flag must be set explicitly.
`

const clientIdFlagHelp = `
_ID_ - OAuth client identifier. All authentication flows require a client identifier, but for most flows the default
value {{ bt }}osac-cli{{ bt }} is appropriate. When using the {{ bt }}credentials{{ bt }} flow, typically for service
accounts, you must provide the service account's client identifier along with the {{ bt }}--client-secret{{ bt }} flag.
`

const clientSecretFlagHelp = `
_SECRET_ - OAuth client secret. When using the {{ bt }}credentials{{ bt }} flow, typically for service accounts, you
must provide the service account's client secret along with the {{ bt }}--client-id{{ bt }} flag.
`

const scopesFlagHelp = `
_[SCOPE...]_ - Comma-separated list of OAuth scopes to request.
`

const redirectUriFlagHelp = `
_URI_ - Redirect URI for the OAuth {{ bt }}code{{ bt }} flow. The default value {{ bt }}http://localhost:0{{ bt }} binds
to localhost on a randomly selected port.
`

const userFlagHelp = `
_USER_ - OAuth user name. Required when using the {{ bt }}password{{ bt }} flow, along with the
{{ bt }}--password{{ bt }} flag.
`

const passwordFlagHelp = `
_PASSWORD_ - OAuth password. Required when using the {{ bt }}password{{ bt }} flow, along with the
{{ bt }}--user{{ bt }} flag.
`
