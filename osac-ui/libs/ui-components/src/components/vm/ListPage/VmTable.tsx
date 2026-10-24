import { useMemo } from 'react';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import type { ComputeInstance } from '@osac/types';
import { ComputeInstanceState } from '@osac/types';
import ResourceNameField from '@osac/ui-components/components/Resource/ResourceNameField.tsx';
import VmActionButtons from '@osac/ui-components/components/vm/actions/VmActionButtons';

import { useTranslation } from '../../../hooks/useTranslation';
import { VmStatusLabel } from '../../../VmStatusLabel';
import { Timestamp } from '../../Primitives/Timestamp';

interface VmTableProps {
  vms: ComputeInstance[];
}

export const VmTable = ({ vms }: VmTableProps) => {
  const { t } = useTranslation();

  const columnLabels = useMemo(
    () => ({
      name: t('Name'),
      status: t('Status'),
      project: t('Project'),
      instanceType: t('Instance type'),
      diskImage: t('Disk image'),
      externalIP: t('External IP'),
      created: t('Created'),
      actions: t('Actions'),
    }),
    [t],
  );
  return (
    <Table aria-label={t('Virtual machines')} variant="compact">
      <Thead>
        <Tr>
          <Th>{columnLabels.name}</Th>
          <Th>{columnLabels.status}</Th>
          <Th>{columnLabels.project}</Th>
          <Th>{columnLabels.instanceType}</Th>
          <Th>{columnLabels.diskImage}</Th>
          <Th>{columnLabels.externalIP}</Th>
          <Th>{columnLabels.created}</Th>
          <Th>{columnLabels.actions}</Th>
        </Tr>
      </Thead>
      <Tbody>
        {vms.map((vm) => {
          const state = vm.status?.state;
          const locked = state === ComputeInstanceState.DELETING;

          return (
            <Tr key={vm.id}>
              <Td dataLabel={columnLabels.name}>
                <ResourceNameField
                  resource={vm}
                  detailsUrl={locked ? undefined : `/vms/${encodeURIComponent(vm.id)}`}
                />
              </Td>
              <Td dataLabel={columnLabels.status}>
                <VmStatusLabel state={state} />
              </Td>
              <Td dataLabel={columnLabels.project}>{vm.metadata?.project || t('Default')}</Td>
              <Td dataLabel={columnLabels.instanceType}>{vm.spec?.instanceType?.name || '-'}</Td>
              <Td dataLabel={columnLabels.diskImage}>
                {locked ? '—' : vm.spec?.diskImage?.name || '—'}
              </Td>
              <Td dataLabel={columnLabels.externalIP}>
                {locked ? '—' : vm.status?.externalIpAddress || '—'}
              </Td>
              <Td dataLabel={columnLabels.created}>
                <Timestamp value={vm.metadata?.creationTimestamp} />
              </Td>
              <Td dataLabel={columnLabels.actions}>
                {locked ? null : <VmActionButtons vm={vm} isMenu />}
              </Td>
            </Tr>
          );
        })}
      </Tbody>
    </Table>
  );
};
