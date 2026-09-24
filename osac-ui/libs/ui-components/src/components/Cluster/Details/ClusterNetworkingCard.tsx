import {
  Card,
  CardBody,
  CardTitle,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Flex,
  FlexItem,
  Spinner,
} from '@patternfly/react-core';

import { type Cluster, ClusterState } from '@osac/types';

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
  const ingressEndpoint = cluster.status?.ingressEndpoint;
  const isProvisioning = cluster.status?.state === ClusterState.PROGRESSING;
  const isPendingIngress = !ingressEndpoint?.trim() && isProvisioning;

  return (
    <Card isFullHeight>
      <CardTitle>{t('Networking')}</CardTitle>
      <CardBody>
        <DescriptionList isCompact>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Subnet')}</DescriptionListTerm>
            <DescriptionListDescription>{displayValue(subnetName)}</DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Security groups')}</DescriptionListTerm>
            <DescriptionListDescription>
              {formatSecurityGroups(securityGroups)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Ingress endpoint')}</DescriptionListTerm>
            <DescriptionListDescription>
              {isPendingIngress ? (
                <Flex
                  alignItems={{ default: 'alignItemsCenter' }}
                  spaceItems={{ default: 'spaceItemsSm' }}
                >
                  <FlexItem>
                    <Spinner size="md" aria-label={t('Ingress endpoint provisioning')} />
                  </FlexItem>
                  <FlexItem>{t('Pending')}</FlexItem>
                </Flex>
              ) : (
                displayValue(ingressEndpoint)
              )}
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </CardBody>
    </Card>
  );
};

export default ClusterNetworkingCard;
