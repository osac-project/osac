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
	"sort"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/certwatcher"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/baremetalhost"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/bcmclient"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/bmcdiscovery"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/shared"
)

const certBaseDir = "/etc/osac/certs"

// BCMAPI defines the BCM client operations needed by the inventory adapter.
// Satisfied by *bcmclient.Client; defined here so tests can substitute a mock
// without depending on the bcmclient package.
//
//go:generate mockgen -destination=bcm_mock_test.go -package=inventory . BCMAPI,BMHLifecycleManager,BMCDiscoverer
type BCMAPI interface {
	CertWatcher() *certwatcher.CertWatcher
	GetDevices(ctx context.Context) ([]bcmclient.Device, error)
	GetDevice(ctx context.Context, hostname string) (*bcmclient.Device, error)
	GetCategories(ctx context.Context) ([]bcmclient.Category, error)
	GetPartitions(ctx context.Context) ([]bcmclient.Partition, error)
	UpdateDevice(ctx context.Context, deviceRaw json.RawMessage) (*bcmclient.UpdateResponse, error)
}

// BMHLifecycleManager abstracts BareMetalHost CR operations for testability.
// Satisfied by *baremetalhost.Manager.
type BMHLifecycleManager interface {
	CreateBMH(ctx context.Context, params baremetalhost.CreateParams) error
	DeleteBMH(ctx context.Context, name string) error
	BMHExists(ctx context.Context, name string) (bool, error)
	IsBMHReady(ctx context.Context, name string) (bool, error)
	EnsureBMCSecret(ctx context.Context, name, username, password string) error
	DeleteBMCSecret(ctx context.Context, name string) error
	GetHardwareNICs(ctx context.Context, name string) ([]string, error)
	Namespace() string
}

// BMCDiscoverer resolves BMC system paths via Redfish. Satisfied by
// *bmcdiscovery.GofishDiscoverer; defined here so tests can substitute
// a mock without making real Redfish connections.
type BMCDiscoverer interface {
	DiscoverSystemPath(ctx context.Context, bmcIP, bootMAC, username, password string) (string, error)
}

const bmcInterfaceChildType = "NetworkBmcInterface"

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
// for BCM API communication and a BMHLifecycleManager for BMH lifecycle.
type BCMClient struct {
	client        BCMAPI
	bmhManager    BMHLifecycleManager
	bmcDiscoverer BMCDiscoverer
	hostClass     string
}

// NewBCMClient creates a BCM inventory client with injected dependencies.
func NewBCMClient(client BCMAPI, bmhManager BMHLifecycleManager, hostClass string) *BCMClient {
	return &BCMClient{
		client:     client,
		bmhManager: bmhManager,
		hostClass:  hostClass,
	}
}

// SetBMCDiscoverer sets the Redfish discoverer used for Priority 2 BMC
// address resolution. When nil, Redfish-compatible protocols return an
// error; only IPMI (static URL) works without a discoverer.
func (c *BCMClient) SetBMCDiscoverer(d BMCDiscoverer) {
	c.bmcDiscoverer = d
}

// CertWatcher returns the certificate watcher for registration with the
// controller manager. The manager calls Start on it to enable automatic
// certificate rotation.
func (c *BCMClient) CertWatcher() *certwatcher.CertWatcher {
	return c.client.CertWatcher()
}

// Selector keys with dedicated handling, excluded from the generic label match.
const (
	managedByKey      = "managedBy"
	provisionStateKey = "provisionState"
)

// reservedExtraValueKeys are OSAC-internal extra_values that must never be
// treated as selectable host labels.
var reservedExtraValueKeys = map[string]bool{
	bcmclient.ExtraValueInstanceID:     true,
	bcmclient.ExtraValueBMCAddress:     true,
	bcmclient.ExtraValueBMCCredentials: true,
}

// FindFreeHost queries BCM for all devices and returns a randomly selected free
// LiteNode whose extra_values satisfy the selector's match labels. All filtering
// is client-side because the BCM JSON API has no server-side filtering.
//
// Selector keys are matched directly against the device's extra_values — BCM's
// native metadata bag, where resource_class already lives — excluding
// OSAC-internal keys. managedBy is handled separately as a default-aware
// ownership guard, and provisionState is excluded from the label match (it has
// no BCM analog).
func (c *BCMClient) FindFreeHost(ctx context.Context, matchExpressions map[string]string) (*Host, error) {
	if err := validateBCMMatchExpressions(matchExpressions); err != nil {
		return nil, err
	}

	log := ctrllog.FromContext(ctx)
	log.Info("Finding free BCM host", "selectorKeys", sortedKeys(matchExpressions))

	matchManagedBy := matchExpressions[managedByKey]
	if matchManagedBy == "" {
		matchManagedBy = shared.OsacDefaultManagedByValue
	}

	// Generic label match: the selector minus keys with dedicated handling.
	labelMatchExpressions := make(map[string]string, len(matchExpressions))
	for key, value := range matchExpressions {
		if key == managedByKey || key == provisionStateKey {
			continue
		}
		labelMatchExpressions[key] = value
	}

	devices, err := c.client.GetDevices(ctx)
	if err != nil {
		return nil, fmt.Errorf("FindFreeHost: %w", err)
	}

	candidates := make([]bcmclient.Device, 0, len(devices))
	for _, d := range devices {
		if d.ChildType != "LiteNode" || d.ExtraValues == nil {
			continue
		}

		if _, assigned := d.ExtraValues[bcmclient.ExtraValueInstanceID]; assigned {
			continue
		}

		if !deviceMatchesLabels(d, labelMatchExpressions) {
			continue
		}

		if deviceManagedBy(d) != matchManagedBy {
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

	updateAvailableMetric(candidates)

	if len(candidates) == 0 {
		log.Info("no free BCM host matches selector", "selectorKeys", sortedKeys(labelMatchExpressions))
		return nil, nil
	}

	rand.Shuffle(len(candidates), func(i, j int) {
		candidates[i], candidates[j] = candidates[j], candidates[i]
	})

	selected := &candidates[0]
	// resource_class is returned as HostType for reporting only; it is no longer a
	// filter dimension — label selection replaces the legacy hostType/resource_class
	// matching.
	resourceClass, _ := selected.ExtraValues[bcmclient.ExtraValueResourceClass].(string)

	return &Host{
		InventoryHostID: fmt.Sprintf("%s/%s", c.bmhManager.Namespace(), selected.Hostname),
		Name:            selected.Hostname,
		HostType:        resourceClass,
		HostClass:       c.hostClass,
		ManagedBy:       matchManagedBy,
	}, nil
}

// validateBCMMatchExpressions validates the selector before any BCM query. It
// mirrors the OpenStack backend's permissive rules: BCM labels are arbitrary
// extra_values keys filtered client-side, not Kubernetes labels, so Kubernetes
// label syntax is intentionally not enforced here.
func validateBCMMatchExpressions(matchExpressions map[string]string) error {
	if len(matchExpressions) == 0 {
		return fmt.Errorf("invalid matchExpressions: empty map")
	}
	for key, value := range matchExpressions {
		if key == "" {
			return fmt.Errorf("invalid matchExpression: empty key not allowed")
		}
		if strings.Contains(key, " ") {
			return fmt.Errorf("invalid matchExpression: key %q contains spaces", key)
		}
		if value == "" {
			return fmt.Errorf("invalid matchExpression: empty value not allowed for key %q", key)
		}
	}
	return nil
}

// deviceMatchesLabels reports whether the device satisfies every selector
// key=value (string equality, AND across all keys). Selector keys are looked up
// directly in the device's extra_values (no intermediate map is built);
// OSAC-internal keys can never match a selector label.
func deviceMatchesLabels(d bcmclient.Device, matchExpressions map[string]string) bool {
	for key, want := range matchExpressions {
		if reservedExtraValueKeys[key] {
			return false
		}
		got, ok := d.ExtraValues[key].(string)
		if !ok || got != want {
			return false
		}
	}
	return true
}

// deviceManagedBy returns the device's managedBy label, defaulting to the
// standard owner when absent or empty.
func deviceManagedBy(d bcmclient.Device) string {
	if v, ok := d.ExtraValues[managedByKey].(string); ok && v != "" {
		return v
	}
	return shared.OsacDefaultManagedByValue
}

// updateAvailableMetric refreshes the available-hosts gauge, keyed by
// resource_class as a best-effort reporting dimension.
func updateAvailableMetric(candidates []bcmclient.Device) {
	bcmHostsAvailable.Reset()
	availableByType := map[string]float64{}
	for _, cd := range candidates {
		rc, _ := cd.ExtraValues[bcmclient.ExtraValueResourceClass].(string)
		availableByType[rc]++
	}
	for t, count := range availableByType {
		bcmHostsAvailable.WithLabelValues(t).Set(count)
	}
}

// sortedKeys returns the map's keys sorted, for stable, value-free logging.
// Selector values may be sensitive and are never logged.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// AssignHost records the assignment identifier in BCM, resolves the BMC
// address, and creates a BareMetalHost CR for Metal3 power management.
// Uses full-object replacement because BCM's updateDevice rejects partial
// objects. Only osac_instance_id is written — no tenant-identifying data.
func (c *BCMClient) AssignHost(ctx context.Context, inventoryHostID string, bareMetalInstanceID string, _ map[string]string) (*Host, error) {
	if bareMetalInstanceID == "" {
		return nil, fmt.Errorf("invalid input: bareMetalInstanceID is empty")
	}
	_, hostname, err := ParseHostID(inventoryHostID)
	if err != nil {
		return nil, err
	}
	ctrllog.FromContext(ctx).Info("Assigning BCM host", "hostname", hostname, "bareMetalInstanceID", bareMetalInstanceID)

	// A nil device (with nil error) means the host is unavailable — return
	// nil,nil so the controller retries later.
	device, err := c.reserveDevice(ctx, hostname, bareMetalInstanceID)
	if err != nil || device == nil {
		return nil, err
	}
	// Ensure the BareMetalHost exists (idempotent), then report readiness. The
	// controller keeps the instance in Allocating and requeues until Ready.
	if err := c.ensureBMHExists(ctx, device, bareMetalInstanceID); err != nil {
		return nil, err
	}
	ready, err := c.bmhManager.IsBMHReady(ctx, device.Hostname)
	if err != nil {
		return nil, err
	}
	host := c.buildHost(device)
	host.Ready = ready
	return host, nil
}

// reserveDevice fetches the BCM device and reserves it (writes osac_instance_id)
// for bareMetalInstanceID. It returns (nil, nil) when the host is unavailable:
// owned by another instance, lost a concurrent write race, or no longer in BCM.
func (c *BCMClient) reserveDevice(ctx context.Context, hostname, bareMetalInstanceID string) (*bcmclient.Device, error) {
	device, err := c.client.GetDevice(ctx, hostname)
	if err != nil {
		return nil, fmt.Errorf("failed to get BCM device %s: %w", hostname, err)
	}
	if device == nil {
		_, err := c.handleDeviceNotFound(ctx, hostname)
		return nil, err
	}
	if existingID, ok := instanceID(device); ok {
		return c.deviceIfOwnedBy(ctx, device, hostname, existingID, bareMetalInstanceID)
	}
	return c.writeAndVerifyAssignment(ctx, device, hostname, bareMetalInstanceID)
}

// deviceIfOwnedBy returns the device when it is already reserved for wantID
// (skip-write / crash-recovery), or (nil, nil) when another instance owns it.
func (c *BCMClient) deviceIfOwnedBy(ctx context.Context, device *bcmclient.Device, hostname, existingID, wantID string) (*bcmclient.Device, error) {
	log := ctrllog.FromContext(ctx)
	if existingID != wantID {
		log.Info("BCM host assigned to another instance", "hostname", hostname, "existingInstanceID", existingID)
		return nil, nil
	}
	log.Info("BCM host already assigned to this instance, skipping write", "hostname", hostname)
	return device, nil
}

// instanceID returns the osac_instance_id from the device's extra_values.
func instanceID(device *bcmclient.Device) (string, bool) {
	if device.ExtraValues == nil {
		return "", false
	}
	v, ok := device.ExtraValues[bcmclient.ExtraValueInstanceID]
	if !ok {
		return "", false
	}
	s, _ := v.(string)
	return s, true
}

func (c *BCMClient) handleDeviceNotFound(ctx context.Context, hostname string) (*Host, error) {
	log := ctrllog.FromContext(ctx)

	exists, err := c.bmhManager.BMHExists(ctx, hostname)
	if err != nil {
		return nil, err
	}
	if !exists {
		log.Info("BCM device not found and no BMH exists, clearing for retry", "hostname", hostname)
		return nil, nil
	}

	return nil, fmt.Errorf(
		"BCM device %q no longer exists in BCM inventory but BareMetalHost CR exists"+
			" — delete the BareMetalInstance or re-register the device in BCM", hostname)
}

func (c *BCMClient) writeAndVerifyAssignment(ctx context.Context, device *bcmclient.Device, hostname, bareMetalInstanceID string) (*bcmclient.Device, error) {
	raw, err := bcmclient.SetExtraValue(device.Raw, bcmclient.ExtraValueInstanceID, bareMetalInstanceID)
	if err != nil {
		return nil, fmt.Errorf("failed to set instance ID on BCM device %s: %w", hostname, err)
	}

	if _, err := c.client.UpdateDevice(ctx, raw); err != nil {
		return nil, fmt.Errorf("failed to update BCM device %s: %w", hostname, err)
	}

	return c.verifyAssignment(ctx, hostname, bareMetalInstanceID)
}

func (c *BCMClient) verifyAssignment(ctx context.Context, hostname, bareMetalInstanceID string) (*bcmclient.Device, error) {
	log := ctrllog.FromContext(ctx)

	device, err := c.client.GetDevice(ctx, hostname)
	if err != nil {
		return nil, fmt.Errorf("failed to verify BCM assignment for %s: %w", hostname, err)
	}

	if device == nil {
		log.Info("BCM device disappeared during verify-after-write", "hostname", hostname)
		return nil, nil
	}

	verifiedID, _ := device.ExtraValues[bcmclient.ExtraValueInstanceID].(string)
	if verifiedID != bareMetalInstanceID {
		log.Info("BCM assignment overwritten by concurrent writer", "hostname", hostname,
			"expected", bareMetalInstanceID, "actual", verifiedID)
		return nil, nil
	}

	return device, nil
}

// ensureBMHExists resolves BMC credentials and address for the device, ensures
// the operator-managed BMC credentials Secret exists, and creates the
// BareMetalHost CR. Idempotent — safe to re-run for the same device (both the
// Secret and the BMH creation are idempotent).
func (c *BCMClient) ensureBMHExists(ctx context.Context, device *bcmclient.Device, bareMetalInstanceID string) error {
	log := ctrllog.FromContext(ctx)

	// The BareMetalHost (and its Secret) names are derived from the BCM
	// hostname; re-validate it before using it to build K8s object names.
	if !hostnamePattern.MatchString(device.Hostname) || len(device.Hostname) > 63 {
		return fmt.Errorf("invalid BCM hostname %q — cannot derive BareMetalHost/Secret names", device.Hostname)
	}
	bmhName := device.Hostname

	username, password, err := c.resolveBMCCredentials(ctx, device)
	if err != nil {
		return err
	}

	bmcAddress, err := c.resolveBMCAddress(ctx, device, username, password)
	if err != nil {
		return err
	}

	// Secret name is derived from the BMH so the two stay coupled.
	secretName := bmhName + bmcSecretSuffix
	if err := c.bmhManager.EnsureBMCSecret(ctx, secretName, username, password); err != nil {
		return fmt.Errorf("failed to create BMC credentials Secret for host %q: %w", device.Hostname, err)
	}

	params := bmhCreateParams(bmhName, bmcAddress, secretName, device.MAC, bareMetalInstanceID)
	if err := c.bmhManager.CreateBMH(ctx, params); err != nil {
		return fmt.Errorf("failed to create BareMetalHost for %s: %w", device.Hostname, err)
	}

	log.V(1).Info("BareMetalHost ensured", "hostname", device.Hostname, "bmcAddress", bmcAddress)
	return nil
}

// bmhCreateParams builds the BareMetalHost creation parameters. Namespace is
// intentionally omitted from ConsumerRef: the consumer is identified by an
// opaque ID, not a resolvable namespace/name — matching the Metal3 adapter's
// documented convention, which is sufficient for BMO's claim mechanism.
func bmhCreateParams(bmhName, bmcAddress, secretName, bootMAC, bareMetalInstanceID string) baremetalhost.CreateParams {
	return baremetalhost.CreateParams{
		Name:              bmhName,
		BMCAddress:        bmcAddress,
		CredentialsSecret: secretName,
		BootMACAddress:    bootMAC,
		ConsumerRef: &corev1.ObjectReference{
			APIVersion: "osac.openshift.io/v1alpha1",
			Kind:       "BareMetalInstance",
			Name:       bareMetalInstanceID,
		},
		Labels: map[string]string{
			Metal3ManagedByLabel: shared.OsacDefaultManagedByValue,
		},
	}
}

// bmcSecretSuffix is appended to the BMH name to name operator-created BMC Secrets.
const bmcSecretSuffix = "-bmc-secret"

// resolveBMCCredentials resolves the per-host BMC username/password from BCM,
// following BCM's own inheritance order (device → category → partition
// bmcSettings). The cloud provider is expected to populate bmcSettings for
// hosts offered to BMaaS; an unset value at every level is an actionable error.
func (c *BCMClient) resolveBMCCredentials(ctx context.Context, device *bcmclient.Device) (username, password string, err error) {
	username, password, source, err := c.credsFromBCM(ctx, device)
	if err != nil {
		return "", "", err
	}
	if username == "" {
		return "", "", fmt.Errorf("no BMC credentials configured in BCM for host %q"+
			" — populate bmcSettings on the device, its category, or its partition", device.Hostname)
	}
	ctrllog.FromContext(ctx).V(1).Info("Resolved BMC credentials from BCM", "hostname", device.Hostname, "source", source)
	return username, password, nil
}

// credsFromBCM reads BMC credentials from the device's own bmcSettings, then
// (if unset) its category, then its partition — mirroring BCM's inheritance
// order. The BCM JSON API returns only each object's own bmcSettings, so
// inherited values must be fetched and resolved here. Returns empty strings
// (nil error) when no level has credentials; source is for logging only.
func (c *BCMClient) credsFromBCM(ctx context.Context, device *bcmclient.Device) (username, password, source string, err error) {
	if u, p, ok := device.BMCSettings.Credentials(); ok {
		return u, p, "device", nil
	}
	if u, p, src, err := c.categoryCreds(ctx, device); err != nil || u != "" {
		return u, p, src, err
	}
	return c.partitionCreds(ctx, device)
}

// categoryCreds returns BMC credentials inherited from the device's category,
// or empty strings when the device has no category, the category isn't found,
// or it has no credentials configured.
func (c *BCMClient) categoryCreds(ctx context.Context, device *bcmclient.Device) (username, password, source string, err error) {
	if device.Category == "" {
		return "", "", "", nil
	}
	categories, err := c.client.GetCategories(ctx)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to resolve category BMC credentials for host %q: %w", device.Hostname, err)
	}
	for i := range categories {
		if categories[i].UUID != device.Category {
			continue
		}
		if u, p, ok := categories[i].BMCSettings.Credentials(); ok {
			return u, p, "category:" + categories[i].Name, nil
		}
		return "", "", "", nil
	}
	ctrllog.FromContext(ctx).V(1).Info("Device references a category not found in BCM", "hostname", device.Hostname, "categoryUUID", device.Category)
	return "", "", "", nil
}

// partitionCreds returns BMC credentials inherited from the device's partition,
// or empty strings when the device has no partition, the partition isn't found,
// or it has no credentials configured.
func (c *BCMClient) partitionCreds(ctx context.Context, device *bcmclient.Device) (username, password, source string, err error) {
	if device.Partition == "" {
		return "", "", "", nil
	}
	partitions, err := c.client.GetPartitions(ctx)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to resolve partition BMC credentials for host %q: %w", device.Hostname, err)
	}
	for i := range partitions {
		if partitions[i].UUID != device.Partition {
			continue
		}
		if u, p, ok := partitions[i].BMCSettings.Credentials(); ok {
			return u, p, "partition:" + partitions[i].Name, nil
		}
		return "", "", "", nil
	}
	ctrllog.FromContext(ctx).V(1).Info("Device references a partition not found in BCM", "hostname", device.Hostname, "partitionUUID", device.Partition)
	return "", "", "", nil
}

// resolveBMCAddress returns the BMH BMC address: Priority 1 uses a pre-configured
// osac_bmc_address; otherwise it discovers the address from BCM interface data.
func (c *BCMClient) resolveBMCAddress(ctx context.Context, device *bcmclient.Device, username, password string) (string, error) {
	addr, ok := device.ExtraValues[bcmclient.ExtraValueBMCAddress].(string)
	if !ok || addr == "" {
		return c.discoverBMCAddress(ctx, device, username, password)
	}
	if err := bmcdiscovery.ValidateBMCAddress(addr); err != nil {
		return "", fmt.Errorf("pre-configured BMC address for host %q is invalid: %w", device.Hostname, err)
	}
	ctrllog.FromContext(ctx).V(1).Info("Using pre-configured BMC address (Priority 1)", "hostname", device.Hostname, "address", addr)
	return addr, nil
}

// discoverBMCAddress (Priority 2) extracts the BMC IP/protocol from the device's
// interfaces, discovers the full address (Redfish system path or static IPMI URL),
// and caches the result back to BCM for future assignments.
func (c *BCMClient) discoverBMCAddress(ctx context.Context, device *bcmclient.Device, username, password string) (string, error) {
	log := ctrllog.FromContext(ctx)

	bmcInterfaces := make([]bmcdiscovery.DeviceInterface, 0, len(device.Interfaces))
	for _, iface := range device.Interfaces {
		bmcInterfaces = append(bmcInterfaces, bmcdiscovery.DeviceInterface{ChildType: iface.ChildType, Name: iface.Name, IP: iface.IP})
	}
	bmcInfo, err := bmcdiscovery.ExtractBMCInfo(bmcInterfaces, bmcInterfaceChildType)
	if err != nil {
		return "", fmt.Errorf("BMC info not available for host %q"+
			" — configure osac_bmc_address in BCM extra_values or register the node with BMC interface data: %w", device.Hostname, err)
	}

	log.V(1).Info("Resolving BMC address via discovery (Priority 2)", "hostname", device.Hostname, "bmcIP", bmcInfo.IP, "protocol", bmcInfo.Protocol)
	addr, err := bmcdiscovery.Resolve(ctx, bmcInfo, device.MAC, username, password, c.bmcDiscoverer)
	if err != nil {
		return "", fmt.Errorf("BMC address discovery failed for host %q: %w", device.Hostname, err)
	}

	if err := c.cacheBMCAddress(ctx, device, addr); err != nil {
		log.Info("Failed to cache discovered BMC address in BCM, will rediscover on next reconcile", "hostname", device.Hostname, "error", err)
	}
	return addr, nil
}

func (c *BCMClient) cacheBMCAddress(ctx context.Context, device *bcmclient.Device, addr string) error {
	raw, err := bcmclient.SetExtraValue(device.Raw, bcmclient.ExtraValueBMCAddress, addr)
	if err != nil {
		return err
	}
	_, err = c.client.UpdateDevice(ctx, raw)
	return err
}

func (c *BCMClient) buildHost(device *bcmclient.Device) *Host {
	resourceClass, _ := device.ExtraValues[bcmclient.ExtraValueResourceClass].(string)
	return &Host{
		InventoryHostID: fmt.Sprintf("%s/%s", c.bmhManager.Namespace(), device.Hostname),
		Name:            device.Hostname,
		HostType:        resourceClass,
		HostClass:       c.hostClass,
		ManagedBy:       shared.OsacDefaultManagedByValue,
	}
}

// UnassignHost clears the OSAC assignment from a BCM device and deletes the
// on-demand BMH CR. Ordering is BCM update before BMH deletion for crash
// recovery safety — both steps are individually idempotent.
func (c *BCMClient) UnassignHost(ctx context.Context, inventoryHostID string, labels []string) error {
	_, hostname, err := ParseHostID(inventoryHostID)
	if err != nil {
		return fmt.Errorf("UnassignHost: %w", err)
	}

	log := ctrllog.FromContext(ctx)
	log.Info("Unassigning BCM host", "hostname", hostname)

	if err := c.clearBCMAssignment(ctx, hostname, labels); err != nil {
		return fmt.Errorf("UnassignHost: %w", err)
	}

	if err := c.bmhManager.DeleteBMH(ctx, hostname); err != nil {
		return fmt.Errorf("UnassignHost: %w", err)
	}

	// Delete the operator-created BMC Secret if we own it. Label-guarded inside
	// DeleteBMCSecret, so admin-provided Secrets are never touched.
	if err := c.bmhManager.DeleteBMCSecret(ctx, hostname+bmcSecretSuffix); err != nil {
		return fmt.Errorf("UnassignHost: %w", err)
	}

	return nil
}

func (c *BCMClient) clearBCMAssignment(ctx context.Context, hostname string, labels []string) error {
	log := ctrllog.FromContext(ctx)

	device, err := c.client.GetDevice(ctx, hostname)
	if err != nil {
		return err
	}

	if device == nil {
		log.Info("BCM device not found, skipping extra_values cleanup", "hostname", hostname)
		return nil
	}

	if _, assigned := device.ExtraValues[bcmclient.ExtraValueInstanceID]; !assigned {
		log.Info("BCM host already unassigned, skipping extra_values cleanup", "hostname", hostname)
		return nil
	}

	raw := device.Raw
	raw, err = bcmclient.RemoveExtraValue(raw, bcmclient.ExtraValueInstanceID)
	if err != nil {
		return err
	}
	for _, label := range labels {
		raw, err = bcmclient.RemoveExtraValue(raw, label)
		if err != nil {
			return err
		}
	}

	_, err = c.client.UpdateDevice(ctx, raw)
	return err
}

// GetHostNICs reads all NIC MAC addresses from the BareMetalHost CR hardware inspection data.
// inventoryHostID must be in namespace/hostname format where hostname is the BMH name.
// Reading from the BMH CR avoids a costly GetDevices round-trip to the BCM API.
// Returns nil, nil when the BMH has no hardware inspection data (caller treats this as "NIC data unavailable").
func (c *BCMClient) GetHostNICs(ctx context.Context, inventoryHostID string) ([]HostNIC, error) {
	_, bmhName, err := ParseHostID(inventoryHostID)
	if err != nil {
		return nil, err
	}

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
