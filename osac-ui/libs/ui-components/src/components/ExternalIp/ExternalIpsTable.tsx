import { useMemo } from 'react';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import type { ExternalIP, ExternalIPPool } from '@osac/types';
import type { ExternalIpAttachedTarget } from '@osac/ui-components/api/v1/external-ip-data';
import ExternalIpPool from '@osac/ui-components/components/ExternalIp/Details/ExternalIpPool';

import ExternalIpAttachedTo from './Details/ExternalIpAttachedTo';
import ExternalIpStatusLabel from './Details/ExternalIpStatusLabel';
import ExternalIpActionsMenu from './ExternalIpActionsMenu';
import { useTranslation } from '../../hooks/useTranslation';
import { Timestamp } from '../Primitives/Timestamp';

const EMPTY_STATE = '—';

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

  const columnLabels = useMemo(
    () => ({
      name: t('Name'),
      status: t('Status'),
      project: t('Project'),
      attachedTo: t('Attached to'),
      address: t('Address'),
      ipPool: t('IP pool'),
      created: t('Created'),
      actions: t('Actions'),
    }),
    [t],
  );

  return (
    <Table aria-label={t('External IPs')} variant="compact">
      <Thead>
        <Tr>
          <Th>{columnLabels.name}</Th>
          <Th>{columnLabels.status}</Th>
          <Th>{columnLabels.project}</Th>
          <Th>{columnLabels.attachedTo}</Th>
          <Th>{columnLabels.address}</Th>
          <Th>{columnLabels.ipPool}</Th>
          <Th>{columnLabels.created}</Th>
          <Th aria-label={columnLabels.actions} />
        </Tr>
      </Thead>
      <Tbody>
        {externalIps.map((externalIp) => {
          const address = externalIp.status?.address || '—';
          const name = externalIp.metadata?.name;
          const pool = resolvePool(externalIp, poolsById);

          return (
            <Tr key={externalIp.id}>
              <Td dataLabel={columnLabels.name}>{name}</Td>
              <Td dataLabel={columnLabels.status}>
                <ExternalIpStatusLabel
                  state={externalIp.status?.state}
                  attached={externalIp.status?.attached}
                />
              </Td>
              <Td dataLabel={columnLabels.project}>
                {externalIp.metadata?.project || t('Default')}
              </Td>
              <Td dataLabel={columnLabels.attachedTo}>
                {externalIp.status?.attached && attachedTargetsByExternalIpId[externalIp.id] ? (
                  <ExternalIpAttachedTo
                    attached={externalIp.status.attached}
                    target={attachedTargetsByExternalIpId[externalIp.id]}
                  />
                ) : (
                  EMPTY_STATE
                )}
              </Td>
              <Td dataLabel={columnLabels.address}>{address}</Td>
              <Td dataLabel={columnLabels.ipPool}>
                <ExternalIpPool externalIp={externalIp} pool={pool} />
              </Td>
              <Td dataLabel={columnLabels.created}>
                <Timestamp value={externalIp.metadata?.creationTimestamp} />
              </Td>
              <Td dataLabel={columnLabels.actions} isActionCell>
                <ExternalIpActionsMenu externalIp={externalIp} />
              </Td>
            </Tr>
          );
        })}
      </Tbody>
    </Table>
  );
};

export default ExternalIpsTable;
