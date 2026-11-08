import { Link } from 'react-router-dom';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import type { ExternalIP, ExternalIPPool } from '@osac/types';
import type { ExternalIpAttachedTarget } from '@osac/ui-components/api/v1/external-ip-data';

import ExternalIpAttachedTo from './Details/ExternalIpAttachedTo';
import ExternalIpStatusLabel from './Details/ExternalIpStatusLabel';
import ExternalIpActionsMenu from './ExternalIpActionsMenu';
import { useTranslation } from '../../hooks/useTranslation';
import NetworkingProjectName from '../networking/NetworkingProjectName';
import NetworkingResourceName from '../networking/NetworkingResourceName';
import StatusWithMessage from '../networking/StatusWithMessage';
import { useNetworkingTenants } from '../networking/useNetworkingTenants';

interface ExternalIpsTableProps {
  externalIps: ExternalIP[];
  poolsById: Record<string, ExternalIPPool>;
  attachedTargetsByExternalIpId: Record<string, ExternalIpAttachedTarget>;
}

const resolvePool = (
  externalIp: ExternalIP,
  poolsById: Record<string, ExternalIPPool>,
): ExternalIPPool | undefined => {
  const poolId = externalIp.spec?.pool?.id || externalIp.status?.pool;
  return poolId ? poolsById[poolId] : undefined;
};

const ExternalIpsTable = ({
  externalIps,
  poolsById,
  attachedTargetsByExternalIpId,
}: ExternalIpsTableProps) => {
  const { t } = useTranslation();
  const { isAdmin, tenantName } = useNetworkingTenants();

  return (
    <Table aria-label={t('External IPs')} variant="compact">
      <Thead>
        <Tr>
          <Th>{t('Name')}</Th>
          <Th>{t('Status')}</Th>
          <Th info={{ tooltip: t('Reachable where your provider routes it.') }}>{t('Address')}</Th>
          <Th>{t('IP pool')}</Th>
          <Th>{t('Used by')}</Th>
          <Th modifier="wrap">{t('Project')}</Th>
          {isAdmin ? <Th>{t('Tenant')}</Th> : null}
          <Th aria-label={t('Actions')} />
        </Tr>
      </Thead>
      <Tbody>
        {externalIps.map((externalIp) => {
          const address = externalIp.status?.address || '—';
          const pool = resolvePool(externalIp, poolsById);
          const poolName =
            pool?.metadata?.name ||
            externalIp.spec?.pool?.name ||
            externalIp.spec?.pool?.id ||
            externalIp.status?.pool ||
            '—';
          const poolId = pool?.id || externalIp.spec?.pool?.id || externalIp.status?.pool;
          const consumer = attachedTargetsByExternalIpId[externalIp.id];

          return (
            <Tr key={externalIp.id}>
              {/* Names and pools stay on one line; the list has no Created column, like the others. */}
              <Td dataLabel={t('Name')} modifier="nowrap">
                <NetworkingResourceName resource={externalIp} />
              </Td>
              <Td dataLabel={t('Status')} modifier="nowrap">
                <StatusWithMessage message={externalIp.status?.message}>
                  <ExternalIpStatusLabel state={externalIp.status?.state} />
                </StatusWithMessage>
              </Td>
              <Td dataLabel={t('Address')} modifier="nowrap">
                {address}
              </Td>
              <Td dataLabel={t('IP pool')} modifier="nowrap">
                {/* Pools have a details page only for the provider admin. */}
                {isAdmin && poolId ? (
                  <Link
                    to={`/admin/infrastructure/external-ip-pools/${encodeURIComponent(poolId)}`}
                  >
                    {poolName}
                  </Link>
                ) : (
                  poolName
                )}
              </Td>
              <Td dataLabel={t('Used by')}>
                {consumer ? <ExternalIpAttachedTo attached target={consumer} /> : '—'}
              </Td>
              <Td dataLabel={t('Project')}>
                <NetworkingProjectName
                  project={externalIp.metadata?.project}
                  tenant={externalIp.metadata?.tenant}
                />
              </Td>
              {isAdmin ? (
                <Td dataLabel={t('Tenant')} modifier="nowrap">
                  {tenantName(externalIp.metadata?.tenant)}
                </Td>
              ) : null}
              <Td dataLabel={t('Actions')} isActionCell>
                <ExternalIpActionsMenu externalIp={externalIp} consumer={consumer} />
              </Td>
            </Tr>
          );
        })}
      </Tbody>
    </Table>
  );
};

export default ExternalIpsTable;
