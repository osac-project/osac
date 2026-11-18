import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import { type Cluster, Subnets } from '@osac/types';

import { useGetResource } from '../../../api/use-resource';
import { useTranslation } from '../../../hooks/useTranslation';
import { displayValue } from '../../../utils/detailFormatters';
import ResourceLink from '../../Resource/ResourceLink';

interface ClusterNetworkingCardProps {
  cluster: Cluster;
}

const ClusterNetworkingCard = ({ cluster }: ClusterNetworkingCardProps) => {
  const { t } = useTranslation();
  const subnetRef = cluster.spec?.networkAttachment?.subnet;
  const securityGroups = cluster.spec?.networkAttachment?.securityGroups ?? [];
  const { data, error } = useGetResource(
    Subnets,
    { id: subnetRef?.id ?? '' },
    { enabled: Boolean(subnetRef?.id?.trim()) },
  );
  const virtualNetwork = error ? undefined : data?.object?.spec?.virtualNetwork;

  return (
    <Table
      aria-label={t('Cluster network attachment')}
      variant="compact"
      isPlain
      className="pf-v6-u-text-break-word"
    >
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
            {virtualNetwork?.id?.trim() ? (
              <ResourceLink
                basePath="/networking/virtual-networks"
                id={virtualNetwork.id}
                name={displayValue(virtualNetwork.name)}
              />
            ) : (
              displayValue(virtualNetwork?.name)
            )}
          </Td>
          <Td dataLabel={t('Security groups')}>
            {securityGroups.length
              ? securityGroups.map((group, index) => (
                  <span key={`${group.id}-${index}`}>
                    {index > 0 && ', '}
                    {group.id?.trim() ? (
                      <ResourceLink
                        basePath="/networking/security-groups"
                        id={group.id}
                        name={displayValue(group.name)}
                      />
                    ) : (
                      displayValue(group.name)
                    )}
                  </span>
                ))
              : '—'}
          </Td>
          <Td dataLabel={t('Subnet')}>{displayValue(subnetRef?.name)}</Td>
        </Tr>
      </Tbody>
    </Table>
  );
};

export default ClusterNetworkingCard;
