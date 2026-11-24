import { useMemo } from 'react';
import { Link } from 'react-router-dom';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import type { SecurityGroup, VirtualNetwork } from '@osac/types';
import { resourceDisplayName } from '@osac/ui-components/api/v1/networking';
import { Timestamp } from '@osac/ui-components/components/Primitives/Timestamp';
import ResourceNameField from '@osac/ui-components/components/Resource/ResourceNameField.tsx';

import { securityGroupDetailsPath } from './security-group-list-utils';
import SecurityGroupActionsMenu from './SecurityGroupActionsMenu';
import { SecurityGroupStatusLabel } from './SecurityGroupStatusLabel';
import { useTranslation } from '../../hooks/useTranslation';

interface SecurityGroupsTableProps {
  securityGroups: SecurityGroup[];
  virtualNetworksById: ReadonlyMap<string, VirtualNetwork>;
}

export const SecurityGroupsTable = ({
  securityGroups,
  virtualNetworksById,
}: SecurityGroupsTableProps) => {
  const { t } = useTranslation();

  const columnLabels = useMemo(
    () => ({
      name: t('Name'),
      status: t('Status'),
      project: t('Project'),
      virtualNetwork: t('Virtual Network'),
      inboundRules: t('Inbound Rules'),
      outboundRules: t('Outbound Rules'),
      created: t('Created'),
      actions: t('Actions'),
    }),
    [t],
  );

  return (
    <Table aria-label={t('Security groups')} variant="compact" borders>
      <Thead>
        <Tr>
          <Th>{columnLabels.name}</Th>
          <Th>{columnLabels.status}</Th>
          <Th>{columnLabels.project}</Th>
          <Th>{columnLabels.virtualNetwork}</Th>
          <Th>{columnLabels.inboundRules}</Th>
          <Th>{columnLabels.outboundRules}</Th>
          <Th>{columnLabels.created}</Th>
          <Th aria-label={columnLabels.actions} />
        </Tr>
      </Thead>
      <Tbody>
        {securityGroups.map((securityGroup) => {
          const virtualNetworkId = securityGroup.spec?.virtualNetwork?.id ?? '';
          const virtualNetwork = virtualNetworkId
            ? virtualNetworksById.get(virtualNetworkId)
            : undefined;
          const virtualNetworkName = resourceDisplayName(
            virtualNetwork?.metadata,
            virtualNetworkId,
          );
          const ingressCount = securityGroup.spec?.ingress?.length ?? 0;
          const egressCount = securityGroup.spec?.egress?.length ?? 0;

          return (
            <Tr key={securityGroup.id}>
              <Td dataLabel={columnLabels.name}>
                <ResourceNameField
                  resource={securityGroup}
                  detailsUrl={securityGroupDetailsPath(securityGroup.id)}
                />
              </Td>
              <Td dataLabel={columnLabels.status}>
                <SecurityGroupStatusLabel state={securityGroup.status?.state} />
              </Td>
              <Td dataLabel={columnLabels.project}>
                {securityGroup.metadata?.project || t('Default')}
              </Td>
              <Td dataLabel={columnLabels.virtualNetwork}>
                {virtualNetworkId ? (
                  <Link to={`/networking/virtual-networks/${virtualNetworkId}`}>
                    {virtualNetworkName}
                  </Link>
                ) : (
                  virtualNetworkName
                )}
              </Td>
              <Td dataLabel={columnLabels.inboundRules}>{ingressCount}</Td>
              <Td dataLabel={columnLabels.outboundRules}>{egressCount}</Td>
              <Td dataLabel={columnLabels.created}>
                <Timestamp value={securityGroup.metadata?.creationTimestamp} />
              </Td>
              <Td dataLabel={columnLabels.actions} isActionCell>
                <SecurityGroupActionsMenu securityGroup={securityGroup} />
              </Td>
            </Tr>
          );
        })}
      </Tbody>
    </Table>
  );
};
