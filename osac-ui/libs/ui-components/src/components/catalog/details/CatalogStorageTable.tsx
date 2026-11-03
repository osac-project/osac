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
  emptyValue?: ReactNode;
  className?: string;
}

const CatalogStorageTable = ({ rows, emptyValue, className }: CatalogStorageTableProps) => {
  const { t } = useTranslation();

  return (
    <Table aria-label={t('Additional disks')} variant="compact" className={className}>
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
            <Td colSpan={2}>{emptyValue ?? t('Not set')}</Td>
          </Tr>
        )}
      </Tbody>
    </Table>
  );
};

export default CatalogStorageTable;
