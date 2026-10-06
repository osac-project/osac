import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Alert, Button, Flex, Stack, StackItem } from '@patternfly/react-core';
import DumpsterIcon from '@patternfly/react-icons/dist/esm/icons/dumpster-icon';
import GlobeIcon from '@patternfly/react-icons/dist/esm/icons/globe-icon';
import PlayIcon from '@patternfly/react-icons/dist/esm/icons/play-icon';
import StopIcon from '@patternfly/react-icons/dist/esm/icons/stop-icon';
import SyncAltIcon from '@patternfly/react-icons/dist/esm/icons/sync-alt-icon';

import type { ComputeInstance } from '@osac/types';
import { ComputeInstanceState, ExternalIPAttachmentState } from '@osac/types';

import AttachExternalIpModal from './AttachExternalIpModal';
import DetachExternalIpModal from './DetachExternalIpModal';
import VmDeleteConfirmModal from './VmDeleteConfirmModal';
import { useExternalIPAttachments } from '../../../api/v1/external-ip';
import { computeInstanceAttachmentFilter } from '../../../api/v1/external-ip-data';
import { useTranslation } from '../../../hooks/useTranslation';
import { getErrorMessage } from '../../../utils/error';
import { useVmPowerAction } from '../useVmPowerAction';

interface VmDetailsActionButtonsProps {
  vm: ComputeInstance;
}

const VmDetailsActionButtons = ({ vm }: VmDetailsActionButtonsProps) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [attachExternalIpOpen, setAttachExternalIpOpen] = useState(false);
  const [detachExternalIpOpen, setDetachExternalIpOpen] = useState(false);
  const { runPowerAction } = useVmPowerAction();
  const {
    data: externalIpAttachments = [],
    isLoading: isLoadingExternalIpAttachments,
    isFetching: isFetchingExternalIpAttachments,
    error: externalIpAttachmentsError,
  } = useExternalIPAttachments(
    { filter: computeInstanceAttachmentFilter(vm.id) },
    { enabled: true },
  );

  const state = vm.status?.state;
  const canStart = state === ComputeInstanceState.STOPPED;
  const canStop = state === ComputeInstanceState.RUNNING || state === ComputeInstanceState.PAUSED;
  const canRestart =
    state === ComputeInstanceState.RUNNING || state === ComputeInstanceState.PAUSED;
  const canDelete = state !== ComputeInstanceState.DELETING;
  const canAttachExternalIp =
    state === ComputeInstanceState.RUNNING && !vm.status?.externalIpAddress;
  const externalIpAttachment = externalIpAttachments[0];
  const hasAttachedExternalIp = Boolean(vm.status?.externalIpAddress);
  const isAttachingExternalIp =
    externalIpAttachment?.status?.state ===
      ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_PENDING ||
    (isFetchingExternalIpAttachments && !isLoadingExternalIpAttachments && !hasAttachedExternalIp);
  const isDetachingExternalIp =
    externalIpAttachment?.status?.state ===
    ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_DELETING;
  const isExternalIpAttachmentFailed =
    externalIpAttachment?.status?.state ===
    ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_FAILED;
  const showDetachExternalIp =
    Boolean(externalIpAttachment) && (hasAttachedExternalIp || isExternalIpAttachmentFailed);
  const externalIpAttachmentMissing =
    hasAttachedExternalIp &&
    !isLoadingExternalIpAttachments &&
    !externalIpAttachmentsError &&
    !externalIpAttachment;

  return (
    <>
      {deleteOpen && (
        <VmDeleteConfirmModal
          vm={vm}
          onClose={() => setDeleteOpen(false)}
          onSuccess={() => navigate('/vms')}
        />
      )}
      {attachExternalIpOpen && (
        <AttachExternalIpModal
          vm={vm}
          onClose={() => setAttachExternalIpOpen(false)}
          onSuccess={() => setAttachExternalIpOpen(false)}
        />
      )}
      {detachExternalIpOpen && externalIpAttachment && (
        <DetachExternalIpModal
          attachment={externalIpAttachment}
          externalIpAddress={vm.status?.externalIpAddress}
          onClose={() => setDetachExternalIpOpen(false)}
        />
      )}
      <Stack hasGutter>
        {Boolean(externalIpAttachmentsError) && hasAttachedExternalIp && (
          <StackItem>
            <Alert variant="danger" isInline title={t('Failed to load external IP attachment')}>
              {getErrorMessage(externalIpAttachmentsError)}
            </Alert>
          </StackItem>
        )}
        {externalIpAttachmentMissing && (
          <StackItem>
            <Alert variant="danger" isInline title={t('External IP attachment not found')}>
              {t(
                'The virtual machine reports an external IP, but its attachment resource is missing.',
              )}
            </Alert>
          </StackItem>
        )}
        {isExternalIpAttachmentFailed && (
          <StackItem>
            <Alert variant="danger" isInline title={t('External IP attachment failed')}>
              {typeof externalIpAttachment?.status?.message === 'string'
                ? externalIpAttachment.status.message
                : t('The external IP attachment could not be provisioned.')}
            </Alert>
          </StackItem>
        )}
        <StackItem>
          <Flex
            justifyContent={{ default: 'justifyContentFlexEnd' }}
            spaceItems={{ default: 'spaceItemsSm' }}
            flexWrap={{ default: 'wrap' }}
          >
            <Button
              variant="primary"
              icon={<PlayIcon />}
              isDisabled={!canStart}
              onClick={() => {
                if (canStart) {
                  runPowerAction(vm.id, vm.metadata?.name ?? vm.id, 'start');
                }
              }}
            >
              Start
            </Button>
            <Button
              variant="secondary"
              icon={<StopIcon />}
              isDisabled={!canStop}
              onClick={() => {
                if (canStop) {
                  runPowerAction(vm.id, vm.metadata?.name ?? vm.id, 'stop');
                }
              }}
            >
              Stop
            </Button>
            <Button
              variant="secondary"
              icon={<SyncAltIcon />}
              isDisabled={!canRestart}
              onClick={() => {
                if (canRestart) {
                  runPowerAction(vm.id, vm.metadata?.name ?? vm.id, 'restart');
                }
              }}
            >
              Restart
            </Button>
            {!showDetachExternalIp && (
              <Button
                variant="secondary"
                icon={<GlobeIcon />}
                isDisabled={
                  isLoadingExternalIpAttachments ||
                  Boolean(externalIpAttachmentsError) ||
                  externalIpAttachmentMissing ||
                  isAttachingExternalIp ||
                  !canAttachExternalIp
                }
                isLoading={isLoadingExternalIpAttachments || isAttachingExternalIp}
                onClick={() => setAttachExternalIpOpen(true)}
              >
                {t(
                  isLoadingExternalIpAttachments
                    ? 'Loading external IP attachment...'
                    : isAttachingExternalIp
                      ? 'Attaching External IP...'
                      : 'Attach external IP',
                )}
              </Button>
            )}
            {showDetachExternalIp && externalIpAttachment && (
              <Button
                variant="secondary"
                icon={<GlobeIcon />}
                isDisabled={
                  isLoadingExternalIpAttachments ||
                  Boolean(externalIpAttachmentsError) ||
                  externalIpAttachmentMissing ||
                  isDetachingExternalIp
                }
                isLoading={isLoadingExternalIpAttachments || isDetachingExternalIp}
                onClick={() => setDetachExternalIpOpen(true)}
              >
                {t(isDetachingExternalIp ? 'Detaching external IP' : 'Detach external IP')}
              </Button>
            )}
            <Button
              variant="danger"
              icon={<DumpsterIcon />}
              isDisabled={!canDelete}
              onClick={() => {
                if (canDelete) {
                  setDeleteOpen(true);
                }
              }}
            >
              Delete
            </Button>
          </Flex>
        </StackItem>
      </Stack>
    </>
  );
};

export default VmDetailsActionButtons;
