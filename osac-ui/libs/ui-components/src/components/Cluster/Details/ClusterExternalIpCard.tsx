import {
  Bullseye,
  Button,
  Card,
  CardBody,
  CardHeader,
  CardTitle,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Divider,
  Flex,
  FlexItem,
  Spinner,
} from '@patternfly/react-core';

import {
  type Cluster,
  ClusterState,
  ExternalIPAttachmentEndpoint,
  ExternalIPAttachmentState,
  ExternalIPAttachments,
} from '@osac/types';
import type { ExternalIPAttachment } from '@osac/types';

import ClusterExternalIpAttachmentStatusLabel from './ClusterExternalIpAttachmentStatusLabel';
import { useListResource } from '../../../api/use-resource';
import { clusterAttachmentFilter } from '../../../api/v1/external-ip-data';
import { useTranslation } from '../../../hooks/useTranslation';
import QueryErrorState from '../../Resource/QueryErrorState';

export interface ClusterEndpointAttachments {
  api: ExternalIPAttachment | undefined;
  ingress: ExternalIPAttachment | undefined;
}

export const groupAttachmentsByEndpoint = (
  attachments: readonly ExternalIPAttachment[],
): ClusterEndpointAttachments => {
  let api: ExternalIPAttachment | undefined;
  let ingress: ExternalIPAttachment | undefined;

  for (const attachment of attachments) {
    const endpoint = attachment.spec?.targetEndpoint;

    if (endpoint === ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_API) {
      api = attachment;
    } else if (endpoint === ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS) {
      ingress = attachment;
    }
  }

  return { api, ingress };
};

export interface ClusterExternalIpCardProps {
  cluster: Cluster;
  onAttach: (endpoint: ExternalIPAttachmentEndpoint) => void;
  onDetach: (attachment: ExternalIPAttachment) => void;
}

const isAttachmentBusyState = (state: ExternalIPAttachmentState | undefined): boolean =>
  state === undefined ||
  state === ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_UNSPECIFIED ||
  state === ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_PENDING ||
  state === ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_DELETING;

interface EndpointRowProps {
  cluster: Cluster;
  endpoint: ExternalIPAttachmentEndpoint;
  endpointValue?: string;
  label: string;
  detachAriaLabel: string;
  attachment: ExternalIPAttachment | undefined;
  onAttach: (endpoint: ExternalIPAttachmentEndpoint) => void;
  onDetach: (attachment: ExternalIPAttachment) => void;
}

const EndpointRow = ({
  cluster,
  endpoint,
  endpointValue,
  label,
  detachAriaLabel,
  attachment,
  onAttach,
  onDetach,
}: EndpointRowProps) => {
  const { t } = useTranslation();
  const clusterState = cluster.status?.state;
  const autoAttachmentRequested = cluster.spec?.autoExternalIpAttachment === true;
  const endpointText =
    endpointValue || (clusterState === ClusterState.PROGRESSING ? t('Awaiting provisioning') : '—');
  const attachmentState = attachment?.status?.state;
  const isAttachmentInProgress = attachment !== undefined && isAttachmentBusyState(attachmentState);
  const isAutoAttachmentInProgress =
    autoAttachmentRequested &&
    (isAttachmentBusyState(attachmentState) ||
      attachmentState === ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_FAILED);
  const isEndpointReady = clusterState === ClusterState.READY && Boolean(endpointValue);
  const isAttachDisabled = !isEndpointReady || isAttachmentInProgress || isAutoAttachmentInProgress;
  const isDetachDisabled = isAttachmentInProgress || isAutoAttachmentInProgress;
  const actionButton = attachment ? (
    <Button
      variant="link"
      isInline
      aria-label={detachAriaLabel}
      isDisabled={isDetachDisabled}
      onClick={() => onDetach(attachment)}
    >
      {t('Detach')}
    </Button>
  ) : (
    <Button
      variant="link"
      isInline
      aria-label={t('Attach External IP')}
      isDisabled={isAttachDisabled}
      onClick={() => onAttach(endpoint)}
    >
      {t('Attach External IP')}
    </Button>
  );

  return (
    <DescriptionListGroup>
      <DescriptionListTerm>{label}</DescriptionListTerm>
      <DescriptionListDescription>
        <Flex direction={{ default: 'column' }} spaceItems={{ default: 'spaceItemsSm' }}>
          <FlexItem>
            <Flex
              alignItems={{ default: 'alignItemsCenter' }}
              spaceItems={{ default: 'spaceItemsSm' }}
            >
              <FlexItem>{endpointText}</FlexItem>
              <ClusterExternalIpAttachmentStatusLabel
                attachment={attachment}
                autoAttachmentRequested={autoAttachmentRequested}
              />
            </Flex>
          </FlexItem>
          <FlexItem>{actionButton}</FlexItem>
        </Flex>
      </DescriptionListDescription>
    </DescriptionListGroup>
  );
};

const ClusterExternalIpCard = ({ cluster, onAttach, onDetach }: ClusterExternalIpCardProps) => {
  const { t } = useTranslation();
  const {
    data: attachmentResponse,
    isLoading,
    error,
  } = useListResource(
    ExternalIPAttachments,
    { filter: clusterAttachmentFilter(cluster.id) },
    { enabled: Boolean(cluster.id) },
  );
  const attachments = attachmentResponse?.items ?? [];
  const grouped = groupAttachmentsByEndpoint(attachments);
  const endpointRows = [
    {
      endpoint: ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
      endpointValue: cluster.status?.apiEndpoint,
      label: t('API endpoint'),
      detachAriaLabel: t('Detach External IP from API endpoint'),
      attachment: grouped.api,
    },
    {
      endpoint: ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS,
      endpointValue: cluster.status?.ingressEndpoint,
      label: t('Ingress endpoint'),
      detachAriaLabel: t('Detach External IP from Ingress endpoint'),
      attachment: grouped.ingress,
    },
  ];

  return (
    <Card variant="secondary">
      <CardHeader>
        <CardTitle>{t('External IP endpoints')}</CardTitle>
      </CardHeader>
      <Divider />
      <CardBody>
        {isLoading ? (
          <Bullseye>
            <Spinner aria-label={t('Loading external IP attachments')} />
          </Bullseye>
        ) : error ? (
          <QueryErrorState error={error} title={t('Failed to load external IP attachments')} />
        ) : (
          <DescriptionList isCompact aria-label={t('External IP endpoints')}>
            {endpointRows.map((row) => (
              <EndpointRow
                key={row.endpoint}
                cluster={cluster}
                {...row}
                onAttach={onAttach}
                onDetach={onDetach}
              />
            ))}
          </DescriptionList>
        )}
      </CardBody>
    </Card>
  );
};

export default ClusterExternalIpCard;
