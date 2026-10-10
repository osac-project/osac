import { create } from '@bufbuild/protobuf';
import { describe, expect, it } from 'vitest';

import { ExternalIPSchema } from '@osac/types';

import { buildExternalIpCreatePayload } from './payload';
import { getExternalIpValues } from './values';

describe('buildExternalIpCreatePayload', () => {
  it('sends name, project, and pool on create', () => {
    expect(
      buildExternalIpCreatePayload({
        metadata: { name: 'edge-ip', project: 'default', description: 'Edge address' },
        pool: { id: 'pool-1', name: 'public-edge' },
      }),
    ).toEqual({
      metadata: { name: 'edge-ip', project: 'default', description: 'Edge address' },
      spec: { pool: { id: 'pool-1', name: 'public-edge' } },
    });
  });

  it('hydrates form values from an existing external IP', () => {
    const externalIp = create(ExternalIPSchema, {
      id: 'eip-1',
      metadata: { name: 'edge-ip', project: 'default', description: 'Edge address' },
      spec: { pool: { id: 'pool-1', name: 'public-edge' } },
    });

    expect(getExternalIpValues(externalIp)).toEqual({
      metadata: { name: 'edge-ip', project: 'default', description: 'Edge address' },
      pool: { id: 'pool-1', name: 'public-edge' },
    });
  });
});
