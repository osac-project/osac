import { useMemo } from 'react';
import {
  Alert,
  Card,
  CardBody,
  CardTitle,
  Label,
  Spinner,
  Stack,
  StackItem,
} from '@patternfly/react-core';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import { ExternalIPAttachments } from '@osac/types';
import type { BareMetalInstance, ExternalIPAttachment } from '@osac/types';

import { type CelFilter, escapeCelStringLiteral } from '../../api/cel';
import { useListResource } from '../../api/use-resource';
import {
  formatResourceIdsForReview,
  resourceDisplayName,
  useSecurityGroups,
  useSubnets,
  useVirtualNetworks,
} from '../../api/v1/networking';
import { useTranslation } from '../../hooks/useTranslation';
import { getErrorMessage } from '../../utils/error';
import { SubtleContent } from '../SubtleContent/SubtleContent';

interface Props {
  instance: BareMetalInstance;
}

const BareMetalNetworkingCard = ({ instance }: Props) => {
  const { t } = useTranslation();

  const { data: virtualNetworks = [], isLoading: vnLoading, error: vnError } = useVirtualNetworks();
  const { data: subnets = [], isLoading: subnetLoading, error: subnetError } = useSubnets();
  const { data: securityGroups = [], isLoading: sgLoading, error: sgError } = useSecurityGroups();

  const externalIpAttachmentFilter = useMemo((): CelFilter<ExternalIPAttachment> => {
    const id = escapeCelStringLiteral(instance.id);
    return `this.spec.baremetal_instance.id == "${id}"` as CelFilter<ExternalIPAttachment>;
  }, [instance.id]);

  const {
    data: externalIPAttachmentResponse,
    isLoading: eipaLoading,
    error: eipaError,
  } = useListResource(
    ExternalIPAttachments,
    { filter: externalIpAttachmentFilter },
    { enabled: Boolean(instance.spec?.autoExternalIpAttachment) },
  );
  const externalIPAttachments = externalIPAttachmentResponse?.items ?? [];

  const autoCreatedAttachment = externalIPAttachments.find(
    (eipa) => eipa.metadata?.labels?.['osac.openshift.io/auto-created'] === 'true',
  );

  const autoCreatedExternalIpAddress = autoCreatedAttachment?.status?.externalIpAddress;

  const attachmentRows = useMemo(() => {
    const networkAttachments = instance.spec?.networkAttachments ?? [];
    const networkAttachmentStatuses = instance.status?.networkAttachmentStatuses ?? [];

    return networkAttachments.map((attachment, index) => {
      const status = networkAttachmentStatuses[index];

      const subnet = subnets.find((s) => s.id === attachment.subnet?.id);
      const vn = virtualNetworks.find((v) => v.id === subnet?.spec?.virtualNetwork?.id);
      const sgNames = formatResourceIdsForReview(
        attachment.securityGroups?.map((sg) => sg.id ?? '') ?? [],
        securityGroups,
      );

      return {
        virtualNetwork: resourceDisplayName(vn?.metadata, vn?.id),
        subnet: resourceDisplayName(subnet?.metadata, subnet?.id),
        securityGroups: sgNames,
        internalIp: status?.ipAddress || '—',
      };
    });
  }, [
    instance.spec?.networkAttachments,
    instance.status?.networkAttachmentStatuses,
    subnets,
    virtualNetworks,
    securityGroups,
  ]);

  const isLoading = vnLoading || subnetLoading || sgLoading || eipaLoading;
  const error = vnError || subnetError || sgError || eipaError;

  return (
    <Card>
      <CardTitle>{t('Networking')}</CardTitle>
      <CardBody>
        {isLoading ? (
          <Spinner />
        ) : (
          <Stack hasGutter>
            {error ? (
              <StackItem>
                <Alert variant="danger" title={t('Failed to load networking resources')} isInline>
                  {getErrorMessage(error)}
                </Alert>
              </StackItem>
            ) : null}

            <StackItem>
              {attachmentRows.length > 0 ? (
                <Table aria-label={t('Network attachments')} variant="compact" borders>
                  <Thead>
                    <Tr>
                      <Th>{t('Virtual network')}</Th>
                      <Th>{t('Subnet')}</Th>
                      <Th>{t('Security groups')}</Th>
                      <Th>{t('Internal IP')}</Th>
                    </Tr>
                  </Thead>
                  <Tbody>
                    {attachmentRows.map((row, index) => (
                      <Tr key={`network-attachment-${index}`}>
                        <Td dataLabel={t('Virtual network')}>{row.virtualNetwork}</Td>
                        <Td dataLabel={t('Subnet')}>{row.subnet}</Td>
                        <Td dataLabel={t('Security groups')}>{row.securityGroups}</Td>
                        <Td dataLabel={t('Internal IP')}>{row.internalIp}</Td>
                      </Tr>
                    ))}
                  </Tbody>
                </Table>
              ) : (
                <SubtleContent component="p">{t('No network attachments')}</SubtleContent>
              )}
            </StackItem>

            {autoCreatedExternalIpAddress ? (
              <StackItem>
                <Alert variant="info" title={t('External access')} isInline>
                  <p>
                    {t('External IP')}: <strong>{autoCreatedExternalIpAddress}</strong>{' '}
                    <Label color="blue">{t('Auto-provisioned')}</Label>
                  </p>
                </Alert>
              </StackItem>
            ) : null}
          </Stack>
        )}
      </CardBody>
    </Card>
  );
};

export default BareMetalNetworkingCard;
