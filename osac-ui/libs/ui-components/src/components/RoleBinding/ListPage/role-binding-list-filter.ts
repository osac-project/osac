import type { TFunction } from 'i18next';

import { RoleBinding, RoleBindingState } from '@osac/types';
import { type CelFilter, cel } from '@osac/ui-components/api/cel';

export const ROLE_BINDING_STATUS_FILTERS = ['ready', 'pending', 'failed'] as const;

export type RoleBindingStatusFilter = (typeof ROLE_BINDING_STATUS_FILTERS)[number];

const STATUS_FILTER_TO_STATE: Record<RoleBindingStatusFilter, RoleBindingState> = {
  ready: RoleBindingState.READY,
  pending: RoleBindingState.PENDING,
  failed: RoleBindingState.FAILED,
};

export const isRoleBindingStatusFilter = (value: string): value is RoleBindingStatusFilter =>
  (ROLE_BINDING_STATUS_FILTERS as readonly string[]).includes(value);

export const roleBindingStatusFilterLabel = (
  filter: RoleBindingStatusFilter,
  t: TFunction,
): string => {
  switch (filter) {
    case 'ready':
      return t('Ready');
    case 'pending':
      return t('Pending');
    case 'failed':
      return t('Failed');
  }
};

export const isRoleBindingRoleFilter = (value: string): value is string => value.trim().length > 0;

export type RoleBindingListFilterCriteria = {
  search?: string;
  statuses?: readonly RoleBindingStatusFilter[];
  roleIds?: readonly string[];
};

export const buildRoleBindingListFilter = (
  criteria: RoleBindingListFilterCriteria,
): CelFilter<RoleBinding> | undefined => {
  const search = criteria.search?.trim();
  const statuses = criteria.statuses ?? [];
  const roleIds = criteria.roleIds ?? [];
  const hasCriteria = Boolean(search || statuses.length || roleIds.length);

  if (!hasCriteria) {
    return undefined;
  }

  return cel<RoleBinding>((filter) => {
    const clauses: CelFilter<RoleBinding>[] = [];

    if (search) {
      clauses.push(
        filter.or(
          filter.field('metadata.name').contains(search),
          filter.field('id').contains(search),
        ),
      );
    }

    if (statuses.length > 0) {
      clauses.push(
        filter.field('status.state').isIn(statuses.map((status) => STATUS_FILTER_TO_STATE[status])),
      );
    }

    if (roleIds.length > 0) {
      clauses.push(filter.field('spec.role.id').isIn([...roleIds]));
    }

    return filter.and(...clauses);
  });
};
