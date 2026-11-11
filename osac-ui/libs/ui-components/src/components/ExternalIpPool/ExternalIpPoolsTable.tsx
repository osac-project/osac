import { Flex, FlexItem, Label } from '@patternfly/react-core';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import type { ExternalIPPool } from '@osac/types/private';

import ExternalIpPoolActionsMenu from './ExternalIpPoolActionsMenu';
import ExternalIpPoolStatusLabel from './ExternalIpPoolStatusLabel';
import { poolCapacity } from './poolCapacity';
import { useTranslation } from '../../hooks/useTranslation';
import NetworkingResourceName from '../networking/NetworkingResourceName';
import StatusWithMessage from '../networking/StatusWithMessage';

interface ExternalIpPoolsTableProps {
  pools: ExternalIPPool[];
}

/** "12 of 16 (75%)"; a pool with addresses but none left also gets a warning Label. */
const PoolAvailability = ({ pool }: { pool: ExternalIPPool }) => {
  const { t } = useTranslation();
  const capacity = poolCapacity(pool);
  if (!capacity) {
    return <>—</>;
  }
  const text = t('{{available}} of {{total}} ({{percent}}%)', {
    available: `${capacity.available}`,
    total: `${capacity.total}`,
    percent: capacity.availablePercent,
  });
  if (capacity.available > 0n) {
    return <>{text}</>;
  }
  return (
    <Flex spaceItems={{ default: 'spaceItemsSm' }} flexWrap={{ default: 'nowrap' }}>
      <FlexItem>{text}</FlexItem>
      <FlexItem>
        <Label status="warning" isCompact>
          {t('None available')}
        </Label>
      </FlexItem>
    </Flex>
  );
};

export const ExternalIpPoolsTable = ({ pools }: ExternalIpPoolsTableProps) => {
  const { t } = useTranslation();

  const formatCidrs = (cidrs?: string[]): string =>
    cidrs && cidrs.length > 0 ? cidrs.join(', ') : '—';

  return (
    <Table aria-label={t('External IP pools')} variant="compact">
      <Thead>
        <Tr>
          <Th>{t('Name')}</Th>
          <Th>{t('Status')}</Th>
          <Th>{t('IPv4 CIDR')}</Th>
          <Th>{t('Available')}</Th>
          <Th aria-label={t('Actions')} />
        </Tr>
      </Thead>
      <Tbody>
        {pools.map((pool) => (
          <Tr key={pool.id}>
            <Td dataLabel={t('Name')}>
              <NetworkingResourceName
                resource={pool}
                detailsUrl={`/admin/infrastructure/external-ip-pools/${encodeURIComponent(pool.id)}`}
              />
            </Td>
            <Td dataLabel={t('Status')} modifier="nowrap">
              <StatusWithMessage message={pool.status?.message}>
                <ExternalIpPoolStatusLabel state={pool.status?.state} />
              </StatusWithMessage>
            </Td>
            <Td dataLabel={t('IPv4 CIDR')}>{formatCidrs(pool.spec?.cidrs)}</Td>
            <Td dataLabel={t('Available')} modifier="nowrap">
              <PoolAvailability pool={pool} />
            </Td>
            <Td dataLabel={t('Actions')} isActionCell>
              <ExternalIpPoolActionsMenu pool={pool} />
            </Td>
          </Tr>
        ))}
      </Tbody>
    </Table>
  );
};
