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
	"errors"
	"fmt"
	"maps"
	"net"
	"strings"

	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/baremetalhost"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/bmcdiscovery"
	bmcCredentials "github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/credentials"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/netboxclient"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/shared"
)

const (
	netBoxHostPrefix   = "netbox-"
	netBoxStatusActive = "active"
	netBoxStatusFailed = "failed"
	netBoxStatusStaged = "staged"
)

type netBoxAssignmentState uint8

const (
	netBoxAssignmentUnspecified netBoxAssignmentState = iota
	netBoxAssignmentReadyToClaim
	netBoxAssignmentOwnedByThisInstance
	netBoxAssignmentOwnedByAnotherInstance
	netBoxAssignmentUnavailable
)

type netBoxReleaseState uint8

const (
	netBoxReleaseUnspecified netBoxReleaseState = iota
	netBoxReleaseComplete
	netBoxReleaseClaimed
	netBoxReleaseOwnedByAnotherInstance
)

var (
	errNetBoxClientNotImplemented = errors.New("NetBox inventory client is not implemented")
	errNetBoxBMHDeletionPending   = errors.New("NetBox BareMetalHost deletion is pending")
)

// NetBoxAPI defines the operations needed by the inventory adapter. It is
// satisfied by netboxclient.Client and keeps generated SDK calls behind a
// consumer-side interface.
type NetBoxAPI interface {
	ForEachDevice(ctx context.Context, query netboxclient.DeviceQuery, visit func(netboxclient.Device) (bool, error)) error
	ForEachDeviceCustomField(ctx context.Context, visit func(netboxclient.CustomField) (bool, error)) error
	ListDeviceInterfaces(ctx context.Context, id string) ([]netboxclient.DeviceInterface, error)
	GetDevice(ctx context.Context, id string) (netboxclient.DeviceSnapshot, error)
	PatchDevice(ctx context.Context, id string, patch netboxclient.DevicePatch, etag string) (netboxclient.DeviceSnapshot, error)
}

// NetBoxClientConfig contains the mounted-file and endpoint settings for NetBox.
type NetBoxClientConfig struct {
	URL               string `json:"url"`
	TokenFile         string `json:"tokenFile"`
	CAFile            string `json:"caFile"`
	AllowInsecureHTTP bool   `json:"allowInsecureHTTP"`
}

// ParseNetBoxOptions extracts the NetBox-specific settings from inventory
// configuration. The token value itself must remain in the mounted file.
func ParseNetBoxOptions(options map[string]any) (*NetBoxClientConfig, error) {
	netBoxOptions, ok := options["netbox"]
	if !ok {
		return nil, fmt.Errorf("netbox options not found in config")
	}

	raw, err := json.Marshal(netBoxOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to parse netbox options")
	}

	var config NetBoxClientConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, fmt.Errorf("failed to parse netbox options")
	}
	if config.URL == "" {
		return nil, fmt.Errorf("netbox url is required in config")
	}
	if config.TokenFile == "" {
		return nil, fmt.Errorf("netbox tokenFile is required in config")
	}
	return &config, nil
}

// NetBoxClient implements inventory.Client against NetBox's device API.
type NetBoxClient struct {
	api               NetBoxAPI
	bmhManager        baremetalhost.BMHLifecycleManager
	bmcSecretStore    bmcCredentials.BMCSecretStore
	hostClass         string
	credentialsReader bmcCredentials.Reader
}

var (
	_ Client = (*NetBoxClient)(nil)
)

// NewNetBoxAPI constructs the secure NetBox API dependency from mounted configuration.
func NewNetBoxAPI(config *Config) (NetBoxAPI, error) {
	if config == nil {
		return nil, fmt.Errorf("netbox inventory config is required")
	}
	netBoxConfig, err := ParseNetBoxOptions(config.Options)
	if err != nil {
		return nil, err
	}

	api, err := netboxclient.New(netboxclient.Config{
		Endpoint:          netBoxConfig.URL,
		TokenFile:         netBoxConfig.TokenFile,
		CAFile:            netBoxConfig.CAFile,
		AllowInsecureHTTP: netBoxConfig.AllowInsecureHTTP,
	})
	if err != nil {
		return nil, err
	}
	return api, nil
}

// NewNetBoxClient creates a NetBox inventory client with injected dependencies.
func NewNetBoxClient(
	api NetBoxAPI,
	bmhManager baremetalhost.BMHLifecycleManager,
	bmcSecretStore bmcCredentials.BMCSecretStore,
	hostClass string,
	credentialsReader bmcCredentials.Reader,
) *NetBoxClient {
	return &NetBoxClient{
		api:               api,
		bmhManager:        bmhManager,
		bmcSecretStore:    bmcSecretStore,
		hostClass:         hostClass,
		credentialsReader: credentialsReader,
	}
}

// Close releases resources owned by the credentials reader.
func (c *NetBoxClient) Close() error {
	if c == nil || c.credentialsReader == nil {
		return nil
	}
	return c.credentialsReader.Close()
}

func (c *NetBoxClient) FindFreeHost(ctx context.Context, selector map[string]string) (*Host, error) {
	return c.findFreeHost(ctx, buildFreeHostQuery(selector))
}

func (c *NetBoxClient) findFreeHost(ctx context.Context, query netboxclient.DeviceQuery) (*Host, error) {
	namespace, err := c.metal3Namespace()
	if err != nil {
		return nil, err
	}

	var foundHost *Host
	err = c.api.ForEachDevice(ctx, query, func(device netboxclient.Device) (bool, error) {
		foundHost = c.netBoxHostFromDevice(namespace, device)
		return false, nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to search NetBox for a free host: %w", err)
	}
	return foundHost, nil
}

func (c *NetBoxClient) metal3Namespace() (string, error) {
	if c.bmhManager != nil {
		if namespace := c.bmhManager.Namespace(); namespace != "" {
			return namespace, nil
		}
	}
	return "", errors.New("NetBox inventory requires a Metal3 namespace")
}

func (c *NetBoxClient) netBoxHostFromDevice(namespace string, device netboxclient.Device) *Host {
	name := netBoxHostPrefix + device.ID
	return &Host{
		InventoryHostID: fmt.Sprintf("%s/%s", namespace, name),
		Name:            name,
		HostClass:       c.hostClass,
		ProvisionState:  shared.OsacDefaultProvisionStateValue,
		ManagedBy:       shared.OsacDefaultManagedByValue,
		Available:       true,
	}
}

func buildFreeHostQuery(selector map[string]string) netboxclient.DeviceQuery {
	capabilityFilters := make(netboxclient.DeviceQuery, len(selector))
	for key, value := range selector {
		capabilityFilters["cf_"+key] = value
	}
	commonSelectors := netboxclient.DeviceQuery{
		"status":                     netBoxStatusStaged,
		"cf_osac_managed":            "true",
		"cf_osac_instance_id__empty": "true",
	}
	query := make(netboxclient.DeviceQuery, len(capabilityFilters)+len(commonSelectors))
	maps.Copy(query, capabilityFilters)
	maps.Copy(query, commonSelectors)
	return query
}

func (c *NetBoxClient) AssignHost(ctx context.Context, inventoryHostID string, bareMetalInstanceID string, _ map[string]string) (*Host, error) {
	if bareMetalInstanceID == "" {
		return nil, errors.New("invalid input: bareMetalInstanceID is empty")
	}
	namespace, deviceID, err := c.parseNetBoxHostID(inventoryHostID)
	if err != nil {
		return nil, err
	}
	snapshot, err := c.readNetBoxDevice(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	return c.assignNetBoxDevice(ctx, namespace, deviceID, bareMetalInstanceID, snapshot)
}

func (c *NetBoxClient) parseNetBoxHostID(inventoryHostID string) (string, string, error) {
	namespace, name, err := ParseHostID(inventoryHostID)
	if err != nil {
		return "", "", errors.New("invalid NetBox inventory host ID: expected namespace/name format")
	}
	if !strings.HasPrefix(name, netBoxHostPrefix) {
		return "", "", errors.New("invalid NetBox inventory host ID")
	}
	deviceID := strings.TrimPrefix(name, netBoxHostPrefix)
	if deviceID == "" {
		return "", "", errors.New("invalid NetBox inventory host ID")
	}
	if c.bmhManager == nil || c.bmhManager.Namespace() == "" {
		return "", "", errors.New("NetBox inventory requires a Metal3 namespace")
	}
	if namespace != c.bmhManager.Namespace() {
		return "", "", errors.New("NetBox inventory host ID is outside the Metal3 namespace")
	}
	return namespace, deviceID, nil
}

func (c *NetBoxClient) readNetBoxDevice(ctx context.Context, deviceID string) (netboxclient.DeviceSnapshot, error) {
	snapshot, err := c.api.GetDevice(ctx, deviceID)
	if err != nil {
		return netboxclient.DeviceSnapshot{}, fmt.Errorf("failed to read NetBox device %s: %w", deviceID, err)
	}
	if snapshot.Device.ID != deviceID {
		return netboxclient.DeviceSnapshot{}, errors.New("NetBox returned a different device than requested")
	}
	return snapshot, nil
}

func (c *NetBoxClient) assignNetBoxDevice(ctx context.Context, namespace, deviceID, bareMetalInstanceID string, snapshot netboxclient.DeviceSnapshot) (*Host, error) {
	state, err := classifyNetBoxAssignment(snapshot.Device, bareMetalInstanceID)
	if err != nil {
		return nil, err
	}
	switch state {
	case netBoxAssignmentReadyToClaim:
		connection, credentials, err := c.resolveNetBoxHostAccess(ctx, snapshot.Device)
		if err != nil {
			if errors.Is(err, bmcCredentials.ErrResolutionFailed) {
				ctrllog.FromContext(ctx).Error(err, "NetBox device credentials are invalid; quarantining device", "deviceID", deviceID)
				return c.markNetBoxDeviceFailed(ctx, deviceID, snapshot.ETag, credentialFailureMessage(err))
			}
			return nil, fmt.Errorf("failed to resolve NetBox BMC credentials: %w", err)
		}
		host, err := c.claimNetBoxDevice(ctx, namespace, deviceID, bareMetalInstanceID, snapshot.ETag)
		if err != nil || host == nil {
			return host, err
		}
		return c.prepareMetal3Host(ctx, host, connection, credentials, bareMetalInstanceID)
	case netBoxAssignmentOwnedByThisInstance:
		connection, credentials, err := c.resolveNetBoxHostAccess(ctx, snapshot.Device)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve NetBox BMC credentials: %w", err)
		}
		host := c.netBoxAssignedHost(namespace, snapshot.Device, bareMetalInstanceID)
		return c.prepareMetal3Host(ctx, host, connection, credentials, bareMetalInstanceID)
	case netBoxAssignmentOwnedByAnotherInstance, netBoxAssignmentUnavailable:
		return nil, nil
	default:
		return nil, fmt.Errorf("unknown NetBox assignment state: %d", state)
	}
}

func (c *NetBoxClient) resolveNetBoxHostAccess(ctx context.Context, device netboxclient.Device) (*netBoxConnectionData, *bmcCredentials.BMC, error) {
	connection, err := netBoxConnection(device.CustomFields)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", bmcCredentials.ErrResolutionFailed, err)
	}
	if c.credentialsReader == nil {
		return nil, nil, errors.New("OSAC Secret API client is not configured")
	}
	credentials, err := c.credentialsReader.GetCredentials(ctx, device.ID)
	if err != nil {
		return nil, nil, err
	}
	return connection, credentials, nil
}

func (c *NetBoxClient) prepareMetal3Host(ctx context.Context, host *Host, connection *netBoxConnectionData, credentials *bmcCredentials.BMC, bareMetalInstanceID string) (*Host, error) {
	secretName := host.Name + bmcSecretSuffix
	if err := c.bmcSecretStore.Ensure(ctx, secretName, credentials, bareMetalInstanceID); err != nil {
		return nil, fmt.Errorf("failed to ensure BMC credentials Secret for NetBox host: %w", err)
	}

	params := bmhCreateParams(host.Name, connection.BMCAddress, secretName, connection.BootMACAddress, bareMetalInstanceID)
	if err := c.bmhManager.CreateBMH(ctx, params); err != nil {
		return nil, fmt.Errorf("failed to create BareMetalHost for NetBox host: %w", err)
	}

	available, err := c.bmhManager.IsBMHAvailable(ctx, host.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to check BareMetalHost availability for NetBox host: %w", err)
	}
	host.Available = available
	return host, nil
}

func credentialFailureMessage(err error) string {
	switch {
	case errors.Is(err, bmcCredentials.ErrCredentialsNotFound):
		return "OSAC: no BMC Secret matches this device. Add the system Secret label, then reset status to staged."
	case errors.Is(err, bmcCredentials.ErrAmbiguousCredentials):
		return "OSAC: multiple BMC Secrets match this device. Fix the labels, then reset status to staged."
	default:
		return "OSAC: BMC credentials are missing or invalid. Check the system Secret and device BMC settings; reset status to staged after repair."
	}
}

func (c *NetBoxClient) markNetBoxDeviceFailed(ctx context.Context, deviceID, etag, message string) (*Host, error) {
	failed := netBoxStatusFailed
	updated, err := c.api.PatchDevice(ctx, deviceID, netboxclient.DevicePatch{
		Status: &failed, ChangelogMessage: message,
	}, etag)
	if err != nil {
		latest, readErr := c.readNetBoxDevice(ctx, deviceID)
		if readErr != nil {
			return nil, fmt.Errorf("failed to confirm NetBox credential quarantine: %w", readErr)
		}
		host, outcomeErr := netBoxQuarantineOutcome(deviceID, latest.Device, true)
		if outcomeErr != nil {
			return nil, fmt.Errorf("%w (quarantine PATCH failed: %w)", outcomeErr, err)
		}
		return host, nil
	}
	return netBoxQuarantineOutcome(deviceID, updated.Device, false)
}

func netBoxQuarantineOutcome(deviceID string, device netboxclient.Device, afterPatchError bool) (*Host, error) {
	if device.ID != deviceID {
		return nil, errors.New("NetBox returned a different device after credential quarantine")
	}
	owner, err := netBoxInstanceOwner(device)
	if err != nil {
		return nil, err
	}
	if device.Status == netBoxStatusFailed && owner == "" {
		return nil, nil
	}
	if afterPatchError {
		if device.Status == netBoxStatusActive && owner != "" {
			return nil, nil
		}
		if device.Status == netBoxStatusStaged && owner == "" {
			return nil, errors.New("NetBox credential quarantine was not confirmed; retry with a fresh ETag")
		}
	}
	return nil, errors.New("NetBox response did not confirm credential quarantine")
}

func (c *NetBoxClient) claimNetBoxDevice(ctx context.Context, namespace, deviceID, bareMetalInstanceID, etag string) (*Host, error) {
	active := netBoxStatusActive
	claimed, err := c.api.PatchDevice(ctx, deviceID, netboxclient.DevicePatch{
		Status:       &active,
		CustomFields: map[string]interface{}{"osac_instance_id": bareMetalInstanceID},
	}, etag)
	if err != nil {
		var apiErr *netboxclient.APIError
		if !errors.As(err, &apiErr) || apiErr.Kind != netboxclient.ErrorKindConflict {
			return nil, fmt.Errorf("failed to claim NetBox device: %w", err)
		}
		return c.reconcileNetBoxClaimConflict(ctx, namespace, deviceID, bareMetalInstanceID)
	}
	if claimed.Device.ID != deviceID {
		return nil, errors.New("NetBox returned a different device after the claim")
	}
	state, err := classifyNetBoxAssignment(claimed.Device, bareMetalInstanceID)
	if err != nil {
		return nil, err
	}
	if state != netBoxAssignmentOwnedByThisInstance {
		return nil, errors.New("NetBox response did not confirm the host assignment")
	}
	return c.netBoxAssignedHost(namespace, claimed.Device, bareMetalInstanceID), nil
}

func (c *NetBoxClient) reconcileNetBoxClaimConflict(ctx context.Context, namespace, deviceID, bareMetalInstanceID string) (*Host, error) {
	latest, err := c.readNetBoxDevice(ctx, deviceID)
	if err != nil {
		return nil, fmt.Errorf("failed to re-read NetBox device after a claim conflict: %w", err)
	}
	state, err := classifyNetBoxAssignment(latest.Device, bareMetalInstanceID)
	if err != nil {
		return nil, err
	}

	switch state {
	case netBoxAssignmentOwnedByThisInstance:
		return c.netBoxAssignedHost(namespace, latest.Device, bareMetalInstanceID), nil
	case netBoxAssignmentOwnedByAnotherInstance, netBoxAssignmentUnavailable:
		return nil, nil
	case netBoxAssignmentReadyToClaim:
		// Preserve the saved ID when the device is still free; the next reconcile
		// will use a fresh ETag rather than selecting a different device.
		return nil, errors.New("NetBox device changed during assignment; retry on the next reconciliation")
	default:
		return nil, fmt.Errorf("unknown NetBox assignment state: %d", state)
	}
}

func classifyNetBoxAssignment(device netboxclient.Device, bareMetalInstanceID string) (netBoxAssignmentState, error) {
	owner, err := netBoxInstanceOwner(device)
	if err != nil {
		return netBoxAssignmentUnspecified, err
	}
	if device.Status == "" {
		return netBoxAssignmentUnspecified, errors.New("NetBox device has no status")
	}
	if owner == "" {
		if device.Status == netBoxStatusActive {
			return netBoxAssignmentUnspecified, errors.New("NetBox device is active without an assigned instance")
		}
		if device.Status == netBoxStatusStaged {
			return netBoxAssignmentReadyToClaim, nil
		}
		return netBoxAssignmentUnavailable, nil
	}
	if device.Status != netBoxStatusActive {
		return netBoxAssignmentUnspecified, errors.New("NetBox device has an assigned instance but is not active")
	}
	if owner == bareMetalInstanceID {
		return netBoxAssignmentOwnedByThisInstance, nil
	}
	return netBoxAssignmentOwnedByAnotherInstance, nil
}

func (c *NetBoxClient) netBoxAssignedHost(namespace string, device netboxclient.Device, bareMetalInstanceID string) *Host {
	host := c.netBoxHostFromDevice(namespace, device)
	host.BareMetalInstanceID = bareMetalInstanceID
	return host
}

func netBoxInstanceOwner(device netboxclient.Device) (string, error) {
	value, ok := device.CustomFields["osac_instance_id"]
	if !ok || value == nil {
		return "", nil
	}
	owner, ok := value.(string)
	if !ok || strings.TrimSpace(owner) != owner {
		return "", errors.New("NetBox device has a malformed osac_instance_id custom field")
	}
	return owner, nil
}

// UnassignHost removes the NetBox claim after BMH and generated Secret deletion completes.
func (c *NetBoxClient) UnassignHost(ctx context.Context, inventoryHostID, bareMetalInstanceID string, _ []string) error {
	_, deviceID, err := c.parseNetBoxHostID(inventoryHostID)
	if err != nil {
		return err
	}
	return c.unassignNetBoxDevice(ctx, deviceID, bareMetalInstanceID)
}

func (c *NetBoxClient) unassignNetBoxDevice(ctx context.Context, deviceID, bareMetalInstanceID string) error {
	snapshot, err := c.readNetBoxDevice(ctx, deviceID)
	if err != nil {
		if netBoxDeviceNotFound(err) {
			ctrllog.FromContext(ctx).Info("NetBox device is gone; cleaning generated resources", "deviceID", deviceID)
			return c.deleteNetBoxResources(ctx, deviceID, bareMetalInstanceID)
		}
		return err
	}
	return c.unassignNetBoxSnapshot(ctx, deviceID, bareMetalInstanceID, snapshot)
}

func netBoxDeviceNotFound(err error) bool {
	var apiErr *netboxclient.APIError
	return errors.As(err, &apiErr) && apiErr.Kind == netboxclient.ErrorKindNotFound
}

func (c *NetBoxClient) unassignNetBoxSnapshot(ctx context.Context, deviceID, bareMetalInstanceID string, snapshot netboxclient.DeviceSnapshot) error {
	state, err := classifyNetBoxRelease(snapshot.Device, bareMetalInstanceID)
	if err != nil {
		return err
	}
	return c.cleanupAndReleaseNetBoxDevice(ctx, deviceID, snapshot.ETag, bareMetalInstanceID, state)
}

func classifyNetBoxRelease(device netboxclient.Device, bareMetalInstanceID string) (netBoxReleaseState, error) {
	owner, err := netBoxInstanceOwner(device)
	if err != nil {
		return netBoxReleaseUnspecified, err
	}
	return netBoxReleaseStateFor(device.Status, owner, bareMetalInstanceID)
}

func netBoxReleaseStateFor(status, owner, bareMetalInstanceID string) (netBoxReleaseState, error) {
	if status == netBoxStatusActive && owner != "" && owner == bareMetalInstanceID {
		return netBoxReleaseClaimed, nil
	}
	if status == netBoxStatusActive && owner != "" {
		return netBoxReleaseOwnedByAnotherInstance, nil
	}
	if status != netBoxStatusActive && owner == "" {
		return netBoxReleaseComplete, nil
	}
	return netBoxReleaseUnspecified, errors.New("NetBox device has inconsistent release state")
}

func (c *NetBoxClient) deleteNetBoxResources(ctx context.Context, deviceID, bareMetalInstanceID string) error {
	name := netBoxHostPrefix + deviceID
	if err := c.deleteNetBoxBMH(ctx, name, bareMetalInstanceID); err != nil {
		return err
	}
	return c.bmcSecretStore.Delete(ctx, name+bmcSecretSuffix, bareMetalInstanceID)
}

func (c *NetBoxClient) deleteNetBoxBMH(ctx context.Context, name, bareMetalInstanceID string) error {
	if err := c.bmhManager.DeleteBMH(ctx, name, bareMetalInstanceID); err != nil {
		return err
	}
	return c.confirmNetBoxBMHDeletion(ctx, name)
}

func (c *NetBoxClient) confirmNetBoxBMHDeletion(ctx context.Context, name string) error {
	exists, err := c.bmhManager.BMHExists(ctx, name)
	if err != nil || !exists {
		return err
	}
	return errNetBoxBMHDeletionPending
}

func (c *NetBoxClient) cleanupAndReleaseNetBoxDevice(ctx context.Context, deviceID, etag, bareMetalInstanceID string, state netBoxReleaseState) error {
	if state == netBoxReleaseOwnedByAnotherInstance {
		return nil
	}
	if err := c.deleteNetBoxResources(ctx, deviceID, bareMetalInstanceID); err != nil {
		return err
	}
	if state == netBoxReleaseComplete {
		return nil
	}
	return c.stageNetBoxDevice(ctx, deviceID, etag)
}

func (c *NetBoxClient) stageNetBoxDevice(ctx context.Context, deviceID, etag string) error {
	staged := netBoxStatusStaged
	patch := netboxclient.DevicePatch{
		Status: &staged, CustomFields: map[string]interface{}{"osac_instance_id": nil},
	}
	_, err := c.api.PatchDevice(ctx, deviceID, patch, etag)
	if netBoxDeviceNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to stage NetBox device %s: %w", deviceID, err)
	}
	return nil
}

// GetHostNICs reads inspected NICs from the allocated BareMetalHost.
func (c *NetBoxClient) GetHostNICs(ctx context.Context, inventoryHostID string) ([]HostNIC, error) {
	_, deviceID, err := c.parseNetBoxHostID(inventoryHostID)
	if err != nil {
		return nil, err
	}
	bmhName := netBoxHostPrefix + deviceID
	macs, err := c.bmhManager.GetHardwareNICs(ctx, bmhName)
	if err != nil {
		return nil, err
	}
	if len(macs) == 0 {
		return nil, fmt.Errorf("BareMetalHost %s has no NIC inventory despite being allocated", bmhName)
	}
	nics := make([]HostNIC, len(macs))
	for i, mac := range macs {
		nics[i] = HostNIC{MAC: strings.ToLower(mac)}
	}
	return nics, nil
}

// GetHostLogicalPortMACs returns the logical-port MAC mapping for a NetBox device.
func (c *NetBoxClient) GetHostLogicalPortMACs(ctx context.Context, inventoryHostID string) (map[string]string, error) {
	_, deviceID, err := c.parseNetBoxHostID(inventoryHostID)
	if err != nil {
		return nil, err
	}
	if c.api == nil {
		return nil, errNetBoxClientNotImplemented
	}

	nativeMACs, err := c.nativeLogicalPortMACs(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	customMACs, err := c.customFieldLogicalPortMACs(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	return mergeLogicalPortMACs(nativeMACs, customMACs), nil
}

func mergeLogicalPortMACs(nativeMACs, customMACs map[string]string) map[string]string {
	macs := make(map[string]string)
	for name, mac := range nativeMACs {
		macs[name] = mac
	}
	for name, mac := range customMACs {
		macs[name] = mac
	}
	return macs
}

func (c *NetBoxClient) nativeLogicalPortMACs(ctx context.Context, deviceID string) (map[string]string, error) {
	interfaces, err := c.api.ListDeviceInterfaces(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	macs := make(map[string]string, len(interfaces))
	for _, iface := range interfaces {
		if iface.PrimaryMAC != "" {
			macs[iface.Name] = iface.PrimaryMAC
		}
	}
	return macs, nil
}

func (c *NetBoxClient) customFieldLogicalPortMACs(ctx context.Context, deviceID string) (map[string]string, error) {
	snapshot, err := c.readNetBoxDevice(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	value := snapshot.Device.CustomFields["osac_interface_macs"]
	if value == nil {
		return nil, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	macs := map[string]string{}
	if err := json.Unmarshal(raw, &macs); err != nil {
		return nil, err
	}
	return macs, nil
}

const (
	netBoxBMCAddressField = "osac_bmc_address"
	netBoxBootMACField    = "osac_boot_mac"
)

// netBoxConnectionData contains validated BMC connection data from one NetBox device.
type netBoxConnectionData struct {
	BMCAddress     string
	BootMACAddress string
}

func netBoxConnection(customFields map[string]interface{}) (*netBoxConnectionData, error) {
	address, ok := netBoxStringField(customFields, netBoxBMCAddressField)
	if !ok ||
		strings.TrimSpace(address) != address ||
		bmcdiscovery.ValidateBMCAddress(address) != nil {
		return nil, errors.New("NetBox device has a missing or invalid BMC address")
	}
	bootMAC, ok := netBoxStringField(customFields, netBoxBootMACField)
	if !ok {
		return nil, errors.New("NetBox device has a missing or invalid boot MAC")
	}
	parsedMAC, err := net.ParseMAC(bootMAC)
	if err != nil || len(parsedMAC) != 6 {
		return nil, errors.New("NetBox device has a missing or invalid boot MAC")
	}
	return &netBoxConnectionData{
		BMCAddress:     address,
		BootMACAddress: strings.ToLower(parsedMAC.String()),
	}, nil
}

func netBoxStringField(fields map[string]interface{}, key string) (string, bool) {
	value, ok := fields[key].(string)
	return value, ok
}
