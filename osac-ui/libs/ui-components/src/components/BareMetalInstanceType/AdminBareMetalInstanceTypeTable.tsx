import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  Bullseye,
  EmptyState,
  EmptyStateBody,
  EmptyStateVariant,
  MenuToggle,
} from '@patternfly/react-core';
import { EllipsisVIcon } from '@patternfly/react-icons/dist/esm/icons/ellipsis-v-icon';
import SearchIcon from '@patternfly/react-icons/dist/esm/icons/search-icon';
import { ActionsColumn, Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import type { BareMetalInstanceType as PrivateBareMetalInstanceType } from '@osac/types/private';
import ResourceNameField from '@osac/ui-components/components/Resource/ResourceNameField';

import BareMetalInstanceTypeDeleteModal from './BareMetalInstanceTypeDeleteModal';
import { useTranslation } from '../../hooks/useTranslation';

const NAME_PREVIEW_LENGTH = 32;
const NAME_COLUMN_WIDTH = 20;
const CPU_COLUMN_WIDTH = 20;
const MEMORY_COLUMN_WIDTH = 15;
const ACCELERATORS_COLUMN_WIDTH = 15;
const EMPTY_STATE_COLUMN_SPAN = 5;

interface AdminBareMetalInstanceTypeTableProps {
  bareMetalInstanceTypes: PrivateBareMetalInstanceType[];
}

const AdminBareMetalInstanceTypeTable = ({
  bareMetalInstanceTypes,
}: AdminBareMetalInstanceTypeTableProps) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [deleteTarget, setDeleteTarget] = useState<PrivateBareMetalInstanceType>();

  return (
    <>
      {deleteTarget && (
        <BareMetalInstanceTypeDeleteModal
          instanceType={deleteTarget}
          onClose={() => setDeleteTarget(undefined)}
          onSuccess={() => setDeleteTarget(undefined)}
        />
      )}
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
          {bareMetalInstanceTypes.length === 0 ? (
            <Tr>
              <Td colSpan={EMPTY_STATE_COLUMN_SPAN}>
                <Bullseye>
                  <EmptyState
                    headingLevel="h2"
                    titleText={t('No bare metal instance types yet.')}
                    icon={SearchIcon}
                    variant={EmptyStateVariant.sm}
                  >
                    <EmptyStateBody>
                      {t('Bare metal instance types defined for this cloud platform appear here.')}
                    </EmptyStateBody>
                  </EmptyState>
                </Bullseye>
              </Td>
            </Tr>
          ) : (
            bareMetalInstanceTypes.map((bareMetalInstanceType) => {
              const name = bareMetalInstanceType.metadata?.name || bareMetalInstanceType.id;
              const cpu = bareMetalInstanceType.spec?.hardware?.cpu;
              const memory = bareMetalInstanceType.spec?.hardware?.memory;
              const acceleratorCount =
                bareMetalInstanceType.spec?.hardware?.accelerators.length ?? 0;
              return (
                <Tr key={bareMetalInstanceType.id}>
                  <Td dataLabel={t('Name')} modifier="truncate" width={NAME_COLUMN_WIDTH}>
                    <ResourceNameField
                      resource={bareMetalInstanceType}
                      detailsUrl={`/admin/infrastructure/baremetal-instance-types/${bareMetalInstanceType.id}`}
                      maxCharsDisplayed={NAME_PREVIEW_LENGTH}
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
                    <ActionsColumn
                      items={[
                        {
                          title: t('Edit'),
                          onClick: () =>
                            navigate(
                              `/admin/infrastructure/baremetal-instance-types/${bareMetalInstanceType.id}/edit`,
                            ),
                        },
                        {
                          title: t('Delete'),
                          onClick: () => setDeleteTarget(bareMetalInstanceType),
                          isDanger: true,
                        },
                      ]}
                      actionsToggle={({ onToggle, isOpen, toggleRef }) => (
                        <MenuToggle
                          ref={toggleRef}
                          variant="plain"
                          isExpanded={isOpen}
                          onClick={onToggle}
                          aria-label={t('Actions for {{name}}', { name })}
                        >
                          <EllipsisVIcon />
                        </MenuToggle>
                      )}
                    />
                  </Td>
                </Tr>
              );
            })
          )}
        </Tbody>
      </Table>
    </>
  );
};

export default AdminBareMetalInstanceTypeTable;
