package provisioning

import (
	"fmt"
	"time"
)

// ProviderConfig contains configuration for creating the AAP provisioning provider.
type ProviderConfig struct {
	AAPClient           AAPClient
	ProvisionTemplate   string
	DeprovisionTemplate string

	// TemplatePrefix enables convention-based template name resolution for AAP.
	// When set, template names are derived from the resource Kind:
	//   {prefix}-create-{kind-kebab} and {prefix}-delete-{kind-kebab}
	// Explicit ProvisionTemplate/DeprovisionTemplate take precedence when set.
	TemplatePrefix string
}

// NewProvider creates an AAP provisioning provider from the configuration.
func NewProvider(config ProviderConfig) (ProvisioningProvider, error) {
	if config.AAPClient == nil {
		return nil, fmt.Errorf("AAP provider requires AAPClient")
	}
	return &AAPProvider{
		client:              config.AAPClient,
		provisionTemplate:   config.ProvisionTemplate,
		deprovisionTemplate: config.DeprovisionTemplate,
		templatePrefix:      config.TemplatePrefix,
	}, nil
}

const (
	// DefaultStatusPollInterval is the default interval for polling provider status.
	DefaultStatusPollInterval = 30 * time.Second

	// DefaultMaxJobHistory is the default number of jobs to keep per job array.
	DefaultMaxJobHistory = 10
)
