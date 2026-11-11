import { useCallback, useMemo } from 'react';

import { Tenants } from '@osac/types/private';

import { useListAllResources } from '../../api/use-resource';
import { useSession } from '../../hooks/use-session';

const HIDDEN_TENANT_IDS = new Set(['system', 'shared']);

/**
 * The tenants a provider admin can filter networking lists by. Tenant users get
 * no tenant column or filter, so the list is loaded only for the admin.
 */
export const useNetworkingTenants = () => {
  const { role } = useSession();
  const isAdmin = role === 'admin';
  const { data } = useListAllResources(Tenants, undefined, { enabled: isAdmin });
  const tenants = useMemo(
    () => (data?.items ?? []).filter((tenant) => !HIDDEN_TENANT_IDS.has(tenant.id)),
    [data],
  );
  const tenantName = useCallback(
    (tenantId?: string) =>
      tenants.find((tenant) => tenant.id === tenantId)?.metadata?.name || tenantId || '—',
    [tenants],
  );

  return { isAdmin, tenants, tenantName };
};
