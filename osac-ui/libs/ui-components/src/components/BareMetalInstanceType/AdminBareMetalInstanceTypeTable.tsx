import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import type { BareMetalInstanceType as PrivateBareMetalInstanceType } from '@osac/types/private';
import ResourceNameField from '@osac/ui-components/components/Resource/ResourceNameField';

import AdminBareMetalInstanceTypeActionsMenu from './AdminBareMetalInstanceTypeActionsMenu';
import { useTranslation } from '../../hooks/useTranslation';

const NAME_PREVIEW_LENGTH = 32;
const NAME_COLUMN_WIDTH = 20;
const CPU_COLUMN_WIDTH = 20;
const MEMORY_COLUMN_WIDTH = 15;
const ACCELERATORS_COLUMN_WIDTH = 15;

interface AdminBareMetalInstanceTypeTableProps {
  bareMetalInstanceTypes: PrivateBareMetalInstanceType[];
}

const AdminBareMetalInstanceTypeTable = ({
  bareMetalInstanceTypes,
}: AdminBareMetalInstanceTypeTableProps) => {
  const { t } = useTranslation();

  return (
    <Table aria-label={t('Bare metal instance types')} variant="compact">
      <Thead>
        <Tr>
          <Th width={NAME_COLUMN_WIDTH}>{t('Name')}</Th>
          <Th width={CPU_COLUMN_WIDTH}>{t('CPU')}</Th>
          <Th width={MEMORY_COLUMN_WIDTH}>{t('Memory (GiB)')}</Th>
          <Th width={ACCELERATORS_COLUMN_WIDTH}>{t('Accelerators')}</Th>
          <Th aria-label={t('Actions')} />
        </Tr>
      </Thead>
      <Tbody>
        {bareMetalInstanceTypes.map((bareMetalInstanceType) => {
          const cpu = bareMetalInstanceType.spec?.hardware?.cpu;
          const memory = bareMetalInstanceType.spec?.hardware?.memory;
          const acceleratorCount = bareMetalInstanceType.spec?.hardware?.accelerators.length ?? 0;
          return (
            <Tr key={bareMetalInstanceType.id}>
              <Td dataLabel={t('Name')} modifier="truncate" width={NAME_COLUMN_WIDTH}>
                <ResourceNameField
                  resource={bareMetalInstanceType}
                  detailsUrl={`/admin/infrastructure/baremetal-instance-types/${bareMetalInstanceType.id}`}
                  maxCharsDisplayed={NAME_PREVIEW_LENGTH}
                  subTitle={bareMetalInstanceType.spec?.description}
                />
              </Td>
              <Td dataLabel={t('CPU')} width={CPU_COLUMN_WIDTH}>
                {cpu ? `${cpu.cores} (${cpu.architecture})` : '—'}
              </Td>
              <Td dataLabel={t('Memory (GiB)')} width={MEMORY_COLUMN_WIDTH}>
                {memory ? memory.totalGb.toString() : '—'}
              </Td>
              <Td dataLabel={t('Accelerators')} width={ACCELERATORS_COLUMN_WIDTH}>
                {acceleratorCount > 0 ? acceleratorCount : '—'}
              </Td>
              <Td dataLabel={t('Actions')} isActionCell>
                <AdminBareMetalInstanceTypeActionsMenu
                  bareMetalInstanceType={bareMetalInstanceType}
                />
              </Td>
            </Tr>
          );
        })}
      </Tbody>
    </Table>
  );
};

export default AdminBareMetalInstanceTypeTable;
