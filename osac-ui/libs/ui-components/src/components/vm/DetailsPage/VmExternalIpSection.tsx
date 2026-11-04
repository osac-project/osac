import { useState } from 'react';
import {
  Alert,
  Button,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Flex,
  FlexItem,
} from '@patternfly/react-core';

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

const EMPTY_VALUE = '-';

const VmExternalIpSection = ({ vm }: VmExternalIpSectionProps) => {
  const { t } = useTranslation();
  const [attachOpen, setAttachOpen] = useState(false);
  const [detachOpen, setDetachOpen] = useState(false);
  const {
    data: attachmentResponse,
    isLoading,
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
  const isAttaching =
    attachment?.status?.state === ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_PENDING;
  const isDetaching =
    attachment?.status?.state === ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_DELETING;
  const isFailed =
    attachment?.status?.state === ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_FAILED;
  const canCreateAttachment =
    state === ComputeInstanceState.RUNNING && !hasExternalIp && (!attachment || isFailed);
  const showDetach = Boolean(attachment) && (hasExternalIp || isFailed);
  const attachmentMissing = hasExternalIp && !isLoading && !error && !attachment;
  const attachmentMessage =
    typeof attachment?.status?.message === 'string'
      ? attachment.status.message
      : t('The external IP attachment could not be provisioned.');
  const actionDisabled = isLoading || Boolean(error) || attachmentMissing;
  const closeAttach = () => {
    setAttachOpen(false);
  };

  return (
    <>
      {attachOpen ? <AttachExternalIpModal vm={vm} onClose={closeAttach} /> : null}
      {detachOpen && attachment ? (
        <DetachExternalIpModal attachment={attachment} onClose={() => setDetachOpen(false)} />
      ) : null}
      <DescriptionListGroup>
        <DescriptionListTerm>{t('External IP')}</DescriptionListTerm>
        <DescriptionListDescription>
          <Flex flexWrap={{ default: 'wrap' }} gap={{ default: 'gapSm' }}>
            <FlexItem>{isLoading ? t('Loading') : externalIp || EMPTY_VALUE}</FlexItem>
            <FlexItem>
              <Flex gap={{ default: 'gapSm' }} flexWrap={{ default: 'wrap' }}>
                {isFailed ? (
                  <>
                    <Button
                      variant="link"
                      isInline
                      isDisabled={actionDisabled || !canCreateAttachment}
                      onClick={() => setAttachOpen(true)}
                    >
                      {t('Retry')}
                    </Button>
                    <Button
                      variant="link"
                      isInline
                      isDisabled={actionDisabled || !canCreateAttachment}
                      onClick={() => setAttachOpen(true)}
                    >
                      {t('Choose another')}
                    </Button>
                  </>
                ) : null}
                {!showDetach && !isAttaching && !isFailed ? (
                  <Button
                    variant="secondary"
                    isDisabled={actionDisabled || !canCreateAttachment}
                    isLoading={isLoading}
                    onClick={() => setAttachOpen(true)}
                  >
                    {t('Attach external IP')}
                  </Button>
                ) : null}
                {showDetach && !isDetaching ? (
                  <Button
                    variant="secondary"
                    isDisabled={actionDisabled}
                    isLoading={isLoading}
                    onClick={() => setDetachOpen(true)}
                  >
                    {t('Detach')}
                  </Button>
                ) : null}
              </Flex>
            </FlexItem>
          </Flex>
          {error ? (
            <Alert variant="danger" isInline title={t('Failed to load external IP attachment')}>
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
          {isDetaching ? <div>{t('Detaching external IP...')}</div> : null}
        </DescriptionListDescription>
      </DescriptionListGroup>
    </>
  );
};

export default VmExternalIpSection;
