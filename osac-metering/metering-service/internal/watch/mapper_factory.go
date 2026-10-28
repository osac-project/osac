package watch

import (
	"context"
	"fmt"

	"google.golang.org/grpc"

	"github.com/osac-project/osac-metering/internal/events"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type ExternalIPPoolGetter interface {
	Get(ctx context.Context, in *privatev1.ExternalIPPoolsGetRequest, opts ...grpc.CallOption) (*privatev1.ExternalIPPoolsGetResponse, error)
}

type MapperFactory struct {
	externalIPPoolClient ExternalIPPoolGetter
	deploymentID         string
	externalIPPools      map[string]string
}

func NewMapperFactory(
	externalIPPoolClient ExternalIPPoolGetter,
	deploymentID string,
	externalIPPools map[string]string,
) (*MapperFactory, error) {
	if externalIPPoolClient == nil {
		return nil, fmt.Errorf("external IP pool client is required")
	}
	if deploymentID == "" {
		return nil, fmt.Errorf("deployment ID is required")
	}
	if externalIPPools == nil {
		return nil, fmt.Errorf("external IP pool cache is required")
	}
	return &MapperFactory{
		externalIPPoolClient: externalIPPoolClient,
		deploymentID:         deploymentID,
		externalIPPools:      externalIPPools,
	}, nil
}

func (f *MapperFactory) MapperForEvent(ctx context.Context, event *privatev1.Event) (events.ResourceMapper, error) {
	if ip := event.GetExternalIp(); ip != nil {
		poolID := ip.GetSpec().GetPool().GetId()
		if _, ok := f.externalIPPools[poolID]; !ok {
			response, err := f.externalIPPoolClient.Get(ctx, &privatev1.ExternalIPPoolsGetRequest{Id: poolID})
			if err != nil {
				return nil, fmt.Errorf("getting external IP pool %s: %w", poolID, err)
			}
			if response.GetObject().GetId() != poolID {
				return nil, fmt.Errorf("external IP pool lookup returned %s for requested pool %s", response.GetObject().GetId(), poolID)
			}
			family, err := events.ExternalIPPoolFamily(response.GetObject())
			if err != nil {
				return nil, err
			}
			f.externalIPPools[poolID] = family
		}
		return events.NewExternalIPMapper(ip, f.deploymentID, f.externalIPPools), nil
	}
	if gateway := event.GetNatGateway(); gateway != nil {
		return events.NewNATGatewayMapper(gateway, f.deploymentID), nil
	}
	return events.MapperForEvent(event)
}
