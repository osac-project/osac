import { Bullseye, EmptyState, EmptyStateBody, EmptyStateVariant } from '@patternfly/react-core';
import SearchIcon from '@patternfly/react-icons/dist/esm/icons/search-icon';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import type { Volume } from '@osac/types';
import ResourceNameField from '@osac/ui-components/components/Resource/ResourceNameField.tsx';

import { VolumeAccessModeLabel } from './VolumeAccessModeLabel';
import VolumeActionsMenu from './VolumeActionsMenu';
import { VolumeStatusLabel } from './VolumeStatusLabel';
import { useTranslation } from '../../hooks/useTranslation';
import { Timestamp } from '../Primitives/Timestamp';

const EMPTY_STATE_COLUMN_SPAN = 7;

interface VolumeTableProps {
  volumes: Volume[];
}

const formatSizeGib = (sizeGib: bigint | undefined): string => {
  if (sizeGib === undefined) {
    return '—';
  }
  return `${Number(sizeGib)} GiB`;
};

export const VolumeTable = ({ volumes }: VolumeTableProps) => {
  const { t } = useTranslation();

  return (
    <Table aria-label={t('Volumes')} variant="compact">
      <Thead>
        <Tr>
          <Th>{t('Name')}</Th>
          <Th>{t('State')}</Th>
          <Th>{t('Storage Tier')}</Th>
          <Th>{t('Size')}</Th>
          <Th>{t('Access Mode')}</Th>
          <Th>{t('Created')}</Th>
          <Th aria-label={t('Actions')} />
        </Tr>
      </Thead>
      <Tbody>
        {volumes.length === 0 ? (
          <Tr>
            <Td colSpan={EMPTY_STATE_COLUMN_SPAN}>
              <Bullseye>
                <EmptyState
                  headingLevel="h2"
                  titleText={t('No volumes found')}
                  icon={SearchIcon}
                  variant={EmptyStateVariant.sm}
                >
                  <EmptyStateBody>{t('Create a volume or adjust your filters.')}</EmptyStateBody>
                </EmptyState>
              </Bullseye>
            </Td>
          </Tr>
        ) : (
          volumes.map((volume) => (
            <Tr key={volume.id}>
              <Td dataLabel={t('Name')}>
                <ResourceNameField resource={volume} detailsUrl={`/storage/volumes/${volume.id}`} />
              </Td>
              <Td dataLabel={t('State')}>
                <VolumeStatusLabel state={volume.status?.state} />
              </Td>
              <Td dataLabel={t('Storage Tier')}>{volume.spec?.storageTier ?? '—'}</Td>
              <Td dataLabel={t('Size')}>{formatSizeGib(volume.spec?.sizeGib)}</Td>
              <Td dataLabel={t('Access Mode')}>
                <VolumeAccessModeLabel accessMode={volume.spec?.accessMode} />
              </Td>
              <Td dataLabel={t('Created')}>
                <Timestamp value={volume.metadata?.creationTimestamp} />
              </Td>
              <Td dataLabel={t('Actions')} isActionCell>
                <VolumeActionsMenu volume={volume} />
              </Td>
            </Tr>
          ))
        )}
      </Tbody>
    </Table>
  );
};
