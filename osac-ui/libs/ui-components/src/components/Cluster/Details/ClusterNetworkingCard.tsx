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
  Tooltip,
} from '@patternfly/react-core';
import ExclamationCircleIcon from '@patternfly/react-icons/dist/esm/icons/exclamation-circle-icon';

import {
  type Cluster,
  ClusterState,
  ExternalIPAttachmentEndpoint,
  ExternalIPAttachmentState,
} from '@osac/types';
import type { ExternalIPAttachment } from '@osac/types';

import { useExternalIPAttachments } from '../../../api/v1/external-ip';
import { clusterAttachmentFilter } from '../../../api/v1/external-ip-data';
import { useTranslation } from '../../../hooks/useTranslation';
import { displayValue } from '../../../utils/detailFormatters';
import { getErrorMessage } from '../../../utils/error';

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
    if (attachment.status?.state !== ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_READY) {
      continue;
    }

    const endpoint = attachment.spec?.targetEndpoint;
    const ipAddress = attachment.status?.externalIpAddress;

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

interface EndpointDescriptionProps {
  type: ExternalIPAttachmentEndpoint;
  cluster: Cluster;
}

const EndpointDescription = ({ type, cluster }: EndpointDescriptionProps) => {
  const { t } = useTranslation();

  const clusterId = cluster.id;
  const clusterState = cluster.status?.state;
  const autoProvisioned = cluster.spec?.autoExternalIpAttachment === true;
  const isProvisioning = clusterState === ClusterState.PROGRESSING;
  const isFailed = isTerminalFailedState(clusterState);

  const value =
    type === ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_API
      ? cluster.status?.apiEndpoint
      : cluster.status?.ingressEndpoint;

  const {
    data: attachments = [],
    isLoading,
    error,
  } = useExternalIPAttachments(
    { filter: clusterAttachmentFilter(clusterId) },
    { enabled: Boolean(clusterId) },
  );

  const displayText = (() => {
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
  })();

  const grouped = groupAttachmentsByEndpoint(attachments);
  const key =
    type === ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_API
      ? ('api' as const)
      : ('ingress' as const);
  const hasExternalIpContent = isLoading || !!error || !!grouped[key].externalIpAddress;

  const renderExternalIpStatus = () => {
    if (isLoading) {
      return <Spinner size="sm" aria-label={t('Loading external IP')} />;
    }
    if (error) {
      return (
        <Tooltip content={getErrorMessage(error)}>
          <Label color="red" isCompact icon={<ExclamationCircleIcon />}>
            {t('External IP error')}
          </Label>
        </Tooltip>
      );
    }
    if (!grouped[key].externalIpAddress) {
      return null;
    }
    return (
      <Label color="green" isCompact>
        {grouped[key].externalIpAddress}
      </Label>
    );
  };

  if (autoProvisioned || hasExternalIpContent) {
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
        {hasExternalIpContent && <FlexItem>{renderExternalIpStatus()}</FlexItem>}
      </Flex>
    );
  }
  return <>{displayText}</>;
};

const ClusterNetworkingCard = ({ cluster }: ClusterNetworkingCardProps) => {
  const { t } = useTranslation();

  const subnetName = cluster.spec?.networkAttachment?.subnet?.name;
  const securityGroups = cluster.spec?.networkAttachment?.securityGroups;
  const podCidr = cluster.spec?.network?.podCidr;
  const serviceCidr = cluster.spec?.network?.serviceCidr;

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
              <EndpointDescription
                type={ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_API}
                cluster={cluster}
              />
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>{t('Ingress endpoint')}</DescriptionListTerm>
            <DescriptionListDescription>
              <EndpointDescription
                type={ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS}
                cluster={cluster}
              />
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </CardBody>
    </Card>
  );
};

export default ClusterNetworkingCard;
