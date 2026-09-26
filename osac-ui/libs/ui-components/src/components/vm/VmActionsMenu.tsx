import { useState } from 'react';
import { Dropdown, DropdownItem, DropdownList, MenuToggle } from '@patternfly/react-core';
import { EllipsisVIcon } from '@patternfly/react-icons/dist/esm/icons/ellipsis-v-icon';

import type { ComputeInstance } from '@osac/types';
import { ComputeInstanceState } from '@osac/types';
import DeleteResourceButton from '@osac/ui-components/components/Resource/DeleteResourceButton';

import VmDeleteConfirmModal from './DetailsPage/VmDeleteConfirmModal';
import { useVmPowerAction } from './useVmPowerAction';

interface VmActionsMenuProps {
  vm: ComputeInstance;
}

export const VmActionsMenu = ({ vm }: VmActionsMenuProps) => {
  const [open, setOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const { runPowerAction } = useVmPowerAction();

  const name = vm.metadata?.name ?? vm.id;
  const state = vm.status?.state;
  const canStart = state === ComputeInstanceState.STOPPED;
  const canStop = state === ComputeInstanceState.RUNNING || state === ComputeInstanceState.PAUSED;
  const canRestart =
    state === ComputeInstanceState.RUNNING || state === ComputeInstanceState.PAUSED;
  const canDelete = state !== ComputeInstanceState.DELETING;

  return (
    <>
      {deleteOpen && (
        <VmDeleteConfirmModal
          vm={vm}
          onClose={() => setDeleteOpen(false)}
          onSuccess={() => setDeleteOpen(false)}
        />
      )}
      <Dropdown
        isOpen={open}
        onOpenChange={setOpen}
        toggle={(ref) => (
          <MenuToggle
            ref={ref}
            variant="plain"
            onClick={() => setOpen((o) => !o)}
            aria-label={`Actions for ${name}`}
          >
            <EllipsisVIcon />
          </MenuToggle>
        )}
        popperProps={{ position: 'right' }}
      >
        <DropdownList>
          <DropdownItem
            value="start"
            isDisabled={!canStart}
            onClick={() => {
              if (!canStart) {
                return;
              }
              runPowerAction(vm.id, name, 'start');
              setOpen(false);
            }}
          >
            Start
          </DropdownItem>
          <DropdownItem
            value="stop"
            isDisabled={!canStop}
            onClick={() => {
              if (!canStop) {
                return;
              }
              runPowerAction(vm.id, name, 'stop');
              setOpen(false);
            }}
          >
            Stop
          </DropdownItem>
          <DropdownItem
            value="restart"
            isDisabled={!canRestart}
            onClick={() => {
              if (!canRestart) {
                return;
              }
              runPowerAction(vm.id, name, 'restart');
              setOpen(false);
            }}
          >
            Restart
          </DropdownItem>
          <DeleteResourceButton
            isDropdown
            canDelete={canDelete}
            onClick={() => {
              if (!canDelete) {
                return;
              }
              setDeleteOpen(true);
              setOpen(false);
            }}
          />
        </DropdownList>
      </Dropdown>
    </>
  );
};
