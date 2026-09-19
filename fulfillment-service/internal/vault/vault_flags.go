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

// BaseConfig holds the vault connection settings shared by all service configurations
// that interact with Vault
type BaseConfig struct {
	Endpoint                 string
	Namespace                string
	KVMountPath              string
	CaCertFile               string
	KeycloakIssuerURL        string
	KeycloakClientID         string
	KeycloakClientSecretFile string
	KeycloakAudience         string
}

// LifecycleConfig holds the additional settings needed for tenant
// namespace lifecycle management in Vault.
type LifecycleConfig struct {
	Role      string
	MountPath string
}

func getString(flags *pflag.FlagSet, name string) (string, error) {
	v, err := flags.GetString(name)
	if err != nil {
		return "", fmt.Errorf("failed to read flag '--%s': %w", name, err)
	}
	return v, nil
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
	_ = flags.String(
		keycloakIssuerURLFlagName,
		"",
		keycloakIssuerURLFlagHelp,
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
	_ = flags.String(
		keycloakAudienceFlagName,
		"osac-api",
		keycloakAudienceFlagHelp,
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
}

// BaseConfigFromFlags reads the base vault flags and returns a populated BaseConfig.
func BaseConfigFromFlags(flags *pflag.FlagSet) (BaseConfig, error) {
	endpoint, err := getString(flags, endpointFlagName)
	if err != nil {
		return BaseConfig{}, err
	}
	namespace, err := getString(flags, namespaceFlagName)
	if err != nil {
		return BaseConfig{}, err
	}
	kvMountPath, err := getString(flags, kvMountPathFlagName)
	if err != nil {
		return BaseConfig{}, err
	}
	caCertFile, err := getString(flags, caCertFileFlagName)
	if err != nil {
		return BaseConfig{}, err
	}
	issuerURL, err := getString(flags, keycloakIssuerURLFlagName)
	if err != nil {
		return BaseConfig{}, err
	}
	clientID, err := getString(flags, keycloakClientIDFlagName)
	if err != nil {
		return BaseConfig{}, err
	}
	clientSecretFile, err := getString(flags, keycloakClientSecretFileFlagName)
	if err != nil {
		return BaseConfig{}, err
	}
	audience, err := getString(flags, keycloakAudienceFlagName)
	if err != nil {
		return BaseConfig{}, err
	}
	return BaseConfig{
		Endpoint:                 endpoint,
		Namespace:                namespace,
		KVMountPath:              kvMountPath,
		CaCertFile:               caCertFile,
		KeycloakIssuerURL:        issuerURL,
		KeycloakClientID:         clientID,
		KeycloakClientSecretFile: clientSecretFile,
		KeycloakAudience:         audience,
	}, nil
}

// LifecycleConfigFromFlags reads the lifecycle vault flags and returns a populated LifecycleConfig.
func LifecycleConfigFromFlags(flags *pflag.FlagSet) (LifecycleConfig, error) {
	role, err := getString(flags, lifecycleRoleFlagName)
	if err != nil {
		return LifecycleConfig{}, err
	}
	mountPath, err := getString(flags, lifecycleMountPathFlagName)
	if err != nil {
		return LifecycleConfig{}, err
	}

	return LifecycleConfig{
		Role:      role,
		MountPath: mountPath,
	}, nil
}

// ValidateBaseKeycloakConfig checks that the Keycloak client credential
// fields are set.
func ValidateBaseKeycloakConfig(cfg BaseConfig) error {
	if cfg.KeycloakIssuerURL == "" {
		return fmt.Errorf(
			"flag '--%s' is required when '--%s' is set",
			keycloakIssuerURLFlagName, endpointFlagName,
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

// ValidateBaseConfig checks that all Vault settings required by the service are set.
func ValidateBaseConfig(cfg BaseConfig) error {
	if cfg.Endpoint == "" {
		return fmt.Errorf("flag '--%s' is required", endpointFlagName)
	}
	if cfg.Namespace == "" {
		return fmt.Errorf("flag '--%s' is required", namespaceFlagName)
	}
	if cfg.KVMountPath == "" {
		return fmt.Errorf("flag '--%s' is required", kvMountPathFlagName)
	}
	return ValidateBaseKeycloakConfig(cfg)
}

func ValidateLifecycleConfig(cfg LifecycleConfig) error {
	if cfg.Role == "" {
		return fmt.Errorf(
			"flag '--%s' is required when '--%s' is set",
			lifecycleRoleFlagName, endpointFlagName,
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

const keycloakClientIDFlagHelp = `
_ID_ - Keycloak client identifier used for Vault authentication.
`

const keycloakClientSecretFileFlagHelp = `
_FILE_ - File containing the Keycloak client secret used for
Vault authentication.
`

const caCertFileFlagHelp = `
_FILE_ - File containing CA certificates for TLS connections to the
Vault-compatible secret store. When not set, the shared CA pool is used.
`
