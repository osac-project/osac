import type { TFunction } from 'i18next';

import { ServiceAccount, ServiceAccountState } from '@osac/types';

import {
  ResourceStatusLabel,
  StatusLabelProps,
} from '../../components/Resource/ResourceStatusLabel';
import { useTranslation } from '../../hooks/useTranslation';

interface ServiceAccountStatusLabelProps {
  serviceAccount: ServiceAccount;
}

const serviceAccountStatusMap = (t: TFunction): Record<ServiceAccountState, StatusLabelProps> => ({
  [ServiceAccountState.READY]: {
    status: 'ready',
    text: t('Ready'),
  },
  [ServiceAccountState.FAILED]: {
    status: 'failed',
    text: t('Failed'),
  },
  [ServiceAccountState.PENDING]: {
    status: 'progressing',
    text: t('Pending'),
  },
  [ServiceAccountState.UNSPECIFIED]: {
    status: 'unspecified',
    text: t('Unspecified'),
  },
});

const ServiceAccountStatusLabel = ({ serviceAccount }: ServiceAccountStatusLabelProps) => {
  const { t } = useTranslation();

  if (serviceAccount.metadata?.deletionTimestamp) {
    return <ResourceStatusLabel status="progressing" text={t('Deleting')} />;
  }

  const statusMap = serviceAccountStatusMap(t);
  const state = serviceAccount.status?.state;
  const status =
    state !== undefined ? statusMap[state] : statusMap[ServiceAccountState.UNSPECIFIED];

  return <ResourceStatusLabel {...(status ?? statusMap[ServiceAccountState.UNSPECIFIED])} />;
};

export default ServiceAccountStatusLabel;
