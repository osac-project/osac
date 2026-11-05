import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import type { StorageBackend } from '@osac/types/private';
import ResourceNameField from '@osac/ui-components/components/Resource/ResourceNameField.tsx';

import StorageBackendActionsMenu from './StorageBackendActionsMenu';
import StorageBackendStatusLabel from './StorageBackendStatusLabel';
import { useTranslation } from '../../hooks/useTranslation';
import { getStorageBackendProviderLabel } from '../../utils/storageBackendProvider';

interface StorageBackendsTableProps {
  backends: StorageBackend[];
}

export const StorageBackendsTable = ({ backends }: StorageBackendsTableProps) => {
  const { t } = useTranslation();

  return (
    <Table aria-label={t('Storage backends')} variant="compact">
      <Thead>
        <Tr>
          <Th>{t('Name')}</Th>
          <Th>{t('Status')}</Th>
          <Th>{t('Provider')}</Th>
          <Th>{t('Endpoint')}</Th>
          <Th aria-label={t('Actions')} />
        </Tr>
      </Thead>
      <Tbody>
        {backends.map((backend) => (
          <Tr key={backend.id}>
            <Td dataLabel={t('Name')}>
              <ResourceNameField
                resource={backend}
                detailsUrl={`/admin/infrastructure/storage/backends/${encodeURIComponent(backend.id)}`}
              />
            </Td>
            <Td dataLabel={t('Status')}>
              <StorageBackendStatusLabel state={backend.status?.state} />
            </Td>
            <Td dataLabel={t('Provider')}>
              {getStorageBackendProviderLabel(backend.spec?.provider ?? '', t)}
            </Td>
            <Td dataLabel={t('Endpoint')}>{backend.spec?.endpoint}</Td>
            <Td dataLabel={t('Actions')} isActionCell>
              <StorageBackendActionsMenu backend={backend} />
            </Td>
          </Tr>
        ))}
      </Tbody>
    </Table>
  );
};
