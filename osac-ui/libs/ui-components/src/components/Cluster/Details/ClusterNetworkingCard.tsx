import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import { type Cluster, SecurityGroups, Subnets, VirtualNetworks } from '@osac/types';

import ClusterResourceLink from './ClusterResourceLink';
import { useGetResource } from '../../../api/use-resource';
import { usePermissions } from '../../../hooks/use-permissions';
import { useTranslation } from '../../../hooks/useTranslation';
import { displayValue } from '../../../utils/detailFormatters';

interface ClusterNetworkingCardProps {
  cluster: Cluster;
}

const ClusterNetworkingCard = ({ cluster }: ClusterNetworkingCardProps) => {
  const { t } = useTranslation();
  const subnetRef = cluster.spec?.networkAttachment?.subnet;
  const securityGroups = cluster.spec?.networkAttachment?.securityGroups ?? [];
  const { allowed } = usePermissions(Subnets, 'get');
  const { data, error } = useGetResource(
    Subnets,
    { id: subnetRef?.id ?? '' },
    { enabled: allowed && Boolean(subnetRef?.id) },
  );
  const subnet = allowed && !error && data?.object?.id === subnetRef?.id ? data?.object : undefined;
  const virtualNetwork = subnet?.spec?.virtualNetwork;

  return (
    <Table aria-label={t('Cluster network attachment')} variant="compact">
      <Thead>
        <Tr>
          <Th>{t('Virtual network')}</Th>
          <Th>{t('Security groups')}</Th>
          <Th>{t('Subnet')}</Th>
        </Tr>
      </Thead>
      <Tbody>
        <Tr>
          <Td dataLabel={t('Virtual network')}>
            <ClusterResourceLink
              service={VirtualNetworks}
              basePath="/networking/virtual-networks"
              id={virtualNetwork?.id}
              name={virtualNetwork?.name}
            />
          </Td>
          <Td dataLabel={t('Security groups')}>
            {securityGroups.length
              ? securityGroups.map((group, index) => (
                  <span key={`${group.id}-${index}`}>
                    {index > 0 && ', '}
                    <ClusterResourceLink
                      service={SecurityGroups}
                      basePath="/networking/security-groups"
                      id={group.id}
                      name={group.name?.trim()}
                    />
                  </span>
                ))
              : '—'}
          </Td>
          <Td dataLabel={t('Subnet')}>
            {displayValue(subnetRef?.name || subnet?.metadata?.name || subnetRef?.id)}
          </Td>
        </Tr>
      </Tbody>
    </Table>
  );
};

export default ClusterNetworkingCard;
