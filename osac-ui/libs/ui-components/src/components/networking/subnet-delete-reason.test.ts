import type { TFunction } from 'i18next';
import { describe, expect, it } from 'vitest';

import { SubnetState } from '@osac/types';

import { subnetDeleteGuard } from './subnet-delete-reason';
import { defaultResourceDeleteReason } from '../../utils/networking';

const t = ((key: string, options?: Record<string, unknown>) =>
  options
    ? key.replace(/\{\{(\w+)\}\}/g, (_match, name: string) => {
        const value = options[name];
        return typeof value === 'string' ? value : '';
      })
    : key) as TFunction;

const makeSubnet = (overrides: Record<string, unknown> = {}) =>
  ({
    id: 'subnet-1',
    metadata: { name: 'web-a', labels: {}, project: 'web-team' },
    spec: { virtualNetwork: { id: 'vn-1' }, ipv4Cidr: '10.20.1.0/24' },
    status: { state: SubnetState.READY },
    ...overrides,
  }) as never;

describe('subnetDeleteGuard', () => {
  it('allows deleting a ready subnet no workload uses', () => {
    expect(subnetDeleteGuard(t, { subnet: makeSubnet(), workloadNames: [] })).toEqual({
      canDelete: true,
    });
  });

  it('leaves Delete enabled when the consumers could not be loaded', () => {
    expect(subnetDeleteGuard(t, { subnet: makeSubnet(), workloadNames: undefined })).toEqual({
      canDelete: true,
    });
  });

  it('blocks a default subnet with the shared default reason', () => {
    const subnet = makeSubnet({
      metadata: { name: 'default', labels: { 'osac.openshift.io/default': 'true' } },
    });

    expect(subnetDeleteGuard(t, { subnet })).toEqual({
      canDelete: false,
      disabledReason: defaultResourceDeleteReason(t),
    });
  });

  it('blocks an auto-created subnet', () => {
    const subnet = makeSubnet({
      metadata: {
        name: 'auto',
        labels: { 'osac.openshift.io/auto-created-for': 'vm-1' },
      },
    });

    expect(subnetDeleteGuard(t, { subnet })).toEqual({
      canDelete: false,
      disabledReason: 'Auto-created subnets cannot be deleted.',
    });
  });

  it('treats the default label as the reason when both labels are set', () => {
    const subnet = makeSubnet({
      metadata: {
        labels: {
          'osac.openshift.io/default': 'true',
          'osac.openshift.io/auto-created-for': 'vm-1',
        },
      },
    });

    expect(subnetDeleteGuard(t, { subnet }).disabledReason).toBe(defaultResourceDeleteReason(t));
  });

  it('blocks a subnet that is being deleted', () => {
    const subnet = makeSubnet({ status: { state: SubnetState.DELETING } });

    expect(subnetDeleteGuard(t, { subnet })).toEqual({
      canDelete: false,
      disabledReason: 'This subnet is being deleted.',
    });
  });

  it('names the workloads that use the subnet', () => {
    expect(
      subnetDeleteGuard(t, { subnet: makeSubnet(), workloadNames: ['vm-a', 'cluster-b'] }),
    ).toEqual({
      canDelete: false,
      disabledReason: 'In use by vm-a, cluster-b. Detach the workload before deleting it.',
    });
  });
});
