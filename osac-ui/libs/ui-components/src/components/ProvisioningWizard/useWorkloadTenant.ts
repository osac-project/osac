import { useFormikContext } from 'formik';

import { useSession } from '../../hooks/use-session';
import type { ResourceSelectValue } from '../Form/resourceSelectValue';

export interface WorkloadTenantFormValues {
  metadata: { tenant?: ResourceSelectValue };
}

export interface WorkloadTenant {
  isAdmin: boolean;
  /** The tenant the workload is created in; empty while a cloud provider admin hasn't chosen one. */
  tenant: string;
  /**
   * The tenant chosen by a cloud provider admin, or undefined for tenant users. Lookups add a
   * tenant filter only when it is defined: the server already scopes tenant users to their tenant,
   * while an admin sees every tenant.
   */
  adminTenant: string | undefined;
}

/**
 * Returns the tenant a provisioning wizard creates its workload in. Cloud provider admins choose it
 * in the catalog step (`metadata.tenant`); tenant users always provision in their session tenant.
 */
export const useWorkloadTenant = (): WorkloadTenant => {
  const { role, tenantId } = useSession();
  const { values } = useFormikContext<WorkloadTenantFormValues>();
  const isAdmin = role === 'admin';
  const selected = values.metadata?.tenant?.id ?? '';
  return {
    isAdmin,
    tenant: isAdmin ? selected : tenantId,
    adminTenant: isAdmin ? selected : undefined,
  };
};

/** The `metadata.tenant` to send on create: only set when a cloud provider admin chose a tenant. */
export const workloadTenantMetadata = (tenant: ResourceSelectValue | undefined) =>
  tenant?.id ? { tenant: tenant.id } : {};

/**
 * Starts a cloud provider admin in the tenant of a tenant-scoped catalog item opened from a link.
 * A shared item leaves the tenant for the admin to choose.
 */
export const withCatalogItemTenant = <V extends { metadata: { tenant?: ResourceSelectValue } }>(
  values: V,
  isAdmin: boolean,
  catalogItemTenant: string | undefined,
): V =>
  isAdmin && catalogItemTenant && catalogItemTenant !== 'shared'
    ? {
        ...values,
        metadata: {
          ...values.metadata,
          tenant: { id: catalogItemTenant, name: catalogItemTenant },
        },
      }
    : values;
