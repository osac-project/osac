package watch_test

import (
	"context"
	"testing"

	"google.golang.org/grpc"

	"github.com/osac-project/osac-metering/internal/watch"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type poolGetter struct {
	response *privatev1.ExternalIPPoolsGetResponse
	err      error
	calls    int
}

func (p *poolGetter) Get(context.Context, *privatev1.ExternalIPPoolsGetRequest, ...grpc.CallOption) (*privatev1.ExternalIPPoolsGetResponse, error) {
	p.calls++
	return p.response, p.err
}

func TestNewMapperFactoryRequiresDependencies(t *testing.T) {
	if _, err := watch.NewMapperFactory(nil, "deployment-1", map[string]string{}); err == nil {
		t.Fatal("expected a missing pool client to fail")
	}
	getter := &poolGetter{}
	if _, err := watch.NewMapperFactory(getter, "", map[string]string{}); err == nil {
		t.Fatal("expected a missing deployment ID to fail")
	}
	if _, err := watch.NewMapperFactory(getter, "deployment-1", nil); err == nil {
		t.Fatal("expected a missing pool cache to fail")
	}
}

func TestMapperFactoryUsesCachedExternalIPPool(t *testing.T) {
	getter := &poolGetter{}
	factory, err := watch.NewMapperFactory(getter, "deployment-1", map[string]string{"pool-1": "ipv4"})
	if err != nil {
		t.Fatal(err)
	}
	ip := &privatev1.ExternalIP{
		Id:       "ip-1",
		Metadata: &privatev1.Metadata{Tenant: "tenant-1"},
		Spec:     &privatev1.ExternalIPSpec{Pool: &privatev1.ExternalIPPoolReference{Id: "pool-1"}},
	}

	mapper, err := factory.MapperForEvent(context.Background(), &privatev1.Event{Payload: &privatev1.Event_ExternalIp{ExternalIp: ip}})
	if err != nil {
		t.Fatal(err)
	}
	dimensions, err := mapper.BillingDimensionsMap()
	if err != nil {
		t.Fatal(err)
	}
	if getter.calls != 0 {
		t.Fatalf("cached pool caused %d lookups", getter.calls)
	}
	if dimensions["ip_family"] != "ipv4" || dimensions["deployment"] != "deployment-1" {
		t.Fatalf("unexpected ExternalIP dimensions: %#v", dimensions)
	}
}

func TestMapperFactoryLoadsAndCachesExternalIPPool(t *testing.T) {
	getter := &poolGetter{response: &privatev1.ExternalIPPoolsGetResponse{Object: &privatev1.ExternalIPPool{
		Id:   "pool-1",
		Spec: &privatev1.ExternalIPPoolSpec{IpFamily: privatev1.IPFamily_IP_FAMILY_IPV6},
	}}}
	factory, err := watch.NewMapperFactory(getter, "deployment-1", map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	ip := &privatev1.ExternalIP{
		Id:       "ip-1",
		Metadata: &privatev1.Metadata{Tenant: "tenant-1"},
		Spec:     &privatev1.ExternalIPSpec{Pool: &privatev1.ExternalIPPoolReference{Id: "pool-1"}},
	}
	event := &privatev1.Event{Payload: &privatev1.Event_ExternalIp{ExternalIp: ip}}

	mapper, err := factory.MapperForEvent(context.Background(), event)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mapper.BillingDimensionsMap(); err != nil {
		t.Fatal(err)
	}
	if _, err := factory.MapperForEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if getter.calls != 1 {
		t.Fatalf("expected one pool lookup, got %d", getter.calls)
	}
}

func TestMapperFactoryInjectsNATGatewayDeployment(t *testing.T) {
	factory, err := watch.NewMapperFactory(&poolGetter{}, "deployment-1", map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	gateway := &privatev1.NATGateway{
		Id:       "nat-1",
		Metadata: &privatev1.Metadata{Tenant: "tenant-1"},
		Spec: &privatev1.NATGatewaySpec{
			VirtualNetwork: &privatev1.VirtualNetworkLocalReference{Id: "vnet-1"},
			ExternalIp:     &privatev1.ExternalIPLocalReference{Id: "ip-1"},
		},
	}

	mapper, err := factory.MapperForEvent(context.Background(), &privatev1.Event{Payload: &privatev1.Event_NatGateway{NatGateway: gateway}})
	if err != nil {
		t.Fatal(err)
	}
	dimensions, err := mapper.BillingDimensionsMap()
	if err != nil {
		t.Fatal(err)
	}
	if dimensions["deployment"] != "deployment-1" {
		t.Fatalf("unexpected NATGateway dimensions: %#v", dimensions)
	}
}
