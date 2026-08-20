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
	"math/rand"
	"net/http"
	"strings"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	"github.com/gophercloud/gophercloud/v2/openstack/baremetal/v1/nodes"
	"github.com/gophercloud/gophercloud/v2/pagination"
	"github.com/gophercloud/utils/v2/openstack/clientconfig"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/shared"
)

var (
	_ Client        = (*OpenStackClient)(nil)
	_ NewClientFunc = NewClientFunc(NewOpenStackClient)
)

const (
	OSACPrefix = "osac_"

	// Label keys within osac_labels map
	BareMetalInstanceIDLabel = "bareMetalInstanceId"
	ManagedByLabel           = "managedBy"
)

func init() {
	newClientFuncs["openstack"] = NewOpenStackClient
}

type OpenStackClient struct {
	client           *gophercloud.ServiceClient
	newServiceClient func(ctx context.Context) (*gophercloud.ServiceClient, error)
	HostClass        string
}

// NewOpenStackClient creates a new OpenStack inventory client
func NewOpenStackClient(ctx context.Context, cfg *Config) (Client, error) {
	factory := newServiceClientFactory(cfg)

	sc, err := factory(ctx)
	if err != nil {
		return nil, err
	}

	return &OpenStackClient{
		client:           sc,
		newServiceClient: factory,
		HostClass:        cfg.HostClass,
	}, nil
}

func newServiceClientFactory(cfg *Config) func(ctx context.Context) (*gophercloud.ServiceClient, error) {
	opts := cfg.Options

	return func(ctx context.Context) (*gophercloud.ServiceClient, error) {
		var cloud clientconfig.Cloud
		if openstackOpts, ok := opts["openstack"]; ok {
			openstackOptsJSON, err := json.Marshal(openstackOpts)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(openstackOptsJSON, &cloud); err != nil {
				return nil, err
			}
		}

		if cloud.AuthInfo == nil {
			cloud.AuthInfo = &clientconfig.AuthInfo{}
		}
		cloud.AuthInfo.AllowReauth = true

		clientOpts := clientconfig.ClientOpts{
			Cloud:        cloud.Cloud,
			AuthType:     cloud.AuthType,
			AuthInfo:     cloud.AuthInfo,
			RegionName:   cloud.RegionName,
			EndpointType: cloud.EndpointType,
		}

		providerClient, err := clientconfig.AuthenticatedClient(ctx, &clientOpts)
		if err != nil {
			return nil, err
		}

		ironicClient, err := openstack.NewBareMetalV1(providerClient, gophercloud.EndpointOpts{})
		if err != nil {
			return nil, err
		}

		ironicClient.Microversion = "latest"

		return ironicClient, nil
	}
}

func isAuthError(err error) bool {
	if err == nil {
		return false
	}
	if gophercloud.ResponseCodeIs(err, http.StatusUnauthorized) {
		return true
	}
	var errReauth *gophercloud.ErrUnableToReauthenticate
	if errors.As(err, &errReauth) {
		return true
	}
	var errAfterReauth *gophercloud.ErrErrorAfterReauthentication
	return errors.As(err, &errAfterReauth)
}

func (c *OpenStackClient) reconnect(ctx context.Context) error {
	log := ctrllog.FromContext(ctx)
	log.Info("recreating ironic service client after authentication failure")
	sc, err := c.newServiceClient(ctx)
	if err != nil {
		log.Error(err, "failed to recreate ironic service client")
		return fmt.Errorf("failed to recreate baremetal client: %w", err)
	}
	c.client = sc
	log.Info("ironic service client reconnected successfully", "endpoint", sc.Endpoint)
	return nil
}

func (c *OpenStackClient) FindFreeHost(ctx context.Context, matchExpressions map[string]string) (*Host, error) {
	host, err := c.findFreeHost(ctx, matchExpressions)
	if err != nil && isAuthError(err) {
		log := ctrllog.FromContext(ctx)
		log.Info("auth error on FindFreeHost, attempting reconnect", "error", err)
		if reconnErr := c.reconnect(ctx); reconnErr != nil {
			return nil, fmt.Errorf("find free host: reconnect failed: %w", reconnErr)
		}
		host, err = c.findFreeHost(ctx, matchExpressions)
		if err != nil {
			return nil, fmt.Errorf("find free host after reconnect: %w", err)
		}
	}
	return host, err
}

func (c *OpenStackClient) findFreeHost(ctx context.Context, matchExpressions map[string]string) (*Host, error) {
	listOpts := nodes.ListOpts{
		Fields: []string{
			"uuid",
			"name",
			"resource_class",
			"provision_state",
			"extra",
		},
	}

	if hostType, ok := matchExpressions["hostType"]; ok {
		listOpts.ResourceClass = hostType
	}
	provisionState, ok := matchExpressions["provisionState"]
	if !ok || provisionState == "" {
		provisionState = shared.OsacDefaultProvisionStateValue
	}
	listOpts.ProvisionState = nodes.ProvisionState(provisionState)

	var foundHost *Host
	err := nodes.List(c.client, listOpts).EachPage(ctx, func(ctx context.Context, page pagination.Page) (bool, error) {
		nodeList, err := nodes.ExtractNodes(page)
		if err != nil {
			return false, err
		}

		// shuffle to reduce chances of getting an unmarked but locked host
		nodes := make([]*nodes.Node, len(nodeList))
		for i := range nodeList {
			nodes[i] = &nodeList[i]
		}
		rand.Shuffle(len(nodes), func(i int, j int) {
			nodes[i], nodes[j] = nodes[j], nodes[i]
		})

		for _, node := range nodes {
			// Check if host is already assigned by looking for bareMetalInstanceId labels
			bareMetalInstanceID, _ := getNestedLabel(node, BareMetalInstanceIDLabel)
			if bareMetalInstanceID != "" {
				continue
			}
			bareMetalPoolID, _ := getNestedLabel(node, shared.OsacBareMetalPoolIDLabel)

			// Get managedBy label, defaulting to standard value if not set
			managedBy, ok := getNestedLabel(node, ManagedByLabel)
			if !ok || managedBy == "" {
				managedBy = shared.OsacDefaultManagedByValue
			}
			matchManagedBy, ok := matchExpressions["managedBy"]
			if !ok || matchManagedBy == "" {
				matchManagedBy = shared.OsacDefaultManagedByValue
			}
			if managedBy != matchManagedBy {
				continue
			}

			foundHost = &Host{
				BareMetalPoolID:     bareMetalPoolID,
				BareMetalInstanceID: bareMetalInstanceID,
				InventoryHostID:     node.UUID,
				Name:                node.Name,
				HostType:            node.ResourceClass,
				HostClass:           c.HostClass,
				ProvisionState:      node.ProvisionState,
				ManagedBy:           managedBy,
			}
			return false, nil
		}

		return true, nil
	})
	if err != nil {
		return nil, err
	}

	return foundHost, nil
}

func (c *OpenStackClient) AssignHost(ctx context.Context, inventoryHostID string, bareMetalInstanceID string, labels map[string]string) (*Host, error) {
	host, err := c.assignHost(ctx, inventoryHostID, bareMetalInstanceID, labels)
	if err != nil && isAuthError(err) {
		log := ctrllog.FromContext(ctx)
		log.Info("auth error on AssignHost, attempting reconnect", "inventoryHostID", inventoryHostID, "error", err)
		if reconnErr := c.reconnect(ctx); reconnErr != nil {
			return nil, fmt.Errorf("assign host %s: reconnect failed: %w", inventoryHostID, reconnErr)
		}
		host, err = c.assignHost(ctx, inventoryHostID, bareMetalInstanceID, labels)
		if err != nil {
			return nil, fmt.Errorf("assign host %s after reconnect: %w", inventoryHostID, err)
		}
	}
	return host, err
}

func (c *OpenStackClient) assignHost(ctx context.Context, inventoryHostID string, bareMetalInstanceID string, labels map[string]string) (*Host, error) {
	if inventoryHostID == "" {
		return nil, fmt.Errorf("invalid input: inventoryHostID is empty")
	}
	if bareMetalInstanceID == "" {
		return nil, fmt.Errorf("invalid input: bareMetalInstanceID is empty")
	}

	node, err := nodes.Get(ctx, c.client, inventoryHostID).Extract()
	if err != nil {
		return nil, err
	}

	currentBareMetalInstanceID, ok := getNestedLabel(node, BareMetalInstanceIDLabel)
	if ok && currentBareMetalInstanceID != "" && currentBareMetalInstanceID != bareMetalInstanceID {
		return nil, nil
	}

	// Ensure /extra/osac_labels exists before adding any labels
	if _, ok := node.Extra["osac_labels"]; !ok {
		initOpts := make(nodes.UpdateOpts, 0, 1)
		initOpts = append(initOpts, nodes.UpdateOperation{
			Op:    nodes.AddOp,
			Path:  "/extra/osac_labels",
			Value: map[string]interface{}{},
		})
		_, err = nodes.Update(ctx, c.client, inventoryHostID, initOpts).Extract()
		if err != nil {
			return nil, err
		}
	}

	// Add hostId and user labels to osac_labels
	updateOpts := make(nodes.UpdateOpts, 0, 1+len(labels))
	updateOpts = append(updateOpts,
		nodes.UpdateOperation{
			Op:    nodes.AddOp,
			Path:  "/extra/osac_labels/" + escapeJSONPointerToken(BareMetalInstanceIDLabel),
			Value: bareMetalInstanceID,
		},
	)

	// Add additional profile labels
	for labelKey, labelValue := range labels {
		updateOpts = append(updateOpts, nodes.UpdateOperation{
			Op:    nodes.AddOp,
			Path:  "/extra/osac_labels/" + escapeJSONPointerToken(labelKey),
			Value: labelValue,
		})
	}

	node, err = nodes.Update(ctx, c.client, inventoryHostID, updateOpts).Extract()
	if err != nil {
		return nil, err
	}

	managedBy, ok := getNestedLabel(node, ManagedByLabel)
	if !ok {
		managedBy = shared.OsacDefaultManagedByValue
	}

	bareMetalPoolID, ok := getNestedLabel(node, shared.OsacBareMetalPoolIDLabel)
	if !ok {
		bareMetalPoolID = ""
	}

	return &Host{
		BareMetalPoolID:     bareMetalPoolID,
		BareMetalInstanceID: bareMetalInstanceID,
		InventoryHostID:     node.UUID,
		Name:                node.Name,
		HostType:            node.ResourceClass,
		HostClass:           c.HostClass,
		ProvisionState:      node.ProvisionState,
		ManagedBy:           managedBy,
	}, nil
}

func (c *OpenStackClient) UnassignHost(ctx context.Context, inventoryHostID string, labels []string) error {
	err := c.unassignHost(ctx, inventoryHostID, labels)
	if err != nil && isAuthError(err) {
		log := ctrllog.FromContext(ctx)
		log.Info("auth error on UnassignHost, attempting reconnect", "inventoryHostID", inventoryHostID, "error", err)
		if reconnErr := c.reconnect(ctx); reconnErr != nil {
			return fmt.Errorf("unassign host %s: reconnect failed: %w", inventoryHostID, reconnErr)
		}
		err = c.unassignHost(ctx, inventoryHostID, labels)
		if err != nil {
			return fmt.Errorf("unassign host %s after reconnect: %w", inventoryHostID, err)
		}
	}
	return err
}

func (c *OpenStackClient) unassignHost(ctx context.Context, inventoryHostID string, labels []string) error {
	// Get current node state to check what labels exist
	node, err := nodes.Get(ctx, c.client, inventoryHostID).Extract()
	if err != nil {
		return err
	}

	existing, _ := node.Extra["osac_labels"].(map[string]any)
	if existing == nil {
		existing = make(map[string]any)
	}

	// Build list of labels to remove: hostId and user-provided labels
	// Note: managedBy is kept as a persistent label
	labelsToRemove := make([]string, 0, 1+len(labels))
	seen := map[string]struct{}{BareMetalInstanceIDLabel: {}}
	labelsToRemove = append(labelsToRemove, BareMetalInstanceIDLabel)
	for _, label := range labels {
		if _, ok := seen[label]; ok {
			continue
		}
		seen[label] = struct{}{}
		labelsToRemove = append(labelsToRemove, label)
	}

	updateOpts := make(nodes.UpdateOpts, 0, len(labelsToRemove))
	for _, label := range labelsToRemove {
		// Only remove if the label exists
		if _, ok := existing[label]; !ok {
			continue
		}
		updateOpts = append(updateOpts, nodes.UpdateOperation{
			Op:   nodes.RemoveOp,
			Path: "/extra/osac_labels/" + escapeJSONPointerToken(label),
		})
	}

	// If no labels to remove, nothing to do
	if len(updateOpts) == 0 {
		return nil
	}

	_, err = nodes.Update(ctx, c.client, inventoryHostID, updateOpts).Extract()
	return err
}

func escapeJSONPointerToken(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	return strings.ReplaceAll(s, "/", "~1")
}

// getNestedLabel retrieves a label value from node.Extra["osac_labels"][labelKey]
// Returns the value as a string and a boolean indicating if it was found
func getNestedLabel(node *nodes.Node, labelKey string) (string, bool) {
	if labelsMap, ok := node.Extra["osac_labels"].(map[string]interface{}); ok {
		if value, ok := labelsMap[labelKey].(string); ok {
			return value, true
		}
	}
	return "", false
}

// GetHostNICs returns nil for OpenStack hosts. NIC metadata discovery via
// Ironic ports is implemented in OSAC-4202; returning nil allows the controller
// to proceed to Ready without blocking on the unimplemented call.
func (c *OpenStackClient) GetHostNICs(_ context.Context, _ string) ([]HostNIC, error) {
	return nil, nil
}
