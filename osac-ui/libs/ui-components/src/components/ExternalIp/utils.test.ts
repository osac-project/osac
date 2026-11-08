import type { TFunction } from 'i18next';
import { describe, expect, it } from 'vitest';

import { type ExternalIP, ExternalIPState } from '@osac/types';

import { attachedTargetKindLabel, deleteDisabledReason } from './utils';

const t = ((key: string, options?: Record<string, string>) =>
  key.replace(/\{\{(\w+)\}\}/g, (_match, name: string) => options?.[name] ?? '')) as TFunction;

const allocated = {
  id: 'eip-1',
  status: { state: ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED },
} as ExternalIP;

describe('ExternalIp utils', () => {
  it('names the bare-metal instance kind consistently', () => {
    expect(attachedTargetKindLabel(t, 'baremetalInstance')).toBe('Bare-metal instance');
  });

  it('asks to delete the NAT gateway that uses the external IP', () => {
    expect(
      deleteDisabledReason(allocated, t, { kind: 'natGateway', name: 'edge-nat', href: '/x' }),
    ).toBe('Delete NAT gateway edge-nat first.');
  });

  it('asks to delete the attachment to a workload', () => {
    expect(
      deleteDisabledReason(allocated, t, { kind: 'computeInstance', name: 'web-1', href: '/x' }),
    ).toBe('Delete its attachment to virtual machine web-1 first.');
  });

  it('allows Delete of an unused external IP', () => {
    expect(deleteDisabledReason(allocated, t)).toBeUndefined();
  });
});
