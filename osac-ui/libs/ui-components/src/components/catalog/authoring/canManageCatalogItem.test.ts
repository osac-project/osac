import { describe, expect, it } from 'vitest';

import type { ComputeInstanceCatalogItem } from '@osac/types';

import { canManageCatalogItem } from './canManageCatalogItem';

const makeItem = (tenant: string): ComputeInstanceCatalogItem =>
  ({
    $typeName: 'osac.public.v1.ComputeInstanceCatalogItem',
    id: 'catalog-1',
    metadata: { tenant },
  }) as unknown as ComputeInstanceCatalogItem;

describe('canManageCatalogItem', () => {
  it('allows an admin to manage any item regardless of tenant', () => {
    expect(canManageCatalogItem('admin', 'other-tenant', makeItem('foo'))).toBe(true);
    expect(canManageCatalogItem('admin', 'other-tenant', makeItem('shared'))).toBe(true);
  });

  it('allows a tenant-admin to manage an item in their own tenant', () => {
    expect(canManageCatalogItem('tenant-admin', 'foo', makeItem('foo'))).toBe(true);
  });

  it('denies a tenant-admin managing an item in a different tenant', () => {
    expect(canManageCatalogItem('tenant-admin', 'foo', makeItem('bar'))).toBe(false);
  });

  it('denies a tenant-user regardless of tenant', () => {
    expect(canManageCatalogItem('tenant-user', 'foo', makeItem('foo'))).toBe(false);
  });

  it('denies a tenant-idp-manager regardless of tenant', () => {
    expect(canManageCatalogItem('tenant-idp-manager', 'foo', makeItem('foo'))).toBe(false);
  });
});
