import { useMemo } from 'react';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import type { ComputeInstance, InstanceType } from '@osac/types';
import { ComputeInstanceState } from '@osac/types';
import ResourceNameField from '@osac/ui-components/components/Resource/ResourceNameField.tsx';

import { VmActionsMenu } from './VmActionsMenu';
import { VmInstanceTypeLabel } from './VmInstanceTypeLabel';
import { useTranslation } from '../../hooks/useTranslation';
import { VmStatusLabel } from '../../VmStatusLabel';
import { Timestamp } from '../Primitives/Timestamp';

interface VmTableProps {
  /** Adds a Tenant column, for cloud provider admins who see every tenant's workloads. */
  showTenant?: boolean;
  vms: ComputeInstance[];
  instanceTypes?: InstanceType[];
  isInstanceTypesLoading?: boolean;
}

export const VmTable = ({
  vms,
  instanceTypes = [],
  isInstanceTypesLoading = false,
  showTenant = false,
}: VmTableProps) => {
  const { t } = useTranslation();

  const instanceTypeById = useMemo(
    () => new Map(instanceTypes.map((item) => [item.id, item])),
    [instanceTypes],
  );

  return (
    <Table aria-label={t('Virtual machines')} variant="compact">
      <Thead>
        <Tr>
          <Th>{t('Name')}</Th>
          {showTenant ? <Th>{t('Tenant')}</Th> : null}
          <Th>{t('Status')}</Th>
          <Th>{t('Instance type')}</Th>
          <Th>{t('Internal IP')}</Th>
          <Th>{t('External IP')}</Th>
          <Th>{t('Created')}</Th>
          <Th aria-label={t('Actions')} />
        </Tr>
      </Thead>
      <Tbody>
        {vms.map((vm) => {
          const state = vm.status?.state;
          const locked = state === ComputeInstanceState.DELETING;
          const instanceTypeId = vm.spec?.instanceType?.id;
          const instanceType = instanceTypeId ? instanceTypeById.get(instanceTypeId) : undefined;
          const internalIp = vm.status?.internalIpAddress;
          const externalIp = vm.status?.externalIpAddress;

          return (
            <Tr key={vm.id}>
              <Td dataLabel={t('Name')}>
                <ResourceNameField
                  resource={vm}
                  detailsUrl={locked ? undefined : `/vms/${encodeURIComponent(vm.id)}`}
                />
              </Td>
              {showTenant ? <Td dataLabel={t('Tenant')}>{vm.metadata?.tenant || '—'}</Td> : null}
              <Td dataLabel={t('Status')}>
                <VmStatusLabel state={state} />
              </Td>
              <Td dataLabel={t('Instance type')}>
                <VmInstanceTypeLabel
                  instanceTypeId={instanceTypeId}
                  instanceType={instanceType}
                  isLoading={isInstanceTypesLoading}
                />
              </Td>
              <Td dataLabel={t('Internal IP')}>{locked ? '—' : internalIp || '—'}</Td>
              <Td dataLabel={t('External IP')}>{locked ? '—' : externalIp || '—'}</Td>
              <Td dataLabel={t('Created')}>
                <Timestamp value={vm.metadata?.creationTimestamp} />
              </Td>
              <Td dataLabel={t('Actions')} isActionCell>
                {locked ? null : <VmActionsMenu vm={vm} />}
              </Td>
            </Tr>
          );
        })}
      </Tbody>
    </Table>
  );
};
