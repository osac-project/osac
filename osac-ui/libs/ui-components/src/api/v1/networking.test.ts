import React, { type ReactNode, createElement } from 'react';
import { create, fromBinary, fromJson, toBinary, toJson } from '@bufbuild/protobuf';
import { createClient, createRouterTransport } from '@connectrpc/connect';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import {
  ExternalIPState,
  Protocol,
  SecurityGroupState,
  SecurityGroups,
  SubnetSchema,
  SubnetState,
  Subnets,
  VirtualNetworkState,
} from '@osac/types';

import {
  VIRTUAL_NETWORK_READY_LIST_FILTER,
  externalIpIdsFilter,
  invalidateSecurityGroupsQueries,
  invalidateSubnetsQueries,
  invalidateVirtualNetworksQueries,
  securityGroupFilterForVirtualNetwork,
  securityGroupFilterForVirtualNetworkList,
  unallocatedExternalIpFilter,
  useUpdateSecurityGroup,
  virtualNetworkFilterForSubnetList,
  virtualNetworkScopeFilter,
} from './networking';
import { createMockConnectTransport } from '../../test-utils/createMockConnectTransport';
import { ApiProvider } from '../api-context';
import { escapeCelStringLiteral } from '../cel';


    );
  });

  it('escapes quotes in virtual network id when building subnet filter', () => {
    expect(virtualNetworkFilterForSubnetList('vn-"evil')).toBe(
      `this.spec.virtual_network.id == "vn-\\"evil" && this.status.state == ${SubnetState.READY}`,
    );
  });

  it('escapes CEL injection characters in virtual network id when building subnet filter', () => {
    expect(virtualNetworkFilterForSubnetList(`"'] || true || this.id in ['`)).toBe(
      `this.spec.virtual_network.id == "\\"'] || true || this.id in ['" && this.status.state == ${SubnetState.READY}`,
    );
  });

  it('scopes to the virtual network without a ready-state filter', () => {
    expect(virtualNetworkScopeFilter('vn-1')).toBe('this.spec.virtual_network.id == "vn-1"');
  });

  it('escapes CEL injection in virtual network id for the scope-only filter', () => {
    expect(virtualNetworkScopeFilter(`"'] || true || this.id in ['`)).toBe(
      `this.spec.virtual_network.id == "\\"'] || true || this.id in ['"`,
    );
  });

  it('combines virtual network scope and ready state for security groups', () => {
    expect(securityGroupFilterForVirtualNetworkList('vn-1')).toBe(
      `this.spec.virtual_network.id == "vn-1" && this.status.state == ${SecurityGroupState.READY}`,
    );
  });

  it('filters security groups by virtual network id', () => {
    expect(securityGroupFilterForVirtualNetwork('vn-123')).toBe(
      'this.spec.virtual_network.id == "vn-123"',
    );
  });

  it('escapes quotes in virtual network id for security group filter', () => {
    expect(securityGroupFilterForVirtualNetwork('vn-"evil')).toBe(
      'this.spec.virtual_network.id == "vn-\\"evil"',
    );
  });

  it('escapes CEL injection in virtual network id for security group filter', () => {
    expect(securityGroupFilterForVirtualNetwork(`"'] || true || this.id in ['`)).toBe(
      `this.spec.virtual_network.id == "\\"'] || true || this.id in ['"`,
    );
  });

  it('escapes trailing backslash in virtual network id for security group filter', () => {
    expect(securityGroupFilterForVirtualNetwork('vn-\\')).toBe(
      'this.spec.virtual_network.id == "vn-\\\\"',
    );
  });
});

describe('networking query invalidation', () => {
  const asApiQueryClient = (qc: QueryClient) =>
    qc as unknown as Parameters<typeof invalidateVirtualNetworksQueries>[0];

  it('invalidates both the list and by-id virtual network queries', async () => {
    const qc = new QueryClient();
    qc.setQueryData(['v1/virtual_networks'], { items: [] });
    qc.setQueryData(['v1/virtual_networks', ['vn-1']], { id: 'vn-1' });

    await invalidateVirtualNetworksQueries(asApiQueryClient(qc));

    expect(qc.getQueryState(['v1/virtual_networks'])?.isInvalidated).toBe(true);
    expect(qc.getQueryState(['v1/virtual_networks', ['vn-1']])?.isInvalidated).toBe(true);
  });

  it('invalidates both the list and by-id subnet queries', async () => {
    const qc = new QueryClient();
    qc.setQueryData(['v1/subnets'], { items: [] });
    qc.setQueryData(['v1/subnets', ['subnet-1']], { id: 'subnet-1' });

    await invalidateSubnetsQueries(asApiQueryClient(qc));

    expect(qc.getQueryState(['v1/subnets'])?.isInvalidated).toBe(true);
    expect(qc.getQueryState(['v1/subnets', ['subnet-1']])?.isInvalidated).toBe(true);
  });

  it('invalidates both the list and by-id security group queries', async () => {
    const qc = new QueryClient();
    qc.setQueryData(['v1/security_groups'], { items: [] });
    qc.setQueryData(['v1/security_groups', ['sg-1']], { id: 'sg-1' });

    await invalidateSecurityGroupsQueries(asApiQueryClient(qc));

    expect(qc.getQueryState(['v1/security_groups'])?.isInvalidated).toBe(true);
    expect(qc.getQueryState(['v1/security_groups', ['sg-1']])?.isInvalidated).toBe(true);
  });
});

describe('useUpdateSecurityGroup', () => {
  it('sends rule updates through the public API', async () => {
    let captured: { object?: unknown } | undefined;
    const transport = createRouterTransport((router) => {
      router.service(SecurityGroups, {
        update: (request) => {
          captured = request;
          return { object: request.object };
        },
      });
    });
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    const wrapper = ({ children }: { children: ReactNode }) =>
      createElement(
        ApiProvider,
        { transport } as React.ComponentProps<typeof ApiProvider>,
        createElement(QueryClientProvider, { client: queryClient }, children),
      );
    const { result } = renderHook(() => useUpdateSecurityGroup(), { wrapper });
    const object = {
      id: 'sg-1',
      metadata: { name: 'sg-web' },
      spec: {
        virtualNetwork: { id: 'vn-1' },
        ingress: [{ protocol: Protocol.TCP, portFrom: 443, portTo: 443 }],
        egress: [],
      },
    };

    await act(async () => {
      await result.current.mutateAsync({ object });
    });

    expect(captured?.object).toMatchObject(object);
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
  });
});
