import { Content, Flex, FlexItem } from '@patternfly/react-core';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import type { ExternalIP, ExternalIPPool } from '@osac/types';
import type { ExternalIpAttachedTarget } from '@osac/ui-components/api/v1/external-ip-data';

import ExternalIpAttachedTo from './Details/ExternalIpAttachedTo';
import ExternalIpStatusLabel from './Details/ExternalIpStatusLabel';
import ExternalIpActionsMenu from './ExternalIpActionsMenu';
import { useTranslation } from '../../hooks/useTranslation';
import { Timestamp } from '../Primitives/Timestamp';

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

  return (
    <Table aria-label={t('External IPs')} variant="compact">
      <Thead>
        <Tr>
          <Th>{t('Name')}</Th>
          <Th>{t('Status')}</Th>
          <Th>{t('Project')}</Th>
          <Th>{t('Address')}</Th>
          <Th>{t('IP pool')}</Th>
          <Th>{t('Created')}</Th>
          <Th aria-label={t('Actions')} />
        </Tr>
      </Thead>
      <Tbody>
        {externalIps.map((externalIp) => {
          const address = externalIp.status?.address || '—';
          const name = externalIp.metadata?.name;
          const pool = resolvePool(externalIp, poolsById);
          const available = pool?.status?.available;

          return (
            <Tr key={externalIp.id}>
              <Td dataLabel={t('Name')}>{name}</Td>
              <Td dataLabel={t('Status')}>
                <Flex direction={{ default: 'column' }} spaceItems={{ default: 'spaceItemsXs' }}>
                  <FlexItem>
                    <ExternalIpStatusLabel
                      state={externalIp.status?.state}
                      attached={externalIp.status?.attached}
                    />
                  </FlexItem>
                  {externalIp.status?.attached && attachedTargetsByExternalIpId[externalIp.id] ? (
                    <FlexItem>
                      <ExternalIpAttachedTo
                        attached={externalIp.status.attached}
                        target={attachedTargetsByExternalIpId[externalIp.id]}
                      />
                    </FlexItem>
                  ) : null}
                </Flex>
              </Td>
              <Td dataLabel={t('Project')}>{externalIp.metadata?.project || t('Default')}</Td>
              <Td dataLabel={t('Address')}>{address}</Td>
              <Td dataLabel={t('IP pool')}>
                <Flex direction={{ default: 'column' }} spaceItems={{ default: 'spaceItemsXs' }}>
                  <FlexItem>{externalIp.spec?.pool?.name}</FlexItem>
                  {available !== undefined ? (
                    <FlexItem>
                      <Content component="small">
                        {t('{{available}} available', { available: available.toString() })}
                      </Content>
                    </FlexItem>
                  ) : null}
                </Flex>
              </Td>
              <Td dataLabel={t('Created')}>
                <Timestamp value={externalIp.metadata?.creationTimestamp} />
              </Td>
              <Td dataLabel={t('Actions')} isActionCell>
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
