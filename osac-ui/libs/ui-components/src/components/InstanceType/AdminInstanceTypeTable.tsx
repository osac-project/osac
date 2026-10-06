import { Bullseye, EmptyState, EmptyStateBody, EmptyStateVariant } from '@patternfly/react-core';
import SearchIcon from '@patternfly/react-icons/dist/esm/icons/search-icon';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import type { InstanceType as PrivateInstanceType } from '@osac/types/private';
import ResourceNameField from '@osac/ui-components/components/Resource/ResourceNameField.tsx';

import AdminInstanceTypeActionsMenu from './AdminInstanceTypeActionsMenu';
import InstanceTypeLifecycleLabel from './InstanceTypeLifecycleLabel';
import { useTranslation } from '../../hooks/useTranslation';
import { Timestamp } from '../Primitives/Timestamp';

const INSTANCE_TYPE_NAME_PREVIEW_LENGTH = 32;
const NAME_COLUMN_WIDTH = 15;
const LIFECYCLE_STATE_COLUMN_WIDTH = 15;
const CPU_CORES_COLUMN_WIDTH = 15;
const MEMORY_COLUMN_WIDTH = 15;
const GPU_COLUMN_WIDTH = 15;
const CREATED_COLUMN_WIDTH = 15;
const EMPTY_STATE_COLUMN_SPAN = 7;

interface AdminInstanceTypeTableProps {
  instanceTypes: PrivateInstanceType[];
}

const AdminInstanceTypeTable = ({ instanceTypes }: AdminInstanceTypeTableProps) => {
  const { t } = useTranslation();

  return (
    <Table aria-label={t('Instance types')} variant="compact">
      <Thead>
        <Tr>
          <Th width={NAME_COLUMN_WIDTH}>{t('Name')}</Th>
          <Th width={LIFECYCLE_STATE_COLUMN_WIDTH}>{t('Lifecycle state')}</Th>
          <Th width={CPU_CORES_COLUMN_WIDTH}>{t('vCPUs')}</Th>
          <Th width={MEMORY_COLUMN_WIDTH}>{t('Memory (GiB)')}</Th>
          <Th width={GPU_COLUMN_WIDTH}>{t('GPUs')}</Th>
          <Th width={CREATED_COLUMN_WIDTH}>{t('Created')}</Th>
          <Th aria-label={t('Actions')} />
        </Tr>
      </Thead>
      <Tbody>
        {instanceTypes.length === 0 ? (
          <Tr>
            <Td colSpan={EMPTY_STATE_COLUMN_SPAN}>
              <Bullseye>
                <EmptyState
                  headingLevel="h2"
                  titleText={t('No instance types yet.')}
                  icon={SearchIcon}
                  variant={EmptyStateVariant.sm}
                >
                  <EmptyStateBody>
                    {t('Create an instance type to start defining provider-managed sizes.')}
                  </EmptyStateBody>
                </EmptyState>
              </Bullseye>
            </Td>
          </Tr>
        ) : (
          instanceTypes.map((instanceType) => {
            const gpu = instanceType.spec?.gpu;
            return (
              <Tr key={instanceType.id}>
                <Td dataLabel={t('Name')} modifier="truncate" width={NAME_COLUMN_WIDTH}>
                  <ResourceNameField
                    resource={instanceType}
                    detailsUrl={`/admin/infrastructure/instance-types/${instanceType.id}`}
                    maxCharsDisplayed={INSTANCE_TYPE_NAME_PREVIEW_LENGTH}
                  />
                </Td>
                <Td dataLabel={t('Lifecycle state')} width={LIFECYCLE_STATE_COLUMN_WIDTH}>
                  <InstanceTypeLifecycleLabel state={instanceType.spec?.state} />
                </Td>
                <Td dataLabel={t('vCPUs')} width={CPU_CORES_COLUMN_WIDTH}>
                  {instanceType.spec?.vcpus ?? '—'}
                </Td>
                <Td dataLabel={t('Memory (GiB)')} width={MEMORY_COLUMN_WIDTH}>
                  {instanceType.spec?.memoryGib ?? '—'}
                </Td>
                <Td dataLabel={t('GPUs')} width={GPU_COLUMN_WIDTH}>
                  {gpu?.count ?? '—'}
                </Td>
                <Td dataLabel={t('Created')} width={CREATED_COLUMN_WIDTH}>
                  <Timestamp value={instanceType.metadata?.creationTimestamp} />
                </Td>
                <Td dataLabel={t('Actions')} isActionCell>
                  <AdminInstanceTypeActionsMenu instanceType={instanceType} />
                </Td>
              </Tr>
            );
          })
        )}
      </Tbody>
    </Table>
  );
};

export default AdminInstanceTypeTable;
