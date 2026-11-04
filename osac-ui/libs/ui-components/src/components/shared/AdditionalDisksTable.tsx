import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import type { ComputeInstanceDisk } from '@osac/types';

import { useTranslation } from '../../hooks/useTranslation';

interface AdditionalDisksTableProps {
  disks: ComputeInstanceDisk[];
  className?: string;
}

const AdditionalDisksTable = ({ disks, className }: AdditionalDisksTableProps) => {
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
        {disks.length ? (
          disks.map((disk, index) => (
            <Tr key={`additional-disk-${index}`}>
              <Td dataLabel={t('Size')}>
                {disk.sizeGib === undefined ? t('Not set') : `${disk.sizeGib} GiB`}
              </Td>
              <Td dataLabel={t('Storage tier')}>
                {disk.storageTier?.name || disk.storageTier?.id || t('Not set')}
              </Td>
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

export default AdditionalDisksTable;
