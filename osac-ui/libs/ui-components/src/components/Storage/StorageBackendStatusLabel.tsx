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
    status: 'unspecified',
    text: t('Unspecified'),
  },
});

/**
 * Resolve the display props for a storage backend state.
 *
 * Known enum values are rendered from the map above. Any value the UI
 * does not recognise — including `undefined` (no status yet) and future
 * enum additions — falls back to a non-ready "Pending" indicator so the
 * UI never claims readiness it cannot verify.
 */
const resolveStorageBackendStatus = (
  t: TFunction,
  state: StorageBackendState | undefined,
): StatusLabelProps => {
  const map = storageBackendStatusMap(t);
  if (state !== undefined && state in map) {
    return map[state];
  }
  return { status: 'progressing', text: t('Pending') };
};

const StorageBackendStatusLabel = ({ state }: StorageBackendStatusLabelProps) => {
  const { t } = useTranslation();

  return <ResourceStatusLabel {...resolveStorageBackendStatus(t, state)} />;
};

export default StorageBackendStatusLabel;
