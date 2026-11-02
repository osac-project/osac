import { useMemo } from 'react';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import type { Volume } from '@osac/types';
import ResourceNameField from '@osac/ui-components/components/Resource/ResourceNameField.tsx';

import { formatSizeGib } from './utils';
import { VolumeAccessModeLabel } from './VolumeAccessModeLabel';
import VolumeActionsMenu from './VolumeActionsMenu';
import { VolumeStatusLabel } from './VolumeStatusLabel';
import { useTranslation } from '../../hooks/useTranslation';
import { Timestamp } from '../Primitives/Timestamp';

interface VolumeTableProps {
  volumes: Volume[];
}

export const VolumeTable = ({ volumes }: VolumeTableProps) => {
  const { t } = useTranslation();

  const columnLabels = useMemo(
    () => ({
      name: t('Name'),
      state: t('State'),
      project: t('Project'),
      storageTier: t('Storage Tier'),
      size: t('Size'),
      accessMode: t('Access Mode'),
      created: t('Created'),
      actions: t('Actions'),
    }),
    [t],
  );

  return (
    <Table aria-label={t('Volumes')} variant="compact">
      <Thead>
        <Tr>
          <Th>{columnLabels.name}</Th>
          <Th>{columnLabels.state}</Th>
          <Th>{columnLabels.project}</Th>
          <Th>{columnLabels.storageTier}</Th>
          <Th>{columnLabels.size}</Th>
          <Th>{columnLabels.accessMode}</Th>
          <Th>{columnLabels.created}</Th>
          <Th aria-label={columnLabels.actions} />
        </Tr>
      </Thead>
      <Tbody>
        {volumes.map((volume) => (
          <Tr key={volume.id}>
            <Td dataLabel={columnLabels.name}>
              <ResourceNameField resource={volume} detailsUrl={`/storage/volumes/${volume.id}`} />
            </Td>
            <Td dataLabel={columnLabels.state}>
              <VolumeStatusLabel state={volume.status?.state} />
            </Td>
            <Td dataLabel={columnLabels.project}>{volume.metadata?.project || t('Default')}</Td>
            <Td dataLabel={columnLabels.storageTier}>{volume.spec?.storageTier ?? '—'}</Td>
            <Td dataLabel={columnLabels.size}>{formatSizeGib(volume.spec?.sizeGib)}</Td>
            <Td dataLabel={columnLabels.accessMode}>
              <VolumeAccessModeLabel accessMode={volume.spec?.accessMode} />
            </Td>
            <Td dataLabel={columnLabels.created}>
              <Timestamp value={volume.metadata?.creationTimestamp} />
            </Td>
            <Td dataLabel={columnLabels.actions} isActionCell>
              <VolumeActionsMenu volume={volume} />
            </Td>
          </Tr>
        ))}
      </Tbody>
    </Table>
  );
};
