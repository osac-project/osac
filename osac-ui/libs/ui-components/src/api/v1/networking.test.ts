import { fromBinary, fromJson, toBinary, toJson } from '@bufbuild/protobuf';
import { QueryClient } from '@tanstack/react-query';
import { describe, expect, it } from 'vitest';

import {
  ExternalIPState,
  SecurityGroupState,
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
  virtualNetworkFilterForSubnetList,
  virtualNetworkScopeFilter,
} from './networking';
import { escapeCelStringLiteral } from '../cel';

describe('networking list filters', () => {
  it.each([undefined, false, true])(
    'preserves optional eligibility through the stable service descriptor: %s',
    (supported) => {
      const schema = Subnets.method.list.output;
      const response = fromJson(schema, {
        items: [
          {
            id: 'subnet',
            status: {
              state: SubnetState.READY,
              ...(supported === undefined ? {} : { computeInstanceSupported: supported }),
              computeInstanceUnsupportedReason: 'backend explanation',
            },
          },
        ],
      });
      for (const roundtrip of [
        fromJson(schema, toJson(schema, response)),
        fromBinary(schema, toBinary(schema, response)),
      ]) {
        expect(roundtrip.items[0].status?.computeInstanceSupported).toBe(supported);
        expect(roundtrip.items[0].status?.computeInstanceUnsupportedReason).toBe(
          'backend explanation',
        );
        expect(roundtrip.items[0].status?.state).toBe(SubnetState.READY);
      }
    },
  );

  it('filters virtual networks to ready state using enum integer', () => {
    expect(VIRTUAL_NETWORK_READY_LIST_FILTER).toBe(
      `this.status.state == ${VirtualNetworkState.READY}`,
    );
  });

  it('filters external IPs to allocated and unattached', () => {
    expect(unallocatedExternalIpFilter()).toBe(
      `this.status.state == ${ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED} && this.status.attached == false`,
    );
  });

  it('filters external IPs to the requested project', () => {
    expect(unallocatedExternalIpFilter('project-a')).toBe(
      `this.metadata.project == "project-a" && this.status.state == ${ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED} && this.status.attached == false`,
    );
  });

  it('filters external IPs by referenced IDs', () => {
    expect(externalIpIdsFilter(['ip-aaa', 'ip-bbb'])).toBe('this.id in ["ip-aaa", "ip-bbb"]');
  });

  it('escapes embedded quotes for CEL string literals', () => {
    expect(escapeCelStringLiteral('say "hello"')).toBe('say \\"hello\\"');
  });

  it('escapes backslashes for CEL string literals', () => {
    expect(escapeCelStringLiteral('path\\to\\thing')).toBe('path\\\\to\\\\thing');
  });

  it('combines virtual network scope and ready state for subnets', () => {
    expect(virtualNetworkFilterForSubnetList('vn-1')).toBe(
      `this.spec.virtual_network.id == "vn-1" && this.status.state == ${SubnetState.READY}`,
    );
  });

  it('requires backend VM capability only for VM choices', () => {
    expect(virtualNetworkFilterForSubnetList('vn-1', true)).toBe(
      `this.spec.virtual_network.id == "vn-1" && this.status.state == ${SubnetState.READY} && this.status.compute_instance_supported == true`,
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
