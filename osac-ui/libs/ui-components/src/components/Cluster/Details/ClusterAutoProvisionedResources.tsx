import { Content, Skeleton, Stack, StackItem, Title } from '@patternfly/react-core';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import {
  type ExternalIP,
  type ExternalIPAttachment,
  ExternalIPAttachmentEndpoint,
  ExternalIPAttachmentState,
  ExternalIPState,
} from '@osac/types';

import { useClusterAutoProvisionedResources } from '../../../api/v1/cluster';
import { useTranslation } from '../../../hooks/useTranslation';
import { ResourceStatusLabel, type StatusKind } from '../../Resource/ResourceStatusLabel';

interface ClusterAutoProvisionedResourcesProps {
  clusterId: string;
}

const externalIpStatusKind = (state?: ExternalIPState): StatusKind => {
  switch (state) {
    case ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED:
      return 'ready';
    case ExternalIPState.EXTERNAL_IP_STATE_PENDING:
    case ExternalIPState.EXTERNAL_IP_STATE_DELETING:
      return 'progressing';
    case ExternalIPState.EXTERNAL_IP_STATE_FAILED:
      return 'failed';
    default:
      return 'unspecified';
  }
};

const externalIpStatusText = (state?: ExternalIPState): string => {
  switch (state) {
    case ExternalIPState.EXTERNAL_IP_STATE_ALLOCATED:
      return 'Allocated';
    case ExternalIPState.EXTERNAL_IP_STATE_PENDING:
      return 'Pending';
    case ExternalIPState.EXTERNAL_IP_STATE_DELETING:
      return 'Deleting';
    case ExternalIPState.EXTERNAL_IP_STATE_FAILED:
      return 'Failed';
    default:
      return 'Unknown';
  }
};

const attachmentStatusKind = (state?: ExternalIPAttachmentState): StatusKind => {
  switch (state) {
    case ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_READY:
      return 'ready';
    case ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_PENDING:
    case ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_DELETING:
      return 'progressing';
    case ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_FAILED:
      return 'failed';
    default:
      return 'unspecified';
  }
};

const attachmentStatusText = (state?: ExternalIPAttachmentState): string => {
  switch (state) {
    case ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_READY:
      return 'Ready';
    case ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_PENDING:
      return 'Pending';
    case ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_DELETING:
      return 'Deleting';
    case ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_FAILED:
      return 'Failed';
    default:
      return 'Unknown';
  }
};

const endpointLabel = (endpoint?: ExternalIPAttachmentEndpoint): string => {
  switch (endpoint) {
    case ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_API:
      return 'API';
    case ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS:
      return 'Ingress';
    default:
      return '—';
  }
};

const resourceName = (metadata?: { name?: string }, id?: string): string =>
  metadata?.name?.trim() || id || '—';

const ClusterAutoProvisionedResources = ({ clusterId }: ClusterAutoProvisionedResourcesProps) => {
  const { t } = useTranslation();
  const { externalIps, externalIpAttachments, isLoading } = useClusterAutoProvisionedResources(
    clusterId,
    true,
  );

  if (isLoading) {
    return (
      <Stack hasGutter>
        <StackItem>
          <Title headingLevel="h4">{t('Auto-provisioned resources')}</Title>
        </StackItem>
        <StackItem>
          <Skeleton width="100%" height="60px" />
        </StackItem>
      </Stack>
    );
  }

  const rows: Array<{
    key: string;
    name: string;
    type: string;
    endpoint: string;
    status: StatusKind;
    statusText: string;
    address: string;
  }> = [];

  externalIps.forEach((ip: ExternalIP) => {
    rows.push({
      key: `eip-${ip.id}`,
      name: resourceName(ip.metadata, ip.id),
      type: t('ExternalIP'),
      endpoint: '—',
      status: externalIpStatusKind(ip.status?.state),
      statusText: externalIpStatusText(ip.status?.state),
      address: ip.status?.address || '—',
    });
  });

  externalIpAttachments.forEach((att: ExternalIPAttachment) => {
    rows.push({
      key: `eipa-${att.id}`,
      name: resourceName(att.metadata, att.id),
      type: t('ExternalIPAttachment'),
      endpoint: endpointLabel(att.spec?.targetEndpoint),
      status: attachmentStatusKind(att.status?.state),
      statusText: attachmentStatusText(att.status?.state),
      address: att.status?.externalIpAddress || '—',
    });
  });

  return (
    <Stack hasGutter>
      <StackItem>
        <Title headingLevel="h4">{t('Auto-provisioned resources')}</Title>
      </StackItem>
      <StackItem>
        {rows.length > 0 ? (
          <Table aria-label={t('Auto-provisioned resources')} variant="compact">
            <Thead>
              <Tr>
                <Th>{t('Name')}</Th>
                <Th>{t('Type')}</Th>
                <Th>{t('Endpoint')}</Th>
                <Th>{t('Status')}</Th>
                <Th>{t('Address')}</Th>
              </Tr>
            </Thead>
            <Tbody>
              {rows.map((row) => (
                <Tr key={row.key}>
                  <Td dataLabel={t('Name')}>{row.name}</Td>
                  <Td dataLabel={t('Type')}>{row.type}</Td>
                  <Td dataLabel={t('Endpoint')}>{row.endpoint}</Td>
                  <Td dataLabel={t('Status')}>
                    <ResourceStatusLabel status={row.status} text={row.statusText} />
                  </Td>
                  <Td dataLabel={t('Address')}>{row.address}</Td>
                </Tr>
              ))}
            </Tbody>
          </Table>
        ) : (
          <Content component="p">{t('No auto-provisioned resources.')}</Content>
        )}
      </StackItem>
    </Stack>
  );
};

export default ClusterAutoProvisionedResources;
