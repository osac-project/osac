import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Button, Flex } from '@patternfly/react-core';
import ExternalLinkAltIcon from '@patternfly/react-icons/dist/esm/icons/external-link-alt-icon';
import PlayIcon from '@patternfly/react-icons/dist/esm/icons/play-icon';
import StopIcon from '@patternfly/react-icons/dist/esm/icons/stop-icon';
import SyncAltIcon from '@patternfly/react-icons/dist/esm/icons/sync-alt-icon';

import type { ComputeInstance } from '@osac/types';
import { ComputeInstanceState } from '@osac/types';
import DeleteResourceButton from '@osac/ui-components/components/Resource/DeleteResourceButton';

import { useVmPowerAction } from '../useVmPowerAction';
import VmDeleteConfirmModal from '../VmDeleteConfirmModal';

interface VmDetailsActionButtonsProps {
  vm: ComputeInstance;
  /** Omitted when the user can't open the console, for example a cloud provider admin. */
  onOpenConsole?: VoidFunction;
}

const VmDetailsActionButtons = ({ vm, onOpenConsole }: VmDetailsActionButtonsProps) => {
  const navigate = useNavigate();
  const [deleteOpen, setDeleteOpen] = useState(false);
  const { runPowerAction } = useVmPowerAction();

  const state = vm.status?.state;
  const canStart = state === ComputeInstanceState.STOPPED;
  const canStop = state === ComputeInstanceState.RUNNING || state === ComputeInstanceState.PAUSED;
  const canRestart =
    state === ComputeInstanceState.RUNNING ||
    state === ComputeInstanceState.PAUSED ||
    state === ComputeInstanceState.FAILED;
  const canOpenConsole = state === ComputeInstanceState.RUNNING;
  const canDelete = state !== ComputeInstanceState.DELETING;

  return (
    <>
      {deleteOpen && (
        <VmDeleteConfirmModal
          vm={vm}
          onClose={() => setDeleteOpen(false)}
          onSuccess={() => navigate('/vms')}
        />
      )}
      <Flex
        justifyContent={{ default: 'justifyContentFlexEnd' }}
        spaceItems={{ default: 'spaceItemsSm' }}
        flexWrap={{ default: 'wrap' }}
      >
        {onOpenConsole ? (
          <Button
            variant="primary"
            icon={<ExternalLinkAltIcon />}
            isDisabled={!canOpenConsole}
            onClick={onOpenConsole}
          >
            Open console
          </Button>
        ) : null}
        <Button
          variant="secondary"
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
