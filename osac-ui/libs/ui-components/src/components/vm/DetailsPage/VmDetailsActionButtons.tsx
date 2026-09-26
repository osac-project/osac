import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Button, Flex } from '@patternfly/react-core';
import GlobeIcon from '@patternfly/react-icons/dist/esm/icons/globe-icon';
import PlayIcon from '@patternfly/react-icons/dist/esm/icons/play-icon';
import StopIcon from '@patternfly/react-icons/dist/esm/icons/stop-icon';
import SyncAltIcon from '@patternfly/react-icons/dist/esm/icons/sync-alt-icon';

import type { ComputeInstance } from '@osac/types';
import { ComputeInstanceState } from '@osac/types';
import DeleteResourceButton from '@osac/ui-components/components/Resource/DeleteResourceButton';

import AttachExternalIpModal from './AttachExternalIpModal';
import VmDeleteConfirmModal from './VmDeleteConfirmModal';
import { useTranslation } from '../../../hooks/useTranslation';
import { useVmPowerAction } from '../useVmPowerAction';

interface VmDetailsActionButtonsProps {
  vm: ComputeInstance;
}

const VmDetailsActionButtons = ({ vm }: VmDetailsActionButtonsProps) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [attachExternalIpOpen, setAttachExternalIpOpen] = useState(false);
  const { runPowerAction } = useVmPowerAction();

  const name = vm.metadata?.name ?? vm.id;
  const state = vm.status?.state;
  const canStart = state === ComputeInstanceState.STOPPED;
  const canStop = state === ComputeInstanceState.RUNNING || state === ComputeInstanceState.PAUSED;
  const canRestart =
    state === ComputeInstanceState.RUNNING || state === ComputeInstanceState.PAUSED;
  const canDelete = state !== ComputeInstanceState.DELETING;
  const canAttachExternalIp =
    state === ComputeInstanceState.RUNNING && !vm.status?.externalIpAddress;

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
              runPowerAction(vm.id, name, 'start');
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
              runPowerAction(vm.id, name, 'stop');
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
              runPowerAction(vm.id, name, 'restart');
            }
          }}
        >
          Restart
        </Button>
        <Button
          variant="secondary"
          icon={<GlobeIcon />}
          isDisabled={!canAttachExternalIp}
          onClick={() => {
            if (canAttachExternalIp) {
              setAttachExternalIpOpen(true);
            }
          }}
        >
          {t('Attach external IP')}
        </Button>
        <DeleteResourceButton
          showIcon
          canDelete={canDelete}
          onClick={() => {
            if (canDelete) {
              setDeleteOpen(true);
            }
          }}
        />
      </Flex>
    </>
  );
};

export default VmDetailsActionButtons;
