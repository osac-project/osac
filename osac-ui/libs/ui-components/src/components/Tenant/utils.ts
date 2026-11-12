import type { TFunction } from 'i18next';

import { type Tenant, TenantState } from '@osac/types/private';
import { type CelFilter, cel } from '@osac/ui-components/api/cel';

export const STATE_FILTER_VALUES = ['synced', 'pending', 'failed'] as const;

export type TenantStateFilterValue = (typeof STATE_FILTER_VALUES)[number];

export const STATE_FILTER_PARAM = 'state';

export const isTenantStateFilterValue = (value: string): value is TenantStateFilterValue =>
  STATE_FILTER_VALUES.includes(value as TenantStateFilterValue);

export const TENANT_STATE_FILTER_TO_ENUM: Record<TenantStateFilterValue, TenantState> = {
  synced: TenantState.SYNCED,
  pending: TenantState.PENDING,
  failed: TenantState.FAILED,
};

export const getTenantStateLabels = (t: TFunction): Record<TenantState, string> => ({
  [TenantState.UNSPECIFIED]: t('Unspecified'),
  [TenantState.PENDING]: t('Pending'),
  [TenantState.SYNCED]: t('Synced'),
  [TenantState.FAILED]: t('Failed'),
});

export const buildTenantListFilter = (
  stateFilters: TenantState[],
  nameSearch: string,
): CelFilter<Tenant> | undefined =>
  cel<Tenant>((b) =>
    b.and(
      stateFilters.length > 0 ? b.field('status.state').isIn(stateFilters) : undefined,
      nameSearch ? b.field('metadata.name').contains(nameSearch) : undefined,
    ),
  );
