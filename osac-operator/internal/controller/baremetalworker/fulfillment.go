/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

// Package baremetalworker holds the CaaS bare-metal worker provisioning controller and the
// narrow client seams it uses to talk to its external dependencies: the fulfillment-service
// private API (FulfillmentClient) and the assisted-service discovery ignition endpoint
// (IgnitionFetcher).
package baremetalworker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"google.golang.org/grpc"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// callTimeout bounds every fulfillment-service gRPC call (and the ignition HTTP fetch). The
// controller-runtime requeue provides the retry loop; this deadline only prevents a hung call
// from blocking a worker.
const callTimeout = 30 * time.Second

// unavailableThreshold is the number of consecutive gRPC failures after which the client
// reports the fulfillment-service as unavailable.
const unavailableThreshold = 3

// ErrFulfillmentServiceUnavailable is returned (wrapped, matchable with errors.Is) once the
// fulfillment-service gRPC calls have failed unavailableThreshold times in a row. The
// reconciler maps it to a FulfillmentServiceUnavailable condition.
var ErrFulfillmentServiceUnavailable = errors.New("fulfillment service unavailable")

// FulfillmentClient is the narrow seam the bare-metal worker reconciler uses to talk to the
// fulfillment-service private gRPC API. It is intentionally small so it can be faked in tests
// without a running fulfillment-service.
type FulfillmentClient interface {
	// CreateBareMetalInstance creates a BareMetalInstance and returns the created object.
	CreateBareMetalInstance(ctx context.Context, obj *privatev1.BareMetalInstance) (*privatev1.BareMetalInstance, error)
	// DeleteBareMetalInstance deletes the BareMetalInstance with the given id.
	DeleteBareMetalInstance(ctx context.Context, id string) error
	// GetBareMetalInstance returns the BareMetalInstance with the given id.
	GetBareMetalInstance(ctx context.Context, id string) (*privatev1.BareMetalInstance, error)
	// ListBareMetalInstances returns the BareMetalInstances matching the given CEL filter
	// (empty filter returns all instances visible to the caller).
	ListBareMetalInstances(ctx context.Context, filter string) ([]*privatev1.BareMetalInstance, error)
	// GetClusterVersion returns the ClusterVersion with the given id.
	GetClusterVersion(ctx context.Context, id string) (*privatev1.ClusterVersion, error)
	// GetCluster returns the Cluster with the given id.
	GetCluster(ctx context.Context, id string) (*privatev1.Cluster, error)
	// GetDiskImage returns the DiskImage with the given id.
	GetDiskImage(ctx context.Context, id string) (*privatev1.DiskImage, error)
	// CreateBareMetalInstanceCatalogItem creates a BareMetalInstanceCatalogItem and returns the created object.
	CreateBareMetalInstanceCatalogItem(ctx context.Context, obj *privatev1.BareMetalInstanceCatalogItem) (*privatev1.BareMetalInstanceCatalogItem, error)
	// ListBareMetalInstanceCatalogItems returns the BareMetalInstanceCatalogItems matching the given CEL filter.
	ListBareMetalInstanceCatalogItems(ctx context.Context, filter string) ([]*privatev1.BareMetalInstanceCatalogItem, error)
	// GetBareMetalInstanceType returns the BareMetalInstanceType with the given id (or name).
	GetBareMetalInstanceType(ctx context.Context, id string) (*privatev1.BareMetalInstanceType, error)
}

// fulfillmentClient is the production FulfillmentClient. It wraps the generated gRPC clients,
// applies a per-call deadline, and tracks consecutive gRPC failures for the unavailable signal.
type fulfillmentClient struct {
	bmis          privatev1.BareMetalInstancesClient
	versions      privatev1.ClusterVersionsClient
	clusters      privatev1.ClustersClient
	diskImages    privatev1.DiskImagesClient
	catalogItems  privatev1.BareMetalInstanceCatalogItemsClient
	instanceTypes privatev1.BareMetalInstanceTypesClient

	mu                  sync.Mutex
	consecutiveFailures int
}

// NewFulfillmentClient builds a FulfillmentClient from already-constructed generated clients.
func NewFulfillmentClient(
	bmis privatev1.BareMetalInstancesClient,
	versions privatev1.ClusterVersionsClient,
	clusters privatev1.ClustersClient,
	diskImages privatev1.DiskImagesClient,
	catalogItems privatev1.BareMetalInstanceCatalogItemsClient,
	instanceTypes privatev1.BareMetalInstanceTypesClient,
) FulfillmentClient {
	return &fulfillmentClient{
		bmis: bmis, versions: versions, clusters: clusters, diskImages: diskImages,
		catalogItems: catalogItems, instanceTypes: instanceTypes,
	}
}

// NewFulfillmentClientFromConn wires the production FulfillmentClient from the shared gRPC
// connection dialed in main(). This is what the reconciler is constructed with.
func NewFulfillmentClientFromConn(conn *grpc.ClientConn) FulfillmentClient {
	return NewFulfillmentClient(
		privatev1.NewBareMetalInstancesClient(conn),
		privatev1.NewClusterVersionsClient(conn),
		privatev1.NewClustersClient(conn),
		privatev1.NewDiskImagesClient(conn),
		privatev1.NewBareMetalInstanceCatalogItemsClient(conn),
		privatev1.NewBareMetalInstanceTypesClient(conn),
	)
}

// call runs a fulfillment-service gRPC operation under the per-call deadline and updates the
// consecutive-failure counter. After unavailableThreshold consecutive failures it wraps the
// underlying error with ErrFulfillmentServiceUnavailable; a success resets the counter.
func (c *fulfillmentClient) call(ctx context.Context, op func(ctx context.Context) error) error {
	opCtx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	err := op(opCtx)

	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.consecutiveFailures++
		if c.consecutiveFailures >= unavailableThreshold {
			return fmt.Errorf("%w: %w", ErrFulfillmentServiceUnavailable, err)
		}
		return err
	}
	c.consecutiveFailures = 0
	return nil
}

func (c *fulfillmentClient) CreateBareMetalInstance(
	ctx context.Context,
	obj *privatev1.BareMetalInstance,
) (*privatev1.BareMetalInstance, error) {
	var out *privatev1.BareMetalInstance
	err := c.call(ctx, func(ctx context.Context) error {
		resp, err := c.bmis.Create(ctx, privatev1.BareMetalInstancesCreateRequest_builder{Object: obj}.Build())
		if err != nil {
			return err
		}
		out = resp.GetObject()
		return nil
	})
	return out, err
}

func (c *fulfillmentClient) DeleteBareMetalInstance(ctx context.Context, id string) error {
	return c.call(ctx, func(ctx context.Context) error {
		_, err := c.bmis.Delete(ctx, privatev1.BareMetalInstancesDeleteRequest_builder{Id: id}.Build())
		return err
	})
}

func (c *fulfillmentClient) GetBareMetalInstance(ctx context.Context, id string) (*privatev1.BareMetalInstance, error) {
	var out *privatev1.BareMetalInstance
	err := c.call(ctx, func(ctx context.Context) error {
		resp, err := c.bmis.Get(ctx, privatev1.BareMetalInstancesGetRequest_builder{Id: id}.Build())
		if err != nil {
			return err
		}
		out = resp.GetObject()
		return nil
	})
	return out, err
}

func (c *fulfillmentClient) ListBareMetalInstances(
	ctx context.Context,
	filter string,
) ([]*privatev1.BareMetalInstance, error) {
	var out []*privatev1.BareMetalInstance
	err := c.call(ctx, func(ctx context.Context) error {
		req := privatev1.BareMetalInstancesListRequest_builder{}
		if filter != "" {
			req.Filter = &filter
		}
		resp, err := c.bmis.List(ctx, req.Build())
		if err != nil {
			return err
		}
		out = resp.GetItems()
		return nil
	})
	return out, err
}

func (c *fulfillmentClient) GetClusterVersion(ctx context.Context, id string) (*privatev1.ClusterVersion, error) {
	var out *privatev1.ClusterVersion
	err := c.call(ctx, func(ctx context.Context) error {
		resp, err := c.versions.Get(ctx, privatev1.ClusterVersionsGetRequest_builder{Id: id}.Build())
		if err != nil {
			return err
		}
		out = resp.GetObject()
		return nil
	})
	return out, err
}

func (c *fulfillmentClient) GetCluster(ctx context.Context, id string) (*privatev1.Cluster, error) {
	var out *privatev1.Cluster
	err := c.call(ctx, func(ctx context.Context) error {
		resp, err := c.clusters.Get(ctx, privatev1.ClustersGetRequest_builder{Id: id}.Build())
		if err != nil {
			return err
		}
		out = resp.GetObject()
		return nil
	})
	return out, err
}

func (c *fulfillmentClient) GetDiskImage(ctx context.Context, id string) (*privatev1.DiskImage, error) {
	var out *privatev1.DiskImage
	err := c.call(ctx, func(ctx context.Context) error {
		resp, err := c.diskImages.Get(ctx, privatev1.DiskImagesGetRequest_builder{Id: id}.Build())
		if err != nil {
			return err
		}
		out = resp.GetObject()
		return nil
	})
	return out, err
}

func (c *fulfillmentClient) CreateBareMetalInstanceCatalogItem(
	ctx context.Context,
	obj *privatev1.BareMetalInstanceCatalogItem,
) (*privatev1.BareMetalInstanceCatalogItem, error) {
	var out *privatev1.BareMetalInstanceCatalogItem
	err := c.call(ctx, func(ctx context.Context) error {
		resp, err := c.catalogItems.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{Object: obj}.Build())
		if err != nil {
			return err
		}
		out = resp.GetObject()
		return nil
	})
	return out, err
}

func (c *fulfillmentClient) ListBareMetalInstanceCatalogItems(
	ctx context.Context,
	filter string,
) ([]*privatev1.BareMetalInstanceCatalogItem, error) {
	var out []*privatev1.BareMetalInstanceCatalogItem
	err := c.call(ctx, func(ctx context.Context) error {
		req := privatev1.BareMetalInstanceCatalogItemsListRequest_builder{}
		if filter != "" {
			req.Filter = &filter
		}
		resp, err := c.catalogItems.List(ctx, req.Build())
		if err != nil {
			return err
		}
		out = resp.GetItems()
		return nil
	})
	return out, err
}

func (c *fulfillmentClient) GetBareMetalInstanceType(
	ctx context.Context, id string,
) (*privatev1.BareMetalInstanceType, error) {
	var out *privatev1.BareMetalInstanceType
	err := c.call(ctx, func(ctx context.Context) error {
		resp, err := c.instanceTypes.Get(ctx, privatev1.BareMetalInstanceTypesGetRequest_builder{Id: id}.Build())
		if err != nil {
			return err
		}
		out = resp.GetObject()
		return nil
	})
	return out, err
}
