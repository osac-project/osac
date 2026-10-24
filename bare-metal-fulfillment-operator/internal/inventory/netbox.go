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
	"strconv"
	"strings"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/baremetalhost"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/bmcdiscovery"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/netboxclient"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/secretapi"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/shared"
)

const (
	netBoxHostPrefix   = "netbox-"
	netBoxStatusActive = "active"
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

var errNetBoxClientNotImplemented = errors.New("NetBox inventory client is not implemented")

// NetBoxAPI defines the operations needed by the inventory adapter. It is
// satisfied by netboxclient.Client and keeps generated SDK calls behind a
// consumer-side interface.
type NetBoxAPI interface {
	ForEachDevice(ctx context.Context, query netboxclient.DeviceQuery, visit func(netboxclient.Device) (bool, error)) error
	ForEachDeviceCustomField(ctx context.Context, visit func(netboxclient.CustomField) (bool, error)) error
	GetDevice(ctx context.Context, id string) (netboxclient.DeviceSnapshot, error)
	PatchDevice(ctx context.Context, id string, patch netboxclient.DevicePatch, etag string) (netboxclient.DeviceSnapshot, error)
}

// SecretAPIClient defines the Secret API operations used by the NetBox inventory.
type SecretAPIClient interface {
	List(ctx context.Context, filter string, limit int32) (secretapi.ListResult, error)
	Get(ctx context.Context, id string) (secretapi.Secret, error)
	Close() error
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
	api                NetBoxAPI
	bmhManager         baremetalhost.BMHLifecycleManager
	hostClass          string
	secretAPI          SecretAPIClient
	credentialResolver DeviceCredentialResolver
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
func NewNetBoxClient(api NetBoxAPI, bmhManager baremetalhost.BMHLifecycleManager, hostClass string, secretAPI SecretAPIClient) *NetBoxClient {
	return &NetBoxClient{
		api:                api,
		bmhManager:         bmhManager,
		hostClass:          hostClass,
		secretAPI:          secretAPI,
		credentialResolver: NewNetBoxCredentialResolver(secretAPI),
	}
}

// Close releases the Secret API connection used by the NetBox client.
func (c *NetBoxClient) Close() error {
	if c == nil || c.secretAPI == nil {
		return nil
	}
	return c.secretAPI.Close()
}

func (c *NetBoxClient) FindFreeHost(ctx context.Context, selector map[string]string) (*Host, error) {
	if c == nil || c.api == nil {
		return nil, errNetBoxClientNotImplemented
	}
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
		Ready:           true,
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
	if c == nil || c.api == nil {
		return nil, errNetBoxClientNotImplemented
	}
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
		return netboxclient.DeviceSnapshot{}, fmt.Errorf("failed to read NetBox device: %w", err)
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
		// OSAC-5952 will use the resolved credentials when preparing the Metal3 host.
		if _, err := c.credentialResolver.Resolve(ctx, snapshot.Device); err != nil {
			return nil, fmt.Errorf("failed to resolve NetBox BMC credentials: %w", err)
		}
		return c.claimNetBoxDevice(ctx, namespace, deviceID, bareMetalInstanceID, snapshot.ETag)
	case netBoxAssignmentOwnedByThisInstance:
		return c.netBoxAssignedHost(namespace, snapshot.Device, bareMetalInstanceID), nil
	case netBoxAssignmentOwnedByAnotherInstance, netBoxAssignmentUnavailable:
		return nil, nil
	default:
		return nil, fmt.Errorf("unknown NetBox assignment state: %d", state)
	}
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

func (*NetBoxClient) UnassignHost(_ context.Context, _ string, _ []string) error {
	return errNetBoxClientNotImplemented
}

func (*NetBoxClient) GetHostNICs(_ context.Context, _ string) ([]HostNIC, error) {
	return nil, errNetBoxClientNotImplemented
}

// GetHostLogicalPortMACs is not implemented until NetBox inventory operations are added.
func (*NetBoxClient) GetHostLogicalPortMACs(_ context.Context, _ string) (map[string]string, error) {
	return nil, errNetBoxClientNotImplemented
}

const (
	netBoxDeviceSecretLabelPrefix = "osac.openshift.io/netbox-device-"
	netBoxBMCAddressField         = "osac_bmc_address"
	netBoxBootMACField            = "osac_boot_mac"
	secretListLimit               = 2
)

// ErrCredentialResolutionFailed identifies invalid or missing device credentials.
var ErrCredentialResolutionFailed = errors.New("NetBox BMC credential resolution failed")

// NetBoxCredentials contains validated BMC connection data for one NetBox device.
type NetBoxCredentials struct {
	BMCAddress     string
	BootMACAddress string
	Username       []byte
	Password       []byte
}

// DeviceCredentialResolver resolves credentials for a NetBox device.
type DeviceCredentialResolver interface {
	Resolve(ctx context.Context, device netboxclient.Device) (*NetBoxCredentials, error)
}

type netBoxSecretReader interface {
	List(ctx context.Context, filter string, limit int32) (secretapi.ListResult, error)
	Get(ctx context.Context, id string) (secretapi.Secret, error)
}

// NetBoxCredentialResolver finds the system Secret associated with a NetBox device.
type NetBoxCredentialResolver struct {
	secrets netBoxSecretReader
}

var _ DeviceCredentialResolver = (*NetBoxCredentialResolver)(nil)

// NewNetBoxCredentialResolver creates the NetBox-specific Secret resolver.
func NewNetBoxCredentialResolver(secrets netBoxSecretReader) *NetBoxCredentialResolver {
	return &NetBoxCredentialResolver{secrets: secrets}
}

// Resolve validates the NetBox connection fields and resolves the unique matching system Secret.
func (r *NetBoxCredentialResolver) Resolve(ctx context.Context, device netboxclient.Device) (*NetBoxCredentials, error) {
	if r == nil || r.secrets == nil {
		return nil, errors.New("OSAC Secret API client is not configured")
	}
	labelKey, err := netBoxDeviceSecretLabel(device.ID)
	if err != nil {
		return nil, credentialResolutionFailure(err)
	}
	connection, err := netBoxConnection(device.CustomFields)
	if err != nil {
		return nil, credentialResolutionFailure(err)
	}
	secret, err := r.getDeviceSecret(ctx, labelKey)
	if err != nil {
		return nil, err
	}
	username, password, err := bmcCredentials(secret)
	if err != nil {
		return nil, credentialResolutionFailure(err)
	}
	connection.Username = username
	connection.Password = password
	return connection, nil
}

func netBoxDeviceSecretLabel(deviceID string) (string, error) {
	value, err := strconv.ParseUint(deviceID, 10, 64)
	if err != nil || value == 0 || strconv.FormatUint(value, 10) != deviceID {
		return "", errors.New("invalid NetBox device ID for Secret association")
	}
	return netBoxDeviceSecretLabelPrefix + deviceID, nil
}

func netBoxConnection(customFields map[string]interface{}) (*NetBoxCredentials, error) {
	address, ok := netBoxStringField(customFields, netBoxBMCAddressField)
	if !ok || bmcdiscovery.ValidateBMCAddress(address) != nil {
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
	return &NetBoxCredentials{
		BMCAddress:     address,
		BootMACAddress: strings.ToLower(parsedMAC.String()),
	}, nil
}

func netBoxStringField(fields map[string]interface{}, key string) (string, bool) {
	value, ok := fields[key].(string)
	if !ok || value == "" || strings.TrimSpace(value) != value {
		return "", false
	}
	return value, true
}

func (r *NetBoxCredentialResolver) getDeviceSecret(ctx context.Context, labelKey string) (secretapi.Secret, error) {
	filter := secretapi.FilterByTenantAndLabel("system", labelKey)
	result, err := r.secrets.List(ctx, filter, secretListLimit)
	if err != nil {
		return secretapi.Secret{}, fmt.Errorf("failed to list OSAC Secrets for NetBox device: %w", err)
	}
	secretID, err := uniqueDeviceSecretID(result, labelKey)
	if err != nil {
		return secretapi.Secret{}, credentialResolutionFailure(err)
	}
	secret, err := r.secrets.Get(ctx, secretID)
	if err != nil {
		return secretapi.Secret{}, fmt.Errorf("failed to get OSAC Secret for NetBox device: %w", err)
	}
	if secret.ID != secretID || !isDeviceSecret(secret, labelKey) {
		return secretapi.Secret{}, credentialResolutionFailure(errors.New("OSAC Secret is not a system-scoped Secret associated with the NetBox device"))
	}
	return secret, nil
}

func credentialResolutionFailure(err error) error {
	return fmt.Errorf("%w: %s", ErrCredentialResolutionFailed, err)
}

func uniqueDeviceSecretID(result secretapi.ListResult, labelKey string) (string, error) {
	if result.Total == 0 && len(result.Items) == 0 {
		return "", errors.New("no system-scoped OSAC Secret is associated with the NetBox device")
	}
	if result.Total > 1 {
		return "", errors.New("multiple system-scoped OSAC Secrets are associated with the NetBox device")
	}
	if result.Total != 1 || len(result.Items) != 1 {
		return "", errors.New("inconsistent OSAC Secret list response for NetBox device")
	}
	secret := result.Items[0]
	if secret.ID == "" || !isDeviceSecret(secret, labelKey) {
		return "", errors.New("OSAC Secret list returned an invalid device association")
	}
	return secret.ID, nil
}

func isDeviceSecret(secret secretapi.Secret, labelKey string) bool {
	if secret.Tenant != "system" {
		return false
	}
	_, found := secret.Labels[labelKey]
	return found
}

func bmcCredentials(secret secretapi.Secret) ([]byte, []byte, error) {
	username := secret.Data["username"]
	password := secret.Data["password"]
	if len(username) == 0 || len(password) == 0 {
		return nil, nil, errors.New("OSAC Secret must contain non-empty username and password values")
	}
	return append([]byte(nil), username...), append([]byte(nil), password...), nil
}
