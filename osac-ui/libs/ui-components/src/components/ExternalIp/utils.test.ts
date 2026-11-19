import type { TFunction } from 'i18next';
import { describe, expect, it } from 'vitest';

import type { ExternalIP } from '@osac/types';
import { ExternalIPState } from '@osac/types';

import { deleteDisabledReason } from './utils';

const t = ((key: string) => key) as unknown as TFunction;

const externalIp = (overrides: Record<string, unknown> = {}): ExternalIP =>
  ({
    id: 'eip-1',
    metadata: { name: 'eip-1', labels: {} },
    status: {
      state: ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED,
      attached: false,
    },
    ...overrides,
  }) as ExternalIP;

describe('deleteDisabledReason', () => {
  it('returns the system-managed reason for a default external IP', () => {
    const ip = externalIp({
      metadata: {
        name: 'eip-1',
        labels: { 'osac.openshift.io/default': 'true' },
      },
    });

    expect(deleteDisabledReason(ip, t)).toBe(
      'Default networking resources are system-managed and cannot be deleted.',
    );
  });

  it('returns the deleting reason while the external IP is being deleted', () => {
    const ip = externalIp({
      status: {
        state: ExternalIPState.EXTERNAL_IP_STATE_DELETING,
        attached: false,
      },
    });

    expect(deleteDisabledReason(ip, t)).toBe('This external IP is being deleted.');
  });

  it('returns the detach reason while the external IP is attached', () => {
    const ip = externalIp({
      status: {
        state: ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED,
        attached: true,
      },
    });

    expect(deleteDisabledReason(ip, t)).toBe('Detach this external IP before deleting it.');
  });

  it('returns undefined for a deletable external IP', () => {
    expect(deleteDisabledReason(externalIp(), t)).toBeUndefined();
  });
});
