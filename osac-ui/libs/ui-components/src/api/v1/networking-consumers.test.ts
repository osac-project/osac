import { describe, expect, it } from 'vitest';

import { buildWorkloadNamesBySubnetId } from './networking-consumers';

const instance = (name: string, subnetRefs: string[]) =>
  ({
    id: `${name}-id`,
    metadata: { name },
    spec: { networkAttachments: subnetRefs.map((id) => ({ subnet: { id } })) },
  }) as never;

const cluster = (name: string, subnetId: string | undefined) =>
  ({
    id: `${name}-id`,
    metadata: { name },
    spec: { networkAttachment: subnetId ? { subnet: { id: subnetId } } : undefined },
  }) as never;

describe('buildWorkloadNamesBySubnetId', () => {
  it('indexes compute instance and cluster attachments by subnet', () => {
    const bySubnetId = buildWorkloadNamesBySubnetId(
      [instance('vm-a', ['subnet-1']), instance('vm-b', ['subnet-1', 'subnet-2'])],
      [cluster('cluster-a', 'subnet-2')],
    );

    expect(bySubnetId.get('subnet-1')).toEqual(['vm-a', 'vm-b']);
    expect(bySubnetId.get('subnet-2')).toEqual(['vm-b', 'cluster-a']);
    expect(bySubnetId.get('subnet-3')).toBeUndefined();
  });

  it('ignores workloads without a subnet and instances without attachments', () => {
    const bySubnetId = buildWorkloadNamesBySubnetId(
      [instance('vm-a', []), { id: 'vm-empty', metadata: {} } as never],
      [cluster('cluster-a', undefined)],
    );

    expect(bySubnetId.size).toBe(0);
  });

  it('falls back to the id when a workload has no name', () => {
    const bySubnetId = buildWorkloadNamesBySubnetId(
      [
        {
          id: 'vm-1',
          metadata: {},
          spec: { networkAttachments: [{ subnet: { id: 'subnet-1' } }] },
        } as never,
      ],
      [],
    );

    expect(bySubnetId.get('subnet-1')).toEqual(['vm-1']);
  });

  it('counts a subnet the instance reports but does not configure', () => {
    const bySubnetId = buildWorkloadNamesBySubnetId(
      [
        {
          id: 'vm-1',
          metadata: { name: 'vm-a' },
          spec: { networkAttachments: [] },
          status: {
            computeNetworkAttachmentStatuses: [{ subnetRef: 'subnet-9' }],
          },
        } as never,
      ],
      [],
    );

    expect(bySubnetId.get('subnet-9')).toEqual(['vm-a']);
  });

  it('does not duplicate a workload that reports the same subnet twice', () => {
    const bySubnetId = buildWorkloadNamesBySubnetId(
      [instance('vm-a', ['subnet-1', 'subnet-1'])],
      [],
    );

    expect(bySubnetId.get('subnet-1')).toEqual(['vm-a']);
  });
});
