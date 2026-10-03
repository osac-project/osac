import { useMemo } from 'react';
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
  Spinner,
} from '@patternfly/react-core';

import { type Cluster, ClusterState, ExternalIPAttachmentEndpoint } from '@osac/types';
import type { ExternalIPAttachment } from '@osac/types';

import { useExternalIPAttachments } from '../../../api/v1/external-ip';
import { clusterAttachmentFilter } from '../../../api/v1/external-ip-data';
import { useTranslation } from '../../../hooks/useTranslation';
import { displayValue } from '../../../utils/detailFormatters';

export interface EndpointAttachmentStatus {
  attachment: ExternalIPAttachment | undefined;
  externalIpAddress: string | undefined;
}

export interface ClusterEndpointAttachments {
  api: EndpointAttachmentStatus;
  ingress: EndpointAttachmentStatus;
}

export const groupAttachmentsByEndpoint = (
  attachments: readonly ExternalIPAttachment[],
): ClusterEndpointAttachments => {
  let api: EndpointAttachmentStatus = { attachment: undefined, externalIpAddress: undefined };
  let ingress: EndpointAttachmentStatus = { attachment: undefined, externalIpAddress: undefined };

  for (const attachment of attachments) {
    const endpoint = attachment.spec?.targetEndpoint;
    const ipAddress = attachment.status?.externalIpAddress || undefined;

    if (endpoint === ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_API) {
      api = { attachment, externalIpAddress: ipAddress };
    } else if (endpoint === ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS) {
      ingress = { attachment, externalIpAddress: ipAddress };
    }
  }

  return { api, ingress };
};

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

  const { data: externalIpAttachments = [], isLoading: isLoadingAttachments } =
    useExternalIPAttachments(
      { filter: clusterAttachmentFilter(cluster.id) },
      { enabled: Boolean(cluster.id) },
    );

  const endpointAttachments = useMemo(
    () => groupAttachmentsByEndpoint(externalIpAttachments),
    [externalIpAttachments],
  );

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

  const renderExternalIpLabel = (status: EndpointAttachmentStatus) => {
    if (isLoadingAttachments) {
      return <Spinner size="sm" aria-label={t('Loading external IP')} />;
    }
    if (status.externalIpAddress) {
      return (
        <Label color="green" isCompact>
          {status.externalIpAddress}
        </Label>
      );
    }
    return null;
  };

  const renderEndpointDescription = (
    value: string | undefined,
    attachmentStatus: EndpointAttachmentStatus,
  ) => {
    const displayText = renderEndpointValue(value);
    const externalIpLabel = renderExternalIpLabel(attachmentStatus);

    if (autoProvisioned || externalIpLabel) {
      return (
        <Flex spaceItems={{ default: 'spaceItemsSm' }} alignItems={{ default: 'alignItemsCenter' }}>
          <FlexItem>{displayText}</FlexItem>
          {autoProvisioned && (
            <FlexItem>
              <Label color="blue" isCompact>
                {t('Auto-provisioned')}
              </Label>
            </FlexItem>
          )}
          {externalIpLabel && <FlexItem>{externalIpLabel}</FlexItem>}
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
              {renderEndpointDescription(apiEndpoint, endpointAttachments.api)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Ingress endpoint')}</DescriptionListTerm>
            <DescriptionListDescription>
              {renderEndpointDescription(ingressEndpoint, endpointAttachments.ingress)}
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </CardBody>
    </Card>
  );
};

export default ClusterNetworkingCard;
