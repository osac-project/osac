import type { UserRole } from '../../../shellTypes';
import type { CatalogItem } from '../catalogItemDisplay';

/** True when an admin may manage any item, or a tenant-admin may manage an item in their tenant. */
export const canManageCatalogItem = (
  role: UserRole,
  tenantId: string,
  item: CatalogItem,
): boolean => {
  if (role === 'admin') {
    return true;
  }
  if (role === 'tenant-admin') {
    return item.metadata?.tenant === tenantId;
  }
  return false;
};
