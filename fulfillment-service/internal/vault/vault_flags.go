/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package vault

import (
	"fmt"

	"github.com/spf13/pflag"
)

// BaseConfig holds the vault connection settings shared by all commands
// that interact with Vault.
type BaseConfig struct {
	Endpoint    string
	Namespace   string
	KVMountPath string
	CaCertFile  string
}

// LifecycleConfig holds the additional settings needed for tenant
// namespace lifecycle management in Vault.
type LifecycleConfig struct {
	Role                     string
	MountPath                string
	KeycloakIssuerURL        string
	KeycloakAudience         string
	KeycloakTokenEndpoint    string
	KeycloakClientID         string
	KeycloakClientSecretFile string
}

// AddBaseFlags registers shared vault flags among all clients/callers.
func AddBaseFlags(flags *pflag.FlagSet) {
	_ = flags.String(
		endpointFlagName,
		"",
		endpointFlagHelp,
	)
	_ = flags.String(
		namespaceFlagName,
		"osac",
		namespaceFlagHelp,
	)
	_ = flags.String(
		kvMountPathFlagName,
		"secret",
		kvMountPathFlagHelp,
	)
	_ = flags.String(
		caCertFileFlagName,
		"",
		caCertFileFlagHelp,
	)
}

// AddLifecycleFlags registers the vault flags needed for tenant namespace
// lifecycle management. Call AddBaseFlags first; these extend the base set.
func AddLifecycleFlags(flags *pflag.FlagSet) {
	_ = flags.String(
		lifecycleRoleFlagName,
		"",
		lifecycleRoleFlagHelp,
	)
	_ = flags.String(
		lifecycleMountPathFlagName,
		"jwt",
		lifecycleMountPathFlagHelp,
	)
	_ = flags.String(
		keycloakIssuerURLFlagName,
		"",
		keycloakIssuerURLFlagHelp,
	)
	_ = flags.String(
		keycloakAudienceFlagName,
		"osac-api",
		keycloakAudienceFlagHelp,
	)
	_ = flags.String(
		keycloakTokenEndpointFlagName,
		"",
		keycloakTokenEndpointFlagHelp,
	)
	_ = flags.String(
		keycloakClientIDFlagName,
		"",
		keycloakClientIDFlagHelp,
	)
	_ = flags.String(
		keycloakClientSecretFileFlagName,
		"",
		keycloakClientSecretFileFlagHelp,
	)
}

// BaseConfigFromFlags reads the base vault flags and returns a populated BaseConfig.
func BaseConfigFromFlags(flags *pflag.FlagSet) (BaseConfig, error) {
	endpoint, err := flags.GetString(endpointFlagName)
	if err != nil {
		return BaseConfig{}, fmt.Errorf("failed to read flag '--%s': %w", endpointFlagName, err)
	}
	namespace, err := flags.GetString(namespaceFlagName)
	if err != nil {
		return BaseConfig{}, fmt.Errorf("failed to read flag '--%s': %w", namespaceFlagName, err)
	}
	kvMountPath, err := flags.GetString(kvMountPathFlagName)
	if err != nil {
		return BaseConfig{}, fmt.Errorf("failed to read flag '--%s': %w", kvMountPathFlagName, err)
	}
	caCertFile, err := flags.GetString(caCertFileFlagName)
	if err != nil {
		return BaseConfig{}, fmt.Errorf("failed to read flag '--%s': %w", caCertFileFlagName, err)
	}
	return BaseConfig{
		Endpoint:    endpoint,
		Namespace:   namespace,
		KVMountPath: kvMountPath,
		CaCertFile:  caCertFile,
	}, nil
}

// LifecycleConfigFromFlags reads the lifecycle vault flags and returns a populated LifecycleConfig.
func LifecycleConfigFromFlags(flags *pflag.FlagSet) (LifecycleConfig, error) {
	getString := func(name string) (string, error) {
		v, err := flags.GetString(name)
		if err != nil {
			return "", fmt.Errorf("failed to read flag '--%s': %w", name, err)
		}
		return v, nil
	}

	role, err := getString(lifecycleRoleFlagName)
	if err != nil {
		return LifecycleConfig{}, err
	}
	mountPath, err := getString(lifecycleMountPathFlagName)
	if err != nil {
		return LifecycleConfig{}, err
	}
	issuerURL, err := getString(keycloakIssuerURLFlagName)
	if err != nil {
		return LifecycleConfig{}, err
	}
	audience, err := getString(keycloakAudienceFlagName)
	if err != nil {
		return LifecycleConfig{}, err
	}
	tokenEndpoint, err := getString(keycloakTokenEndpointFlagName)
	if err != nil {
		return LifecycleConfig{}, err
	}
	clientID, err := getString(keycloakClientIDFlagName)
	if err != nil {
		return LifecycleConfig{}, err
	}
	clientSecretFile, err := getString(keycloakClientSecretFileFlagName)
	if err != nil {
		return LifecycleConfig{}, err
	}

	return LifecycleConfig{
		Role:                     role,
		MountPath:                mountPath,
		KeycloakIssuerURL:        issuerURL,
		KeycloakAudience:         audience,
		KeycloakTokenEndpoint:    tokenEndpoint,
		KeycloakClientID:         clientID,
		KeycloakClientSecretFile: clientSecretFile,
	}, nil
}

func ValidateLifecycleConfig(cfg LifecycleConfig) error {
	if cfg.KeycloakIssuerURL == "" {
		return fmt.Errorf(
			"flag '--%s' is required when '--%s' is set",
			keycloakIssuerURLFlagName, endpointFlagName,
		)
	}
	if cfg.Role == "" {
		return fmt.Errorf(
			"flag '--%s' is required when '--%s' is set",
			lifecycleRoleFlagName, endpointFlagName,
		)
	}
	if cfg.KeycloakTokenEndpoint == "" {
		return fmt.Errorf(
			"flag '--%s' is required when '--%s' is set",
			keycloakTokenEndpointFlagName, endpointFlagName,
		)
	}
	if cfg.KeycloakClientID == "" {
		return fmt.Errorf(
			"flag '--%s' is required when '--%s' is set",
			keycloakClientIDFlagName, endpointFlagName,
		)
	}
	if cfg.KeycloakClientSecretFile == "" {
		return fmt.Errorf(
			"flag '--%s' is required when '--%s' is set",
			keycloakClientSecretFileFlagName, endpointFlagName,
		)
	}
	return nil
}

const (
	endpointFlagName    = "vault-endpoint"
	namespaceFlagName   = "vault-namespace"
	kvMountPathFlagName = "vault-kv-mount-path"

	lifecycleRoleFlagName            = "vault-lifecycle-role"
	lifecycleMountPathFlagName       = "vault-lifecycle-mount-path"
	keycloakIssuerURLFlagName        = "vault-keycloak-issuer-url"
	keycloakAudienceFlagName         = "vault-keycloak-audience"
	keycloakTokenEndpointFlagName    = "vault-keycloak-token-endpoint"
	keycloakClientIDFlagName         = "vault-keycloak-client-id"
	keycloakClientSecretFileFlagName = "vault-keycloak-client-secret-file"
	caCertFileFlagName               = "vault-ca-cert-file"
)

const endpointFlagHelp = `
_URL_ - Vault API endpoint URL.
`

const namespaceFlagHelp = `
_NAMESPACE_ - Parent namespace path within the Vault-compatible
store. Tenant namespaces are created as children of this namespace.
`

const kvMountPathFlagHelp = `
_PATH_ - KV v2 secret engine mount path within a tenant namespaces.
`

const lifecycleRoleFlagHelp = `
_ROLE_ - Vault role name used when authenticating with JWT for
lifecycle operations.
`

const lifecycleMountPathFlagHelp = `
_PATH_ - Auth method mount path in the Vault parent namespace
used for lifecycle JWT authentication.
`

const keycloakIssuerURLFlagHelp = `
_URL_ - Keycloak OIDC issuer URL (e.g. https://keycloak/realms/osac)
used to configure JWT auth in tenant Vault namespaces.
`

const keycloakAudienceFlagHelp = `
_AUDIENCE_ - Expected audience claim in Keycloak JWTs for Vault
JWT auth role configuration.
`

const keycloakTokenEndpointFlagHelp = `
_URL_ - Keycloak token endpoint URL used by the controller to
obtain JWTs for Vault authentication via client credentials flow.
`

const keycloakClientIDFlagHelp = `
_ID_ - Keycloak client identifier used by the controller for
Vault authentication.
`

const keycloakClientSecretFileFlagHelp = `
_FILE_ - File containing the Keycloak client secret used by the
controller for Vault authentication.
`

const caCertFileFlagHelp = `
_FILE_ - File containing CA certificates for TLS connections to the
Vault-compatible secret store. When not set, the shared CA pool is used.
`
