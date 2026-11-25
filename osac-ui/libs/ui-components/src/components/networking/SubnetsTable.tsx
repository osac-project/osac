import type { ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { Flex, FlexItem } from '@patternfly/react-core';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import type { Subnet, VirtualNetwork } from '@osac/types';

import { networkingResourceOrigin } from './networking-resource-origin';
import NetworkingResourceOriginBadge from './NetworkingResourceOriginBadge';
import { virtualNetworkDetailsPath } from './subnet-list-utils';
import { SubnetStatusLabel } from './SubnetStatusLabel';
import { resourceDisplayName } from '../../api/v1/networking';
import { useTranslation } from '../../hooks/useTranslation';

interface SubnetsTableProps {
  subnets: readonly Subnet[];
  virtualNetworksById: ReadonlyMap<string, VirtualNetwork>;
  /** The network's own Subnets card does not repeat the parent column. */
  showVirtualNetwork?: boolean;
  renderActions?: (subnet: Subnet) => ReactNode;
  ariaLabel?: string;
}

/**
 * The one subnet table used by the Subnets list page and a network's Subnets
 * card. Names are plain text: a Subnet has no page, so only the parent network
 * is a link.
 */
export const SubnetsTable = ({
  subnets,
  virtualNetworksById,
  showVirtualNetwork = true,
  renderActions,
  ariaLabel,
}: SubnetsTableProps) => {
  const { t } = useTranslation();

  return (
    <Table aria-label={ariaLabel ?? t('Subnets')} variant="compact" borders>
      <Thead>
        <Tr>
          <Th>{t('Name')}</Th>
          <Th>{t('Status')}</Th>
          <Th>{t('IPv4 CIDR')}</Th>
          {showVirtualNetwork ? <Th>{t('Virtual network')}</Th> : null}
          <Th>{t('Project')}</Th>
          {renderActions ? <Th aria-label={t('Actions')} /> : null}
        </Tr>
      </Thead>
      <Tbody>
        {subnets.map((subnet) => {
          const origin = networkingResourceOrigin(subnet);
          const virtualNetworkId = subnet.spec?.virtualNetwork?.id ?? '';
          const virtualNetwork = virtualNetworkId
            ? virtualNetworksById.get(virtualNetworkId)
            : undefined;
          const project = subnet.metadata?.project;

          return (
            <Tr key={subnet.id}>
              <Td dataLabel={t('Name')}>
                <Flex
                  spaceItems={{ default: 'spaceItemsSm' }}
                  alignItems={{ default: 'alignItemsCenter' }}
                >
                  <FlexItem>{subnet.metadata?.name || subnet.id}</FlexItem>
                  {origin ? (
                    <FlexItem>
                      <NetworkingResourceOriginBadge origin={origin} />
                    </FlexItem>
                  ) : null}
                </Flex>
              </Td>
              <Td dataLabel={t('Status')}>
                <SubnetStatusLabel state={subnet.status?.state} />
              </Td>
              <Td dataLabel={t('IPv4 CIDR')}>{subnet.spec?.ipv4Cidr ?? '—'}</Td>
              {showVirtualNetwork ? (
                <Td dataLabel={t('Virtual network')}>
                  {virtualNetworkId ? (
                    <Link to={virtualNetworkDetailsPath(virtualNetworkId)}>
                      {resourceDisplayName(virtualNetwork?.metadata, virtualNetworkId)}
                    </Link>
                  ) : (
                    '—'
                  )}
                </Td>
              ) : null}
              <Td dataLabel={t('Project')}>{project === '' ? t('Root') : (project ?? '—')}</Td>
              {renderActions ? <Td isActionCell>{renderActions(subnet)}</Td> : null}
            </Tr>
          );
        })}
      </Tbody>
    </Table>
  );
};
