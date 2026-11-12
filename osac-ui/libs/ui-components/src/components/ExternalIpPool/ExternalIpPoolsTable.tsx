import { useMemo } from 'react';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import type { ExternalIPPool } from '@osac/types/private';
import { Timestamp } from '@osac/ui-components/components/Primitives/Timestamp';
import ResourceNameField from '@osac/ui-components/components/Resource/ResourceNameField.tsx';

import ExternalIpPoolActionsMenu from './ExternalIpPoolActionsMenu';
import ExternalIpPoolStatusLabel from './ExternalIpPoolStatusLabel';
import { formatExternalIpPoolCidrs, getPoolCapacity, getPoolTenant } from './utils';
import { useTranslation } from '../../hooks/useTranslation';

interface ExternalIpPoolsTableProps {
  pools: ExternalIPPool[];
}

export const ExternalIpPoolsTable = ({ pools }: ExternalIpPoolsTableProps) => {
  const { t } = useTranslation();

  const columnLabels = useMemo(
    () => ({
      name: t('Name'),
      status: t('Status'),
      cidrs: t('CIDRs'),
      tenant: t('Tenant'),
      capacity: t('Capacity'),
      created: t('Created'),
      actions: t('Actions'),
    }),
    [t],
  );

  return (
    <Table aria-label={t('External IP pools')} variant="compact">
      <Thead>
        <Tr>
          <Th>{columnLabels.name}</Th>
          <Th>{columnLabels.status}</Th>
          <Th>{columnLabels.cidrs}</Th>
          <Th>{columnLabels.tenant}</Th>
          <Th>{columnLabels.capacity}</Th>
          <Th>{columnLabels.created}</Th>
          <Th aria-label={columnLabels.actions} />
        </Tr>
      </Thead>
      <Tbody>
        {pools.map((pool) => (
          <Tr key={pool.id}>
            <Td dataLabel={columnLabels.name}>
              <ResourceNameField
                resource={pool}
                detailsUrl={`/admin/infrastructure/external-ip-pools/${encodeURIComponent(pool.id)}`}
              />
            </Td>
            <Td dataLabel={columnLabels.status}>
              <ExternalIpPoolStatusLabel state={pool.status?.state} />
            </Td>
            <Td dataLabel={columnLabels.cidrs}>{formatExternalIpPoolCidrs(t, pool.spec?.cidrs)}</Td>
            <Td dataLabel={columnLabels.tenant}>{getPoolTenant(pool, t)}</Td>
            <Td dataLabel={columnLabels.capacity}>{getPoolCapacity(pool, t)}</Td>
            <Td dataLabel={columnLabels.created}>
              <Timestamp value={pool.metadata?.creationTimestamp} />
            </Td>
            <Td dataLabel={columnLabels.actions} isActionCell>
              <ExternalIpPoolActionsMenu pool={pool} />
            </Td>
          </Tr>
        ))}
      </Tbody>
    </Table>
  );
};
