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
	"math/rand"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/certwatcher"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/baremetalhost"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/bcmclient"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/shared"
)

const certBaseDir = "/etc/osac/certs"

// BCMAPI defines the BCM client operations needed by the inventory adapter.
// Satisfied by *bcmclient.Client; defined here so tests can substitute a mock
// without depending on the bcmclient package.
type BCMAPI interface {
	CertWatcher() *certwatcher.CertWatcher
	GetDevices(ctx context.Context) ([]bcmclient.Device, error)
}

var (
	_ Client = (*BCMClient)(nil)

	macPattern      = regexp.MustCompile(`^[0-9a-fA-F]{2}(:[0-9a-fA-F]{2}){5}$`)
	hostnamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9\-]*[a-z0-9])?$`)

	bcmHostsAvailable = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "osac_bcm_hosts_available",
			Help: "Number of unassigned BCM LiteNodes by host type",
		},
		[]string{"host_type"},
	)
)

func init() {
	metrics.Registry.MustRegister(bcmHostsAvailable)
}

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

// FindFreeHost queries BCM for all devices and returns a randomly selected
// free LiteNode matching the requested hostType. All filtering is client-side
// because the BCM JSON API has no server-side filtering.
func (c *BCMClient) FindFreeHost(ctx context.Context, matchExpressions map[string]string) (*Host, error) {
	log := ctrllog.FromContext(ctx)
	log.Info("Finding free BCM host")

	matchManagedBy := matchExpressions["managedBy"]
	if matchManagedBy == "" {
		matchManagedBy = shared.OsacDefaultManagedByValue
	}
	if matchManagedBy != shared.OsacDefaultManagedByValue {
		return nil, nil
	}

	devices, err := c.client.GetDevices(ctx)
	if err != nil {
		return nil, fmt.Errorf("FindFreeHost: %w", err)
	}

	hostType := matchExpressions["hostType"]

	candidates := make([]bcmclient.Device, 0, len(devices))
	for _, d := range devices {
		if d.ChildType != "LiteNode" {
			continue
		}

		if d.ExtraValues == nil {
			continue
		}

		resourceClass, _ := d.ExtraValues[bcmclient.ExtraValueResourceClass].(string)
		if resourceClass == "" {
			continue
		}

		if hostType != "" && resourceClass != hostType {
			continue
		}

		if _, assigned := d.ExtraValues[bcmclient.ExtraValueInstanceID]; assigned {
			continue
		}

		if !hostnamePattern.MatchString(d.Hostname) || len(d.Hostname) > 63 {
			continue
		}

		if !macPattern.MatchString(d.MAC) || d.MAC == "00:00:00:00:00:00" {
			continue
		}

		candidates = append(candidates, d)
	}

	bcmHostsAvailable.Reset()
	availableByType := map[string]float64{}
	for _, cd := range candidates {
		rc, _ := cd.ExtraValues[bcmclient.ExtraValueResourceClass].(string)
		availableByType[rc]++
	}
	for t, count := range availableByType {
		bcmHostsAvailable.WithLabelValues(t).Set(count)
	}

	if len(candidates) == 0 {
		return nil, nil
	}

	rand.Shuffle(len(candidates), func(i, j int) {
		candidates[i], candidates[j] = candidates[j], candidates[i]
	})

	selected := &candidates[0]
	resourceClass, _ := selected.ExtraValues[bcmclient.ExtraValueResourceClass].(string)

	return &Host{
		InventoryHostID: fmt.Sprintf("%s/%s", c.bmhManager.Namespace(), selected.Hostname),
		Name:            selected.Hostname,
		HostType:        resourceClass,
		HostClass:       c.hostClass,
		ManagedBy:       shared.OsacDefaultManagedByValue,
	}, nil
}

// AssignHost is implemented in OSAC-3767 through OSAC-3769.
func (c *BCMClient) AssignHost(_ context.Context, _ string, _ string, _ map[string]string) (*Host, error) {
	return nil, fmt.Errorf("bcm AssignHost not implemented")
}

// UnassignHost is implemented in OSAC-3771.
func (c *BCMClient) UnassignHost(_ context.Context, _ string, _ []string) error {
	return fmt.Errorf("bcm UnassignHost not implemented")
}

// GetHostNICs reads all NIC MAC addresses from the BareMetalHost CR hardware inspection data.
// inventoryHostID must be in namespace/hostname format where hostname is the BMH name.
// Reading from the BMH CR avoids a costly GetDevices round-trip to the BCM API.
// Returns nil, nil when the BMH has no hardware inspection data (caller treats this as "NIC data unavailable").
func (c *BCMClient) GetHostNICs(ctx context.Context, inventoryHostID string) ([]HostNIC, error) {
	parts := strings.SplitN(inventoryHostID, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid inventoryHostID %q: expected namespace/hostname format", inventoryHostID)
	}
	bmhName := parts[1]

	macs, err := c.bmhManager.GetHardwareNICs(ctx, bmhName)
	if err != nil {
		return nil, fmt.Errorf("GetHostNICs: failed to get BMH %s: %w", bmhName, err)
	}
	if len(macs) == 0 {
		return nil, nil
	}
	nics := make([]HostNIC, 0, len(macs))
	for _, mac := range macs {
		nics = append(nics, HostNIC{MAC: mac})
	}
	return nics, nil
}
