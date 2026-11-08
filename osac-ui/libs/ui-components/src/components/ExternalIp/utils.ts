import type { TFunction } from 'i18next';

import { type ExternalIP, ExternalIPState } from '@osac/types';
import { type CelFilter, cel } from '@osac/ui-components/api/cel';
import type {
  ExternalIpAttachedTarget,
  ExternalIpAttachedTargetKind,
} from '@osac/ui-components/api/v1/external-ip-data';

export const STATE_FILTER_VALUES = ['pending', 'allocated', 'inuse', 'failed', 'deleting'] as const;

export type ExternalIpStateFilterValue = (typeof STATE_FILTER_VALUES)[number];

export const STATE_FILTER_PARAM = 'state';

export const isExternalIpStateFilterValue = (value: string): value is ExternalIpStateFilterValue =>
  STATE_FILTER_VALUES.includes(value as ExternalIpStateFilterValue);

export const EXTERNAL_IP_STATE_FILTER_TO_ENUM: Record<
  ExternalIpStateFilterValue,
  ExternalIPState | 'inuse'
> = {
  pending: ExternalIPState.EXTERNAL_IP_STATE_PENDING,
  allocated: ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED,
  failed: ExternalIPState.EXTERNAL_IP_STATE_FAILED,
  deleting: ExternalIPState.EXTERNAL_IP_STATE_DELETING,
  inuse: 'inuse',
};

export const getExternalIpStateFilterLabels = (
  t: TFunction,
): Record<ExternalIPState | 'inuse', string> => ({
  [ExternalIPState.EXTERNAL_IP_STATE_UNSPECIFIED]: t('Unknown'),
  [ExternalIPState.EXTERNAL_IP_STATE_PENDING]: t('Provisioning'),
  [ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED]: t('Allocated'),
  inuse: t('In use'),
  [ExternalIPState.EXTERNAL_IP_STATE_FAILED]: t('Failed'),
  [ExternalIPState.EXTERNAL_IP_STATE_DELETING]: t('Deleting'),
});

export const buildExternalIpListFilter = (
  projectFilter: CelFilter<ExternalIP> | undefined,
  stateFilters: (ExternalIPState | 'inuse')[],
  search: string,
): CelFilter<ExternalIP> | undefined =>
  cel<ExternalIP>((b) =>
    b.and(
      projectFilter,
      stateFilters.length > 0
        ? b.or(
            ...stateFilters.map((stateFilter) => {
              if (stateFilter === 'inuse') {
                return b.and(
                  b.field('status.state').equals(ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED),
                  b.field('status.attached').equals(true),
                );
              } else if (stateFilter === ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED) {
                return b.and(
                  b.field('status.state').equals(ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED),
                  b.field('status.attached').notEquals(true),
                );
              } else {
                return b.field('status.state').equals(stateFilter);
              }
            }),
          )
        : undefined,
      search
        ? b.or(
            b.field('metadata.name').contains(search),
            b.field('status.address').contains(search),
          )
        : undefined,
    ),
  );

export const attachedTargetKindLabel = (
  t: TFunction,
  kind: ExternalIpAttachedTargetKind,
): string => {
  switch (kind) {
    case 'baremetalInstance':
      return t('Bare-metal instance');
    case 'cluster':
      return t('Cluster');
    case 'natGateway':
      return t('NAT gateway');
    case 'computeInstance':
      return t('Virtual machine');
  }
};

/**
 * Why an ExternalIP cannot be deleted yet. `consumer` is the NATGateway or
 * ExternalIPAttachment that references it; `status.attached` misses NATGateway consumers.
 */
export const deleteDisabledReason = (
  externalIp: ExternalIP,
  t: TFunction,
  consumer?: ExternalIpAttachedTarget,
): string | undefined => {
  if (externalIp.status?.state === ExternalIPState.EXTERNAL_IP_STATE_DELETING) {
    return t('This external IP is being deleted.');
  }
  if (consumer) {
    // A NAT gateway keeps its external IP for life; an attachment is deleted on its own.
    return consumer.kind === 'natGateway'
      ? t('Delete NAT gateway {{name}} first.', { name: consumer.name })
      : t('Delete its attachment to {{kind}} {{name}} first.', {
          kind: attachedTargetKindLabel(t, consumer.kind).toLowerCase(),
          name: consumer.name,
        });
  }
  return externalIp.status?.attached === true
    ? t('Detach this external IP before deleting it.')
    : undefined;
};
