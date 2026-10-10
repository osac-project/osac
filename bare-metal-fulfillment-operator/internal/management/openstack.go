package management

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	"github.com/gophercloud/gophercloud/v2/openstack/baremetal/v1/nodes"
	"github.com/gophercloud/utils/v2/openstack/clientconfig"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
)

var (
	_ Client        = (*OpenStackClient)(nil)
	_ NewClientFunc = NewClientFunc(NewOpenStackClient)
)

func init() {
	newClientFuncs["openstack"] = NewOpenStackClient
}

type OpenStackClient struct {
	mu               sync.RWMutex
	client           *gophercloud.ServiceClient
	newServiceClient func(ctx context.Context) (*gophercloud.ServiceClient, error)
}

func NewOpenStackClient(ctx context.Context, cfg *Config) (Client, error) {
	var cloud clientconfig.Cloud
	if cfg != nil && cfg.Options != nil {
		if openstackOpts, ok := cfg.Options["openstack"]; ok {
			openstackOptsJSON, err := json.Marshal(openstackOpts)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal openstack options (check cloud configuration)")
			}
			if err := json.Unmarshal(openstackOptsJSON, &cloud); err != nil {
				return nil, fmt.Errorf("failed to unmarshal openstack options (check cloud configuration)")
			}
		}
	}

	factory := func(ctx context.Context) (*gophercloud.ServiceClient, error) {
		clientOpts := clientconfig.ClientOpts{
			Cloud:        cloud.Cloud,
			AuthType:     cloud.AuthType,
			AuthInfo:     cloud.AuthInfo,
			RegionName:   cloud.RegionName,
			EndpointType: cloud.EndpointType,
		}

		providerClient, err := clientconfig.AuthenticatedClient(ctx, &clientOpts)
		if err != nil {
			return nil, fmt.Errorf("failed to create authenticated client (check cloud credentials and endpoint configuration)")
		}

		ironicClient, err := openstack.NewBareMetalV1(providerClient, gophercloud.EndpointOpts{
			Region:       cloud.RegionName,
			Availability: gophercloud.Availability(cloud.EndpointType),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create baremetal client (check endpoint configuration and region)")
		}

		return ironicClient, nil
	}

	sc, err := factory(ctx)
	if err != nil {
		return nil, err
	}

	return &OpenStackClient{
		client:           sc,
		newServiceClient: factory,
	}, nil
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

// withAuthRetry executes the given operation, retrying once with a fresh client if auth fails
func (c *OpenStackClient) withAuthRetry(ctx context.Context, operation func(client *gophercloud.ServiceClient) error) error {
	log := ctrllog.FromContext(ctx)

	c.mu.RLock()
	client := c.client
	c.mu.RUnlock()

	err := operation(client)
	if err != nil && isAuthError(err) {
		log.Info("auth error, attempting reconnect")
		if reconnErr := c.reconnect(ctx); reconnErr != nil {
			return fmt.Errorf("reconnect failed: %w", reconnErr)
		}

		c.mu.RLock()
		client = c.client
		c.mu.RUnlock()

		err = operation(client)
	}
	return err
}

func (c *OpenStackClient) reconnect(ctx context.Context) error {
	log := ctrllog.FromContext(ctx)
	log.Info("recreating ironic service client after authentication failure")
	sc, err := c.newServiceClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to recreate baremetal client: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if sc == nil {
		return fmt.Errorf("failed to recreate baremetal client: nil service client")
	}
	if sc.Endpoint == "" {
		return fmt.Errorf("recreated baremetal client has no endpoint configured")
	}
	c.client = sc
	log.Info("ironic service client reconnected successfully")
	return nil
}

func (c *OpenStackClient) GetPowerState(ctx context.Context, hostID string) (*PowerStatus, error) {
	var node *nodes.Node
	err := c.withAuthRetry(ctx, func(client *gophercloud.ServiceClient) error {
		var extractErr error
		node, extractErr = nodes.Get(ctx, client, hostID).Extract()
		return extractErr
	})

	if err != nil {
		return nil, fmt.Errorf("get node %s: %w", hostID, err)
	}

	return nodePowerStatus(node, hostID)
}

func nodePowerStatus(node *nodes.Node, hostID string) (*PowerStatus, error) {
	state := PowerState(node.PowerState)
	switch state {
	case PowerOn, PowerOff:
	default:
		return nil, fmt.Errorf("node %s: unexpected power state %q", hostID, node.PowerState)
	}

	return &PowerStatus{
		State:           state,
		IsTransitioning: node.TargetPowerState != "",
	}, nil
}

func (c *OpenStackClient) SetPowerState(ctx context.Context, hostID string, target PowerState) error {
	switch target {
	case PowerOn, PowerOff:
	default:
		return fmt.Errorf("node %s: invalid target power state %q", hostID, target)
	}

	err := c.withAuthRetry(ctx, func(client *gophercloud.ServiceClient) error {
		res := nodes.ChangePowerState(ctx, client, hostID, nodes.PowerStateOpts{
			Target: nodes.TargetPowerState(target),
		})
		return res.ExtractErr()
	})

	if err != nil {
		if gophercloud.ResponseCodeIs(err, http.StatusConflict) {
			return fmt.Errorf("node %s: %w", hostID, ErrTransitioning)
		}
		return fmt.Errorf("failed to set power state on node %s: %w", hostID, err)
	}
	return nil
}

func (c *OpenStackClient) TriggerRestart(ctx context.Context, hostID string) error {
	err := c.withAuthRetry(ctx, func(client *gophercloud.ServiceClient) error {
		// Use Ironic's reboot command which automatically cycles power off then on
		res := nodes.ChangePowerState(ctx, client, hostID, nodes.PowerStateOpts{
			Target: "reboot",
		})
		return res.ExtractErr()
	})

	if err != nil {
		if gophercloud.ResponseCodeIs(err, http.StatusConflict) {
			return fmt.Errorf("node %s: %w", hostID, ErrTransitioning)
		}
		return fmt.Errorf("failed to trigger restart on node %s: %w", hostID, err)
	}
	return nil
}

func (c *OpenStackClient) IsRestartComplete(ctx context.Context, hostID string) (bool, error) {
	// Get current power status to check if node is still transitioning
	powerStatus, err := c.GetPowerState(ctx, hostID)
	if err != nil {
		return false, err
	}
	if powerStatus == nil {
		return false, fmt.Errorf("node %s: nil power status", hostID)
	}

	// Restart is complete when the node is no longer transitioning
	return !powerStatus.IsTransitioning, nil
}
