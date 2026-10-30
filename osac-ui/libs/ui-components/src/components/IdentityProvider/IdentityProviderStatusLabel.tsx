import type { TFunction } from 'i18next';

import { IdentityProvider, IdentityProviderPhase } from '@osac/types';

import {
  ResourceStatusLabel,
  StatusLabelProps,
} from '../../components/Resource/ResourceStatusLabel';
import { useTranslation } from '../../hooks/useTranslation';

interface IdentityProviderStatusLabelProps {
  idp: IdentityProvider;
}

const identityProviderPhaseMap = (
  t: TFunction,
): Record<IdentityProviderPhase, StatusLabelProps> => ({
  [IdentityProviderPhase.READY]: {
    status: 'ready',
    text: t('Ready'),
  },
  [IdentityProviderPhase.ERROR]: {
    status: 'failed',
    text: t('Error'),
  },
  [IdentityProviderPhase.UNKNOWN]: {
    status: 'unspecified',
    text: t('Unknown'),
  },
  [IdentityProviderPhase.UNSPECIFIED]: {
    status: 'unspecified',
    text: t('Unspecified'),
  },
});

const IdentityProviderStatusLabel = ({ idp }: IdentityProviderStatusLabelProps) => {
  const { t } = useTranslation();

  const phaseMap = identityProviderPhaseMap(t);

  const status =
    idp.status?.phase !== undefined
      ? phaseMap[idp.status.phase]
      : phaseMap[IdentityProviderPhase.UNSPECIFIED];

  return <ResourceStatusLabel {...status} />;
};

export default IdentityProviderStatusLabel;
