import {
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Stack,
  StackItem,
} from '@patternfly/react-core';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import type { Cluster } from '@osac/types';

import { useTranslation } from '../../../hooks/useTranslation';
import { displayValue } from '../../../utils/detailFormatters';

interface ClusterNetworkingCardProps {
  cluster: Cluster;
}

const formatSecurityGroups = (securityGroups?: Array<{ id: string; name?: string }>): string => {
  if (!securityGroups || securityGroups.length === 0) {
    return '—';
  }
  return securityGroups.map((sg) => sg.name?.trim() || sg.id).join(', ');
};

const ClusterNetworkingCard = ({ cluster }: ClusterNetworkingCardProps) => {
  const { t } = useTranslation();

  const subnetName = cluster.spec?.networkAttachment?.subnet?.name;
  const securityGroups = cluster.spec?.networkAttachment?.securityGroups;
  const podCidr = cluster.spec?.network?.podCidr;
  const serviceCidr = cluster.spec?.network?.serviceCidr;

  return (
    <Stack hasGutter>
      <StackItem>
        <Table aria-label={t('Cluster network attachment')} variant="compact">
          <Thead>
            <Tr>
              <Th>{t('Subnet')}</Th>
              <Th>{t('Security groups')}</Th>
            </Tr>
          </Thead>
          <Tbody>
            <Tr>
              <Td dataLabel={t('Subnet')}>{displayValue(subnetName)}</Td>
              <Td dataLabel={t('Security groups')}>{formatSecurityGroups(securityGroups)}</Td>
            </Tr>
          </Tbody>
        </Table>
      </StackItem>
      <StackItem>
        <DescriptionList isCompact aria-label={t('Cluster network CIDRs')}>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Pod CIDR')}</DescriptionListTerm>
            <DescriptionListDescription>{displayValue(podCidr)}</DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Service CIDR')}</DescriptionListTerm>
            <DescriptionListDescription>{displayValue(serviceCidr)}</DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </StackItem>
    </Stack>
  );
};

export default ClusterNetworkingCard;
