import type { TFunction } from 'i18next';

import { StorageBackendState } from '@osac/types/private';

import { useTranslation } from '../../hooks/useTranslation';
import { ResourceStatusLabel, type StatusLabelProps } from '../Resource/ResourceStatusLabel';

interface StorageBackendStatusLabelProps {
  state?: StorageBackendState;
}

const storageBackendStatusMap = (t: TFunction): Record<StorageBackendState, StatusLabelProps> => ({
  [StorageBackendState.READY]: {
    status: 'ready',
    text: t('Ready'),
  },
  [StorageBackendState.UNSPECIFIED]: {
    status: 'progressing',
    text: t('Pending'),
  },
});

/**
 * Resolve the display props for a storage backend state.
 *
 * UNSPECIFIED (the proto zero-value) means the backend's provider connection
 * has not been validated yet — display it as "Pending", not "Ready".
 * Any unknown / future enum value also falls back to "Pending" so the UI
 * never claims readiness it cannot verify.
 */
const resolveStorageBackendStatus = (
  t: TFunction,
  state: StorageBackendState | undefined,
): StatusLabelProps => {
  const map = storageBackendStatusMap(t);
  if (state !== undefined && state in map) {
    return map[state];
  }
  return map[StorageBackendState.UNSPECIFIED];
};

const StorageBackendStatusLabel = ({ state }: StorageBackendStatusLabelProps) => {
  const { t } = useTranslation();

  return <ResourceStatusLabel {...resolveStorageBackendStatus(t, state)} />;
};

export default StorageBackendStatusLabel;
