import { describe, expect, it } from 'vitest';

import {
  AUTO_CREATED_LABEL,
  DEFAULT_LABEL,
  autoCreatedFor,
  isProtectedResource,
  resourceOrigin,
} from './resourceOrigin';

describe('resourceOrigin', () => {
  it('reads the default label', () => {
    expect(resourceOrigin({ [DEFAULT_LABEL]: 'true' })).toBe('default');
    expect(isProtectedResource({ [DEFAULT_LABEL]: 'true' })).toBe(true);
  });

  it('reads the auto label and its owner', () => {
    expect(resourceOrigin({ [AUTO_CREATED_LABEL]: 'virtual-network/x' })).toBe('auto');
    expect(autoCreatedFor({ [AUTO_CREATED_LABEL]: 'virtual-network/x' })).toBe('virtual-network/x');
  });

  it('leaves a user-created resource unprotected', () => {
    expect(resourceOrigin({ other: 'label' })).toBeUndefined();
    expect(resourceOrigin(undefined)).toBeUndefined();
    expect(isProtectedResource({})).toBe(false);
  });

  it('prefers default when a resource carries both labels', () => {
    expect(resourceOrigin({ [DEFAULT_LABEL]: 'true', [AUTO_CREATED_LABEL]: 'x' })).toBe('default');
  });
});
