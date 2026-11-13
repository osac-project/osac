import { useMemo } from 'react';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import type { StorageBackend } from '@osac/types/private';
import { Timestamp } from '@osac/ui-components/components/Primitives/Timestamp';
import ResourceNameField from '@osac/ui-components/components/Resource/ResourceNameField.tsx';

import StorageBackendActionsMenu from './StorageBackendActionsMenu';
import StorageBackendStatusLabel from './StorageBackendStatusLabel';
import { storageBackendDetailsPath } from './utils';
import { useTranslation } from '../../hooks/useTranslation';
import { getStorageBackendProviderLabel } from '../../utils/storageBackendProvider';

interface StorageBackendsTableProps {
  backends: StorageBackend[];
}

export const StorageBackendsTable = ({ backends }: StorageBackendsTableProps) => {
  const { t } = useTranslation();

  const columnLabels = useMemo(
    () => ({
      name: t('Name'),
      status: t('Status'),
      provider: t('Provider'),
      endpoint: t('Endpoint'),
      created: t('Created'),
      actions: t('Actions'),
    }),
    [t],
  );

  return (
    <Table aria-label={t('Storage backends')} variant="compact">
      <Thead>
        <Tr>
          <Th>{columnLabels.name}</Th>
          <Th>{columnLabels.status}</Th>
          <Th>{columnLabels.provider}</Th>
          <Th>{columnLabels.endpoint}</Th>
          <Th>{columnLabels.created}</Th>
          <Th aria-label={columnLabels.actions} />
        </Tr>
      </Thead>
      <Tbody>
        {backends.map((backend) => (
          <Tr key={backend.id}>
            <Td dataLabel={columnLabels.name}>
              <ResourceNameField
                resource={backend}
                detailsUrl={storageBackendDetailsPath(backend.id)}
              />
            </Td>
            <Td dataLabel={columnLabels.status}>
              <StorageBackendStatusLabel state={backend.status?.state} />
            </Td>
            <Td dataLabel={columnLabels.provider}>
              {getStorageBackendProviderLabel(backend.spec?.provider ?? '', t)}
            </Td>
            <Td dataLabel={columnLabels.endpoint}>{backend.spec?.endpoint}</Td>
            <Td dataLabel={columnLabels.created}>
              <Timestamp value={backend.metadata?.creationTimestamp} />
            </Td>
            <Td dataLabel={columnLabels.actions} isActionCell>
              <StorageBackendActionsMenu backend={backend} />
            </Td>
          </Tr>
        ))}
      </Tbody>
    </Table>
  );
};
