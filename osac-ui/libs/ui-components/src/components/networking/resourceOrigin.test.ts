import type { TFunction } from 'i18next';
import { describe, expect, it } from 'vitest';

import { autoCreatedTooltip } from './resourceOrigin';

const t = ((key: string, values?: Record<string, string>) =>
  key.replace(/\{\{(\w+)\}\}/g, (_match, name: string) => values?.[name] ?? '')) as TFunction;

const autoCreated = (kind: string) => ({
  metadata: {
    labels: {
      'osac.openshift.io/auto-created': 'true',
      'osac.openshift.io/auto-created-kind': kind,
      'osac.openshift.io/auto-created-for': 'owner-1',
    },
  },
});

describe('autoCreatedTooltip', () => {
  it.each([
    ['cluster', 'cluster'],
    ['compute_instance', 'virtual machine'],
    ['bare_metal_instance', 'bare-metal instance'],
  ])('names the owner for the kind label %s', (kind, label) => {
    expect(autoCreatedTooltip(t, autoCreated(kind))).toBe(
      `Created automatically for ${label} owner-1. It is deleted with it.`,
    );
  });

  it('names the target of an automatic attachment, which has no kind label', () => {
    expect(
      autoCreatedTooltip(t, {
        metadata: { labels: { 'osac.openshift.io/auto-created': 'true' } },
        spec: { target: { case: 'cluster', value: { id: 'c1', name: 'prod' } } },
      }),
    ).toBe('Created automatically for cluster prod. It is deleted with it.');
  });

  it('falls back when the kind is unknown', () => {
    expect(autoCreatedTooltip(t, autoCreated('other'))).toBe(
      'Created automatically by OSAC. It is deleted with its owner.',
    );
  });
});
