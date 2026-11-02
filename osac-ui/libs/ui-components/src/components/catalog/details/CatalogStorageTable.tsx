import type { ReactNode } from 'react';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import { useTranslation } from '../../../hooks/useTranslation';

export interface CatalogStorageTableRow {
  id: string;
  size: ReactNode;
  storageTier: ReactNode;
}

interface CatalogStorageTableProps {
  rows: CatalogStorageTableRow[];
}

const CatalogStorageTable = ({ rows }: CatalogStorageTableProps) => {
  const { t } = useTranslation();

  return (
    <Table aria-label={t('Additional disks')} variant="compact">
      <Thead>
        <Tr>
          <Th>{t('Size')}</Th>
          <Th>{t('Storage tier')}</Th>
        </Tr>
      </Thead>
      <Tbody>
        {rows.length ? (
          rows.map((row) => (
            <Tr key={row.id}>
              <Td dataLabel={t('Size')}>{row.size}</Td>
              <Td dataLabel={t('Storage tier')}>{row.storageTier}</Td>
            </Tr>
          ))
        ) : (
          <Tr>
            <Td colSpan={2}>{t('Not set')}</Td>
          </Tr>
        )}
      </Tbody>
    </Table>
  );
};

export default CatalogStorageTable;
