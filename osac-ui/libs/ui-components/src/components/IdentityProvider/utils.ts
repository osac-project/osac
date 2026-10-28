import type { TFunction } from 'i18next';

import { type IdentityProvider, IdentityProviderPhase } from '@osac/types';
import { type CelFilter, cel } from '@osac/ui-components/api/cel';

export const getIdpName = (idp: IdentityProvider) =>
  idp.spec?.title || idp.metadata?.name || idp.id;

export const getIdpIssuerUrl = (idp: IdentityProvider): string | undefined =>
  idp.spec?.config.value?.issuer?.trim() || undefined;

export const ENABLED_FILTER_PARAM = 'enabled';

export const PHASE_FILTER_VALUES = ['ready', 'error', 'unknown'] as const;

export type IdentityProviderPhaseFilterValue = (typeof PHASE_FILTER_VALUES)[number];

export const PHASE_FILTER_PARAM = 'state';

export const isIdentityProviderPhaseFilterValue = (
  value: string,
): value is IdentityProviderPhaseFilterValue =>
  PHASE_FILTER_VALUES.includes(value as IdentityProviderPhaseFilterValue);

export const IDENTITY_PROVIDER_PHASE_FILTER_TO_ENUM: Record<
  IdentityProviderPhaseFilterValue,
  IdentityProviderPhase
> = {
  ready: IdentityProviderPhase.READY,
  error: IdentityProviderPhase.ERROR,
  unknown: IdentityProviderPhase.UNKNOWN,
};

export const resolveIdpType = (t: TFunction, configCase: string | undefined): string => {
  switch (configCase) {
    case 'oidc':
      return t('OpenID Connect (OIDC)');
    default:
      return configCase ?? '-';
  }
};

export const getIdentityProviderPhaseLabels = (
  t: TFunction,
): Record<IdentityProviderPhase, string> => ({
  [IdentityProviderPhase.UNSPECIFIED]: t('Unspecified'),
  [IdentityProviderPhase.READY]: t('Ready'),
  [IdentityProviderPhase.ERROR]: t('Error'),
  [IdentityProviderPhase.UNKNOWN]: t('Unknown'),
});

export const buildIdentityProviderListFilter = (
  phaseFilters: IdentityProviderPhase[],
  enabledFilter: boolean | undefined,
  titleSearch: string,
): CelFilter<IdentityProvider> | undefined =>
  cel<IdentityProvider>((b) =>
    b.and(
      phaseFilters.length > 0 ? b.field('status.phase').isIn(phaseFilters) : undefined,
      enabledFilter !== undefined ? b.field('spec.enabled').equals(enabledFilter) : undefined,
      titleSearch ? b.field('spec.title').contains(titleSearch) : undefined,
    ),
  );
