import { Content, Flex, FlexItem } from '@patternfly/react-core';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import { type ExternalIPAttachment, ExternalIPAttachmentEndpoint } from '@osac/types';
import { attachedTargetFromAttachment } from '@osac/ui-components/api/v1/external-ip-data';

import ExternalIpAttachedTo from './Details/ExternalIpAttachedTo';
import ExternalIpAttachmentStatusLabel from './Details/ExternalIpAttachmentStatusLabel';
import ExternalIpAttachmentActionsMenu from './ExternalIpAttachmentActionsMenu';
import { useTranslation } from '../../hooks/useTranslation';
import NetworkingProjectName from '../networking/NetworkingProjectName';
import NetworkingResourceName from '../networking/NetworkingResourceName';
import StatusWithMessage from '../networking/StatusWithMessage';
import { useNetworkingTenants } from '../networking/useNetworkingTenants';

interface ExternalIpAttachmentsTableProps {
  attachments: ExternalIPAttachment[];
}

const ExternalIpAttachmentsTable = ({ attachments }: ExternalIpAttachmentsTableProps) => {
  const { t } = useTranslation();
  const { isAdmin, tenantName } = useNetworkingTenants();

  const endpointLabel = (endpoint?: ExternalIPAttachmentEndpoint): string | undefined => {
    switch (endpoint) {
      case ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_API:
        return t('API endpoint');
      case ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS:
        return t('Ingress endpoint');
      default:
        return undefined;
    }
  };

  return (
    <Table aria-label={t('External IP attachments')} variant="compact">
      <Thead>
        <Tr>
          <Th>{t('Name')}</Th>
          <Th info={{ tooltip: t('Reachable where your provider routes it.') }}>
            {t('External IP')}
          </Th>
          <Th>{t('Target')}</Th>
          <Th>{t('Status')}</Th>
          <Th>{t('Project')}</Th>
          {isAdmin ? <Th>{t('Tenant')}</Th> : null}
          <Th aria-label={t('Actions')} />
        </Tr>
      </Thead>
      <Tbody>
        {attachments.map((attachment) => {
          const target = attachedTargetFromAttachment(attachment);
          const endpoint =
            attachment.spec?.target?.case === 'cluster'
              ? endpointLabel(attachment.spec.targetEndpoint)
              : undefined;
          return (
            <Tr key={attachment.id}>
              <Td dataLabel={t('Name')}>
                <NetworkingResourceName resource={attachment} />
              </Td>
              <Td dataLabel={t('External IP')} modifier="nowrap">
                <code>{attachment.status?.externalIpAddress || '—'}</code>
              </Td>
              <Td dataLabel={t('Target')}>
                {target ? (
                  <Flex direction={{ default: 'column' }} spaceItems={{ default: 'spaceItemsXs' }}>
                    <FlexItem>
                      <ExternalIpAttachedTo attached target={target} />
                    </FlexItem>
                    {endpoint ? (
                      <FlexItem>
                        <Content component="small">{endpoint}</Content>
                      </FlexItem>
                    ) : null}
                  </Flex>
                ) : (
                  '—'
                )}
              </Td>
              <Td dataLabel={t('Status')} modifier="nowrap">
                <StatusWithMessage message={attachment.status?.message}>
                  <ExternalIpAttachmentStatusLabel state={attachment.status?.state} />
                </StatusWithMessage>
              </Td>
              <Td dataLabel={t('Project')}>
                <NetworkingProjectName
                  project={attachment.metadata?.project}
                  tenant={attachment.metadata?.tenant}
                />
              </Td>
              {isAdmin ? (
                <Td dataLabel={t('Tenant')} modifier="nowrap">
                  {tenantName(attachment.metadata?.tenant)}
                </Td>
              ) : null}
              <Td dataLabel={t('Actions')} isActionCell>
                <ExternalIpAttachmentActionsMenu attachment={attachment} />
              </Td>
            </Tr>
          );
        })}
      </Tbody>
    </Table>
  );
};

export default ExternalIpAttachmentsTable;
