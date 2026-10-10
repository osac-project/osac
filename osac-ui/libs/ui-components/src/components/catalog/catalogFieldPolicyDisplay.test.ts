import { describe, expect, it } from 'vitest';

import {
  CATALOG_RESOURCE_EMPTY_DISPLAY,
  catalogFieldPolicyBehavior,
  catalogFieldPolicyIsConfigured,
  catalogResourceHasDisplayValue,
} from './catalogFieldPolicyDisplay';

describe('catalogFieldPolicyDisplay', () => {
  it('reads locked and editable policy behavior cases', () => {
    expect(catalogFieldPolicyBehavior({ behavior: { case: 'locked' } })).toBe('locked');
    expect(catalogFieldPolicyBehavior({ behavior: { case: 'editable' } })).toBe('editable');
    expect(catalogFieldPolicyBehavior({ behavior: { case: 'unset' } })).toBeUndefined();
    expect(catalogFieldPolicyIsConfigured({ behavior: { case: 'locked' } })).toBe(true);
    expect(catalogFieldPolicyIsConfigured(undefined)).toBe(false);
  });

  it('treats only non-empty resource labels as displayable', () => {
    expect(catalogResourceHasDisplayValue('RHEL 10')).toBe(true);
    expect(catalogResourceHasDisplayValue(CATALOG_RESOURCE_EMPTY_DISPLAY)).toBe(false);
  });
});
