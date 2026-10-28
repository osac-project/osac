import { useState } from 'react';
import {
  Alert,
  Button,
  Card,
  CardBody,
  CardTitle,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Flex,
  FlexItem,
  Title,
} from '@patternfly/react-core';
import GlobeIcon from '@patternfly/react-icons/dist/esm/icons/globe-icon';

import type { ComputeInstance } from '@osac/types';
import {
  ComputeInstanceState,
  ExternalIPAttachmentState,
  ExternalIPAttachments,
} from '@osac/types';

import AttachExternalIpModal from './AttachExternalIpModal';
import DetachExternalIpModal from './DetachExternalIpModal';
import { useListResource } from '../../../api/use-resource';
import { computeInstanceAttachmentFilter } from '../../../api/v1/external-ip-data';
import { useTranslation } from '../../../hooks/useTranslation';
import { getErrorMessage } from '../../../utils/error';

interface VmExternalIpSectionProps {
  vm: ComputeInstance;
}

const VmExternalIpSection = ({ vm }: VmExternalIpSectionProps) => {
  const { t } = useTranslation();
  const [attachOpen, setAttachOpen] = useState(false);
  const [detachOpen, setDetachOpen] = useState(false);
  const {
    data: attachmentResponse,
    isLoading,
    isFetching,
    error,
  } = useListResource(
    ExternalIPAttachments,
    { filter: computeInstanceAttachmentFilter(vm.id) },
    { enabled: true },
  );
  const attachment = attachmentResponse?.items?.[0];
  const externalIp = vm.status?.externalIpAddress?.trim();
  const hasExternalIp = Boolean(externalIp);
  const state = vm.status?.state;
  const canAttach = state === ComputeInstanceState.RUNNING && !hasExternalIp;
  const isAttaching =
    attachment?.status?.state === ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_PENDING ||
    (isFetching && !isLoading && !hasExternalIp);
  const isDetaching =
    attachment?.status?.state === ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_DELETING;
  const isFailed =
    attachment?.status?.state === ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_FAILED;
  const showDetach = Boolean(attachment) && (hasExternalIp || isFailed);
  const attachmentMissing = hasExternalIp && !isLoading && !error && !attachment;
  const attachmentMessage =
    typeof attachment?.status?.message === 'string'
      ? attachment.status.message
      : t('The external IP attachment could not be provisioned.');
  const actionDisabled = isLoading || Boolean(error) || attachmentMissing;

  return (
    <>
      {attachOpen ? <AttachExternalIpModal vm={vm} onClose={() => setAttachOpen(false)} /> : null}
      {detachOpen && attachment ? (
        <DetachExternalIpModal attachment={attachment} onClose={() => setDetachOpen(false)} />
      ) : null}
      <Card>
        <CardTitle>
          <Title headingLevel="h2" size="lg">
            {t('External IP')}
          </Title>
        </CardTitle>
        <CardBody>
          <DescriptionList isCompact>
            <DescriptionListGroup>
              <DescriptionListTerm>{t('Host')}</DescriptionListTerm>
              <DescriptionListDescription>
                <Flex flexWrap={{ default: 'wrap' }} gap={{ default: 'gapSm' }}>
                  <FlexItem>{isLoading ? t('Loading') : externalIp || t('Not set')}</FlexItem>
                  <FlexItem>
                    <Flex gap={{ default: 'gapSm' }} flexWrap={{ default: 'wrap' }}>
                      {isFailed ? (
                        <>
                          <Button
                            variant="link"
                            isDisabled={actionDisabled}
                            onClick={() => setAttachOpen(true)}
                          >
                            {t('Retry')}
                          </Button>
                          <Button
                            variant="link"
                            isDisabled={actionDisabled}
                            onClick={() => setAttachOpen(true)}
                          >
                            {t('Choose another')}
                          </Button>
                        </>
                      ) : null}
                      {!showDetach ? (
                        <Button
                          variant="link"
                          icon={<GlobeIcon />}
                          isDisabled={actionDisabled || isAttaching || !canAttach}
                          isLoading={isLoading || isAttaching}
                          onClick={() => setAttachOpen(true)}
                        >
                          {isAttaching ? t('Attaching external IP...') : t('Attach external IP')}
                        </Button>
                      ) : null}
                      {showDetach ? (
                        <Button
                          variant="link"
                          icon={<GlobeIcon />}
                          isDisabled={actionDisabled || isDetaching}
                          isLoading={isLoading || isDetaching}
                          onClick={() => setDetachOpen(true)}
                        >
                          {isDetaching ? t('Detaching external IP') : t('Detach external IP')}
                        </Button>
                      ) : null}
                    </Flex>
                  </FlexItem>
                </Flex>
                {error && hasExternalIp ? (
                  <Alert
                    variant="danger"
                    isInline
                    title={t('Failed to load external IP attachment')}
                  >
                    {getErrorMessage(error)}
                  </Alert>
                ) : null}
                {attachmentMissing ? (
                  <Alert variant="danger" isInline title={t('External IP attachment not found')}>
                    {t(
                      'The virtual machine reports an external IP, but its attachment resource is missing.',
                    )}
                  </Alert>
                ) : null}
                {isFailed ? (
                  <Alert variant="danger" isInline title={t('External IP attachment failed')}>
                    {attachmentMessage}
                  </Alert>
                ) : null}
                {isAttaching ? <div>{t('Attaching external IP...')}</div> : null}
                {isDetaching ? <div>{t('Detaching external IP')}</div> : null}
              </DescriptionListDescription>
            </DescriptionListGroup>
          </DescriptionList>
        </CardBody>
      </Card>
    </>
  );
};

export default VmExternalIpSection;
