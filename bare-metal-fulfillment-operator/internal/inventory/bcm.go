/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/certwatcher"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/baremetalhost"
)

const certBaseDir = "/etc/osac/certs"

// BCMAPI defines the BCM client operations needed by the inventory adapter.
// Satisfied by *bcmclient.Client; defined here so tests can substitute a mock
// without depending on the bcmclient package.
type BCMAPI interface {
	CertWatcher() *certwatcher.CertWatcher
}

var _ Client = (*BCMClient)(nil)

// BCMClientConfig holds the BCM-specific options parsed from the inventory config.
type BCMClientConfig struct {
	URL                string `json:"url"`
	CredentialsSecret  string `json:"credentialsSecret"`
	InsecureSkipVerify bool   `json:"insecureSkipVerify"`
}

// ParseBCMOptions extracts and validates BCM options from the inventory config.
func ParseBCMOptions(options map[string]any) (*BCMClientConfig, error) {
	bcmOpts, ok := options["bcm"]
	if !ok {
		return nil, fmt.Errorf("bcm options not found in config")
	}

	raw, err := json.Marshal(bcmOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal bcm options: %w", err)
	}

	var cfg BCMClientConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal bcm options: %w", err)
	}

	if cfg.URL == "" {
		return nil, fmt.Errorf("bcm url is required in config")
	}
	if cfg.CredentialsSecret == "" {
		return nil, fmt.Errorf("bcm credentialsSecret is required in config")
	}

	certDir := filepath.Join(certBaseDir, cfg.CredentialsSecret)
	if !strings.HasPrefix(certDir, certBaseDir+"/") {
		return nil, fmt.Errorf("bcm credentialsSecret resolves outside cert directory")
	}

	return &cfg, nil
}

// BCMClient implements inventory.Client by wrapping a BCMAPI
// for BCM API communication and a baremetalhost.Manager for BMH lifecycle.
type BCMClient struct {
	client     BCMAPI
	bmhManager *baremetalhost.Manager
	hostClass  string
}

// NewBCMClient creates a BCM inventory client with injected dependencies.
func NewBCMClient(client BCMAPI, bmhManager *baremetalhost.Manager, hostClass string) *BCMClient {
	return &BCMClient{
		client:     client,
		bmhManager: bmhManager,
		hostClass:  hostClass,
	}
}

// CertWatcher returns the certificate watcher for registration with the
// controller manager. The manager calls Start on it to enable automatic
// certificate rotation.
func (c *BCMClient) CertWatcher() *certwatcher.CertWatcher {
	return c.client.CertWatcher()
}

// FindFreeHost is implemented in OSAC-3766.
func (c *BCMClient) FindFreeHost(_ context.Context, _ map[string]string) (*Host, error) {
	return nil, fmt.Errorf("bcm FindFreeHost not implemented")
}

// AssignHost is implemented in OSAC-3767 through OSAC-3769.
func (c *BCMClient) AssignHost(_ context.Context, _ string, _ string, _ map[string]string) (*Host, error) {
	return nil, fmt.Errorf("bcm AssignHost not implemented")
}

// UnassignHost is implemented in OSAC-3771.
func (c *BCMClient) UnassignHost(_ context.Context, _ string, _ []string) error {
	return fmt.Errorf("bcm UnassignHost not implemented")
}

func (c *BCMClient) GetHostNICs(_ context.Context, _ string) ([]HostNIC, error) {
	return nil, nil
}
