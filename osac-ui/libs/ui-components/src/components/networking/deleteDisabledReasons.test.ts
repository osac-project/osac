import type { TFunction } from 'i18next';
import { describe, expect, it } from 'vitest';

import { virtualNetworkDeleteBlockedReason } from './deleteDisabledReasons';

const t = ((key: string, options?: Record<string, unknown>) =>
  key.replace(/{{(\w+)}}/g, (_match, name: string) =>
    String((options?.[name] as string | number | undefined) ?? ''),
  )) as unknown as TFunction;

describe('virtualNetworkDeleteBlockedReason', () => {
  it('is undefined without dependents, so the confirmation and server decide', () => {
    expect(
      virtualNetworkDeleteBlockedReason(t, { subnetCount: 0, securityGroupCount: 0 }),
    ).toBeUndefined();
  });

  it('names every loaded dependent', () => {
    const reason = virtualNetworkDeleteBlockedReason(t, {
      subnetCount: 2,
      securityGroupCount: 1,
      natGatewayName: 'default-nat',
      fabricDomainCount: 1,
    });
    expect(reason).toContain('2 subnet');
    expect(reason).toContain('1 security group');
    expect(reason).toContain('default-nat');
    expect(reason).toContain('1 fabric domain');
  });

  it('names a NAT gateway on its own', () => {
    expect(
      virtualNetworkDeleteBlockedReason(t, {
        subnetCount: 0,
        securityGroupCount: 0,
        natGatewayName: 'nat-a',
      }),
    ).toContain('nat-a');
  });
});
