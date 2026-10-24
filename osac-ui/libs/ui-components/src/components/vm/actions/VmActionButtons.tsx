import { useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import {
  Alert,
  Button,
  Dropdown,
  DropdownItem,
  DropdownList,
  Flex,
  MenuToggle,
  Stack,
  StackItem,
} from '@patternfly/react-core';
import { EllipsisVIcon } from '@patternfly/react-icons/dist/esm/icons/ellipsis-v-icon';
import GlobeIcon from '@patternfly/react-icons/dist/esm/icons/globe-icon';
import PlayIcon from '@patternfly/react-icons/dist/esm/icons/play-icon';
import StopIcon from '@patternfly/react-icons/dist/esm/icons/stop-icon';
import SyncAltIcon from '@patternfly/react-icons/dist/esm/icons/sync-alt-icon';

import type { ComputeInstance } from '@osac/types';
import {
  ComputeInstanceState,
  ExternalIPAttachmentState,
  ExternalIPAttachments,
} from '@osac/types';
import DeleteResourceButton from '@osac/ui-components/components/Resource/DeleteResourceButton';

import AttachExternalIpModal from './AttachExternalIpModal';
import DetachExternalIpModal from './DetachExternalIpModal';
import VmDeleteConfirmModal from './VmDeleteConfirmModal';
import { useListResource } from '../../../api/use-resource';
import { computeInstanceAttachmentFilter } from '../../../api/v1/external-ip-data';
import { useTranslation } from '../../../hooks/useTranslation';
import { getErrorMessage } from '../../../utils/error';
import { useVmPowerAction } from '../useVmPowerAction';

interface VmActionButtonsProps {
  vm: ComputeInstance;
  isMenu?: boolean;
}

const VmActionButtons = ({ vm, isMenu }: VmActionButtonsProps) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [attachExternalIpOpen, setAttachExternalIpOpen] = useState(false);
  const [detachExternalIpOpen, setDetachExternalIpOpen] = useState(false);
  const { runPowerAction } = useVmPowerAction();
  const {
    data: externalIpAttachmentResponse,
    isLoading: isLoadingExternalIpAttachments,
    isFetching: isFetchingExternalIpAttachments,
    error: externalIpAttachmentsError,
  } = useListResource(
    ExternalIPAttachments,
    { filter: computeInstanceAttachmentFilter(vm.id) },
    { enabled: !isMenu || open },
  );
  const externalIpAttachments = externalIpAttachmentResponse?.items ?? [];

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
  const isExternalIpAttachmentReady =
    externalIpAttachment?.status?.state ===
    ExternalIPAttachmentState.EXTERNAL_IP_ATTACHMENT_STATE_READY;
  const showDetachExternalIp =
    Boolean(externalIpAttachment) &&
    (hasAttachedExternalIp || isExternalIpAttachmentFailed || isExternalIpAttachmentReady);
  const externalIpAttachmentMissing =
    hasAttachedExternalIp &&
    !isLoadingExternalIpAttachments &&
    !externalIpAttachmentsError &&
    !externalIpAttachment;

  const onStart = () => {
    if (canStart) {
      runPowerAction(vm.id, vm.metadata?.name ?? vm.id, 'start');
    }
    setOpen(false);
  };

  const onStop = () => {
    if (canStop) {
      runPowerAction(vm.id, vm.metadata?.name ?? vm.id, 'stop');
    }
    setOpen(false);
  };

  const onRestart = () => {
    if (canRestart) {
      runPowerAction(vm.id, vm.metadata?.name ?? vm.id, 'restart');
    }
    setOpen(false);
  };

  const onAttachExternalIP = () => {
    setAttachExternalIpOpen(true);
    setOpen(false);
  };

  const attachDisabled =
    isLoadingExternalIpAttachments ||
    Boolean(externalIpAttachmentsError) ||
    externalIpAttachmentMissing ||
    isAttachingExternalIp ||
    !canAttachExternalIp;
  const attachLoading = isLoadingExternalIpAttachments || isAttachingExternalIp;
  const attachLabel = t(
    isLoadingExternalIpAttachments
      ? 'Loading external IP attachment...'
      : isAttachingExternalIp
        ? 'Attaching External IP...'
        : 'Attach external IP',
  );

  const onDetach = () => {
    setDetachExternalIpOpen(true);
    setOpen(false);
  };

  const detachDisabled =
    isLoadingExternalIpAttachments ||
    Boolean(externalIpAttachmentsError) ||
    externalIpAttachmentMissing ||
    isDetachingExternalIp;
  const detachLoading = isLoadingExternalIpAttachments || isDetachingExternalIp;
  const detachLabel = t(isDetachingExternalIp ? 'Detaching external IP' : 'Detach external IP');

  const onDelete = () => {
    if (canDelete) {
      setDeleteOpen(true);
    }
    setOpen(false);
  };

  const renderModals = () => (
    <>
      {deleteOpen && (
        <VmDeleteConfirmModal
          vm={vm}
          onClose={() => setDeleteOpen(false)}
          onSuccess={() => !isMenu && navigate('/vms')}
        />
      )}
      {attachExternalIpOpen && (
        <AttachExternalIpModal vm={vm} onClose={() => setAttachExternalIpOpen(false)} />
      )}
      {detachExternalIpOpen && externalIpAttachment && (
        <DetachExternalIpModal
          attachment={externalIpAttachment}
          onClose={() => setDetachExternalIpOpen(false)}
        />
      )}
    </>
  );

  const renderErrors = () => (
    <>
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
    </>
  );

  if (isMenu) {
    return (
      <>
        {renderModals()}
        <Dropdown
          isOpen={open}
          onOpenChange={setOpen}
          toggle={(ref) => (
            <MenuToggle
              ref={ref}
              variant="plain"
              onClick={() => setOpen((o) => !o)}
              aria-label={`Actions for ${vm.metadata?.name ?? vm.id}`}
            >
              <EllipsisVIcon />
            </MenuToggle>
          )}
          popperProps={{ position: 'right' }}
        >
          <Stack hasGutter>
            {renderErrors()}
            <StackItem>
              <DropdownList>
                <DropdownItem
                  component={(props) => <Link {...props} to={`/vms/${vm.id}`} />}
                  onClick={() => setOpen(false)}
                >
                  {t('View details')}
                </DropdownItem>
                <DropdownItem isDisabled={!canStart} onClick={onStart}>
                  {t('Start')}
                </DropdownItem>
                <DropdownItem isDisabled={!canStop} onClick={onStop}>
                  {t('Stop')}
                </DropdownItem>
                <DropdownItem isDisabled={!canRestart} onClick={onRestart}>
                  {t('Restart')}
                </DropdownItem>
                {!showDetachExternalIp && (
                  <DropdownItem
                    isDisabled={attachDisabled}
                    isLoading={attachLoading}
                    onClick={onAttachExternalIP}
                  >
                    {attachLabel}
                  </DropdownItem>
                )}
                {showDetachExternalIp && externalIpAttachment && (
                  <DropdownItem
                    isDisabled={detachDisabled}
                    isLoading={detachLoading}
                    onClick={onDetach}
                  >
                    {detachLabel}
                  </DropdownItem>
                )}
                <DeleteResourceButton isDropdown canDelete={canDelete} onClick={onDelete} />
              </DropdownList>
            </StackItem>
          </Stack>
        </Dropdown>
      </>
    );
  }

  return (
    <>
      {renderModals()}
      <Stack hasGutter>
        {renderErrors()}
        <StackItem>
          <Flex
            justifyContent={{ default: 'justifyContentFlexEnd' }}
            spaceItems={{ default: 'spaceItemsSm' }}
            flexWrap={{ default: 'wrap' }}
          >
            <Button variant="primary" icon={<PlayIcon />} isDisabled={!canStart} onClick={onStart}>
              {t('Start')}
            </Button>
            <Button variant="secondary" icon={<StopIcon />} isDisabled={!canStop} onClick={onStop}>
              {t('Stop')}
            </Button>
            <Button
              variant="secondary"
              icon={<SyncAltIcon />}
              isDisabled={!canRestart}
              onClick={onRestart}
            >
              {t('Restart')}
            </Button>
            {!showDetachExternalIp && (
              <Button
                variant="secondary"
                icon={<GlobeIcon />}
                isDisabled={attachDisabled}
                isLoading={attachLoading}
                onClick={onAttachExternalIP}
              >
                {attachLabel}
              </Button>
            )}
            {showDetachExternalIp && externalIpAttachment && (
              <Button
                variant="secondary"
                icon={<GlobeIcon />}
                isDisabled={detachDisabled}
                isLoading={detachLoading}
                onClick={onDetach}
              >
                {detachLabel}
              </Button>
            )}
            <DeleteResourceButton showIcon canDelete={canDelete} onClick={onDelete} />
          </Flex>
        </StackItem>
      </Stack>
    </>
  );
};

export default VmActionButtons;
