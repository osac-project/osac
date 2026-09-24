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
  Label,
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

const isTerminalFailedState = (state: ClusterState | undefined): boolean =>
  state === ClusterState.FAILED || state === ClusterState.DELETE_FAILED;

const ClusterNetworkingCard = ({ cluster }: ClusterNetworkingCardProps) => {
  const { t } = useTranslation();

  const subnetName = cluster.spec?.networkAttachment?.subnet?.name;
  const securityGroups = cluster.spec?.networkAttachment?.securityGroups;
  const podCidr = cluster.spec?.network?.podCidr;
  const serviceCidr = cluster.spec?.network?.serviceCidr;
  const apiEndpoint = cluster.status?.apiEndpoint;
  const ingressEndpoint = cluster.status?.ingressEndpoint;
  const clusterState = cluster.status?.state;
  const autoProvisioned = cluster.spec?.autoExternalIpAttachment === true;

  const isProvisioning = clusterState === ClusterState.PROGRESSING;
  const isFailed = isTerminalFailedState(clusterState);

  const renderEndpointValue = (value: string | undefined) => {
    if (value?.trim()) {
      return value.trim();
    }
    if (isFailed) {
      return '—';
    }
    if (isProvisioning) {
      return t('Awaiting provisioning');
    }
    return '—';
  };

  const renderEndpointDescription = (value: string | undefined) => {
    const displayText = renderEndpointValue(value);
    if (autoProvisioned) {
      return (
        <Flex spaceItems={{ default: 'spaceItemsSm' }} alignItems={{ default: 'alignItemsCenter' }}>
          <FlexItem>{displayText}</FlexItem>
          <FlexItem>
            <Label color="blue" isCompact>
              {t('Auto-provisioned')}
            </Label>
          </FlexItem>
        </Flex>
      );
    }
    return displayText;
  };

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
            <DescriptionListTerm>{t('Pod CIDR')}</DescriptionListTerm>
            <DescriptionListDescription>{displayValue(podCidr)}</DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Service CIDR')}</DescriptionListTerm>
            <DescriptionListDescription>{displayValue(serviceCidr)}</DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('API endpoint')}</DescriptionListTerm>
            <DescriptionListDescription>
              {renderEndpointDescription(apiEndpoint)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Ingress endpoint')}</DescriptionListTerm>
            <DescriptionListDescription>
              {renderEndpointDescription(ingressEndpoint)}
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </CardBody>
    </Card>
  );
};

export default ClusterNetworkingCard;
