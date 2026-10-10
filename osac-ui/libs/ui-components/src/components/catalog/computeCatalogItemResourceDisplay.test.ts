import { describe, expect, it } from 'vitest';

import {
  computeDiskImageReferenceFromPolicy,
  computeInstanceTypeReferenceFromPolicy,
  int32ValueFromPolicy,
} from './computeCatalogItemResourceDisplay';

type Int32Policy = Parameters<typeof int32ValueFromPolicy>[0];
type InstanceTypePolicy = Parameters<typeof computeInstanceTypeReferenceFromPolicy>[0];
type DiskImagePolicy = Parameters<typeof computeDiskImageReferenceFromPolicy>[0];

describe('computeCatalogItemResourceDisplay', () => {
  it('reads locked and editable int32 field policies', () => {
    expect(int32ValueFromPolicy({ behavior: { case: 'locked', value: 40 } } as Int32Policy)).toBe(
      40,
    );
    expect(
      int32ValueFromPolicy({
        behavior: { case: 'editable', value: { defaultValue: 80 } },
      } as Int32Policy),
    ).toBe(80);
    expect(int32ValueFromPolicy(undefined)).toBeUndefined();
  });

  it('reads instance type and disk image references from field policies', () => {
    const lockedRef = { id: 'it-1', name: 'standard-4-8', project: 'default', shared: false };
    expect(
      computeInstanceTypeReferenceFromPolicy({
        behavior: { case: 'locked', value: lockedRef },
      } as InstanceTypePolicy),
    ).toEqual(lockedRef);
    expect(
      computeInstanceTypeReferenceFromPolicy({
        behavior: { case: 'editable', value: { defaultValue: lockedRef } },
      } as InstanceTypePolicy),
    ).toEqual(lockedRef);

    const diskRef = { id: 'di-1', name: 'RHEL 10', project: 'default', shared: false };
    expect(
      computeDiskImageReferenceFromPolicy({
        behavior: { case: 'locked', value: diskRef },
      } as DiskImagePolicy),
    ).toEqual(diskRef);
  });
});
