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
	"strings"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	"github.com/gophercloud/gophercloud/v2/openstack/baremetal/v1/nodes"
	"github.com/gophercloud/gophercloud/v2/openstack/baremetal/v1/ports"
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
	client    *gophercloud.ServiceClient
	hostClass string
}

// NewOpenStackClient creates a new OpenStack inventory client
func NewOpenStackClient(ctx context.Context, cfg *Config) (Client, error) {
	opts := cfg.Options

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

	return &OpenStackClient{
		client:    ironicClient,
		hostClass: cfg.HostClass,
	}, nil
}

func (c *OpenStackClient) FindFreeHost(ctx context.Context, matchExpressions map[string]string) (*Host, error) {
	if err := validateMatchExpressions(matchExpressions); err != nil {
		return nil, err
	}

	log := ctrllog.FromContext(ctx)
	log.Info("searching for free host", "selectorLabels", matchExpressions)

	// managedBy is enforced client-side as a default-aware ownership guard
	// (mirroring the Metal3 backend), and provisionState is enforced server-side
	// via ListOpts below. Both are excluded from the generic osac_labels match so
	// that they are not looked up as node labels (which would reject every node,
	// since provisionState is a node field, not an osac_label).
	matchManagedBy := matchExpressions[ManagedByLabel]
	if matchManagedBy == "" {
		matchManagedBy = shared.OsacDefaultManagedByValue
	}
	labelMatchExpressions := make(map[string]string, len(matchExpressions))
	for key, value := range matchExpressions {
		if key == ManagedByLabel || key == "provisionState" {
			continue
		}
		labelMatchExpressions[key] = value
	}

	// Note: server-side ResourceClass filtering is intentionally not used.
	// Label-based selection supports arbitrary keys (gpu=a100, datacenter=west)
	// that cannot be mapped to a single resource_class value. All filtering
	// is done client-side via osac_labels. Ironic nodes must have osac_labels
	// populated for this to work correctly.
	listOpts := nodes.ListOpts{
		Fields: []string{
			"uuid",
			"name",
			"resource_class",
			"provision_state",
			"extra",
		},
		ProvisionState: nodes.ProvisionState(shared.OsacDefaultProvisionStateValue),
	}

	var foundHost *Host
	err := nodes.List(c.client, listOpts).EachPage(ctx, func(ctx context.Context, page pagination.Page) (bool, error) {
		log := ctrllog.FromContext(ctx)
		nodeList, err := nodes.ExtractNodes(page)
		if err != nil {
			return false, err
		}

		nodeRefs := make([]*nodes.Node, len(nodeList))
		for i := range nodeList {
			nodeRefs[i] = &nodeList[i]
		}
		rand.Shuffle(len(nodeRefs), func(i, j int) {
			nodeRefs[i], nodeRefs[j] = nodeRefs[j], nodeRefs[i]
		})

		for _, node := range nodeRefs {
			// Skip already assigned hosts
			bareMetalInstanceID, _ := getNestedLabel(node, BareMetalInstanceIDLabel)
			if bareMetalInstanceID != "" {
				continue
			}

			// Apply host selector label filtering against the node's osac_labels.
			if !nodeMatchesLabels(node, labelMatchExpressions) {
				continue
			}

			// Ownership guard: skip hosts managed by another system. A missing or
			// empty managedBy label is treated as the default owner.
			managedBy, ok := getNestedLabel(node, ManagedByLabel)
			if !ok || managedBy == "" {
				managedBy = shared.OsacDefaultManagedByValue
			}
			if managedBy != matchManagedBy {
				continue
			}

			// Skip nodes without registered Ironic ports
			portList, portErr := c.listNodePorts(ctx, node.UUID)
			if portErr != nil {
				log.V(1).Info("Skipping node: port lookup failed", "node", node.UUID, "error", portErr)
				continue
			}
			if len(portList) == 0 {
				log.Error(nil, "Skipping node: no registered ports", "node", node.UUID)
				continue
			}

			bareMetalPoolID, _ := getNestedLabel(node, shared.OsacBareMetalPoolIDLabel)
			foundHost = &Host{
				BareMetalPoolID:     bareMetalPoolID,
				BareMetalInstanceID: bareMetalInstanceID,
				InventoryHostID:     node.UUID,
				Name:                node.Name,
				HostType:            node.ResourceClass,
				HostClass:           c.hostClass,
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
	if inventoryHostID == "" {
		return nil, fmt.Errorf("invalid input: inventoryHostID is empty")
	}
	if bareMetalInstanceID == "" {
		return nil, fmt.Errorf("invalid input: bareMetalInstanceID is empty")
	}

	node, err := nodes.Get(ctx, c.client, inventoryHostID).Extract()
	if err != nil {
		return nil, fmt.Errorf("getting node %s: %w", inventoryHostID, err)
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
			return nil, fmt.Errorf("initializing osac_labels on node %s: %w", inventoryHostID, err)
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
		return nil, fmt.Errorf("assigning labels to node %s: %w", inventoryHostID, err)
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
		HostClass:           c.hostClass,
		ProvisionState:      node.ProvisionState,
		ManagedBy:           managedBy,
		Ready:               true, // Ironic nodes are immediately usable after label assignment
	}, nil
}

func (c *OpenStackClient) UnassignHost(ctx context.Context, inventoryHostID string, labels []string) error {
	// Get current node state to check what labels exist
	node, err := nodes.Get(ctx, c.client, inventoryHostID).Extract()
	if err != nil {
		return fmt.Errorf("getting node %s: %w", inventoryHostID, err)
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
	if err != nil {
		return fmt.Errorf("removing labels from node %s: %w", inventoryHostID, err)
	}
	return nil
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

func (c *OpenStackClient) GetHostNICs(ctx context.Context, inventoryHostID string) ([]HostNIC, error) {
	portList, err := c.listNodePorts(ctx, inventoryHostID)
	if err != nil {
		return nil, fmt.Errorf("getting node ports for node %s: %w", inventoryHostID, err)
	}
	if len(portList) == 0 {
		return nil, fmt.Errorf("node %s has no NIC inventory despite being allocated", inventoryHostID)
	}
	nics := make([]HostNIC, 0, len(portList))
	for _, p := range portList {
		nics = append(nics, HostNIC{MAC: strings.ToLower(p.Address)})
	}
	return nics, nil
}

// listNodePorts fetches and extracts the Ironic ports for the given node UUID.
// Returns an empty slice (not an error) when the node has no registered ports.
func (c *OpenStackClient) listNodePorts(ctx context.Context, nodeUUID string) ([]ports.Port, error) {
	portPages, err := ports.ListDetail(c.client, ports.ListOpts{NodeUUID: nodeUUID}).AllPages(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing ports for node %s: %w", nodeUUID, err)
	}
	portList, err := ports.ExtractPorts(portPages)
	if err != nil {
		return nil, fmt.Errorf("extracting ports for node %s: %w", nodeUUID, err)
	}
	return portList, nil
}

// validateMatchExpressions validates matchExpressions keys and values for the OpenStack backend.
// Rejects empty keys, keys containing spaces, and empty values. All other keys are valid and will be
// used as host selector labels for filtering nodes.
func validateMatchExpressions(matchExpressions map[string]string) error {
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

func nodeMatchesLabels(node *nodes.Node, matchExpressions map[string]string) bool {
	if len(matchExpressions) == 0 {
		return true
	}

	labelsMap, ok := node.Extra["osac_labels"].(map[string]interface{})
	if !ok {
		return false
	}

	for key, expectedValue := range matchExpressions {
		nodeValue, exists := labelsMap[key]
		if !exists {
			return false
		}
		nodeValueStr, ok := nodeValue.(string)
		if !ok {
			return false
		}
		if nodeValueStr != expectedValue {
			return false
		}
	}
	return true
}
