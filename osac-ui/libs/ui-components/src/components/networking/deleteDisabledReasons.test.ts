import type { TFunction } from 'i18next';
import { describe, expect, it } from 'vitest';

import type { BareMetalInstance, Cluster, ComputeInstance } from '@osac/types';

import { clustersUseNatGatewayReason, usedByWorkloadsReason } from './deleteDisabledReasons';
import { protectedResourceReason } from './resourceOrigin';
import { buildNetworkConsumers } from '../../api/v1/networking-consumers';

const t = ((key: string, values?: Record<string, unknown>) =>
  key.replace(/\{\{(\w+)\}\}/g, (_, name: string) => String(values?.[name]))) as TFunction;

describe('buildNetworkConsumers', () => {
  it('collects the subnets and security groups of every workload kind', () => {
    const consumers = buildNetworkConsumers({
      computeInstances: [
        {
          id: 'vm-1',
          metadata: { name: 'web-1' },
          spec: {
            networkAttachments: [{ subnet: { id: 's-1' }, securityGroups: [{ id: 'sg-1' }] }],
          },
        } as unknown as ComputeInstance,
      ],
      bareMetalInstances: [
        {
          id: 'bm-1',
          spec: { networkAttachments: [{ subnet: { id: 's-2' } }] },
        } as unknown as BareMetalInstance,
      ],
      clusters: [
        {
          id: 'c-1',
          metadata: { name: 'c1' },
          spec: { networkAttachment: { subnet: { id: 's-3' }, securityGroups: [{ id: 'sg-2' }] } },
        } as unknown as Cluster,
        { id: 'c-2', metadata: { name: 'c2' }, spec: {} } as unknown as Cluster,
      ],
    });

    expect(consumers).toEqual([
      {
        kind: 'computeInstance',
        id: 'vm-1',
        name: 'web-1',
        subnetIds: ['s-1'],
        securityGroupIds: ['sg-1'],
      },
      {
        kind: 'baremetalInstance',
        id: 'bm-1',
        name: 'bm-1',
        subnetIds: ['s-2'],
        securityGroupIds: [],
      },
      { kind: 'cluster', id: 'c-1', name: 'c1', subnetIds: ['s-3'], securityGroupIds: ['sg-2'] },
      { kind: 'cluster', id: 'c-2', name: 'c2', subnetIds: [], securityGroupIds: [] },
    ]);
  });
});

describe('delete disabled reasons', () => {
  const consumer = (name: string) => ({
    kind: 'cluster' as const,
    id: name,
    name,
    subnetIds: [],
    securityGroupIds: [],
  });

  it('names the first workload and counts the others', () => {
    expect(usedByWorkloadsReason(t, [])).toBeUndefined();
    expect(usedByWorkloadsReason(t, [consumer('c1')])).toBe('Used by cluster c1. Delete it first.');
    expect(usedByWorkloadsReason(t, [consumer('c1'), consumer('c2'), consumer('c3')])).toBe(
      'Used by cluster c1 and 2 more. Delete them first.',
    );
  });

  it('names the clusters that use a NAT gateway', () => {
    expect(clustersUseNatGatewayReason(t, [])).toBeUndefined();
    expect(clustersUseNatGatewayReason(t, [consumer('c1')])).toBe(
      'Cluster c1 reaches its management endpoints through this gateway. Delete it first.',
    );
    expect(clustersUseNatGatewayReason(t, [consumer('c1'), consumer('c2')])).toBe(
      'Cluster c1 and 1 more reach their management endpoints through this gateway. Delete them first.',
    );
  });

  it('explains Default and Auto resources', () => {
    expect(protectedResourceReason(t, { metadata: { labels: {} } })).toBeUndefined();
    expect(
      protectedResourceReason(t, { metadata: { labels: { 'osac.openshift.io/default': 'true' } } }),
    ).toBe('Created as a tenant default. It is deleted with the tenant.');
    expect(
      protectedResourceReason(t, {
        metadata: {
          labels: {
            'osac.openshift.io/auto-created': 'true',
            'osac.openshift.io/auto-created-for': 'vm-1',
            'osac.openshift.io/auto-created-kind': 'compute_instance',
          },
        },
      }),
    ).toBe('Created automatically for virtual machine vm-1. It is deleted with it.');
  });
});
