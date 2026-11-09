import { useState } from 'react';
import { Link } from 'react-router-dom';
import {
  Button,
  Dropdown,
  DropdownItem,
  DropdownList,
  MenuToggle,
  Stack,
  StackItem,
} from '@patternfly/react-core';
import { EllipsisVIcon } from '@patternfly/react-icons/dist/esm/icons/ellipsis-v-icon';

import type { ComputeInstance } from '@osac/types';
import { ComputeInstanceState } from '@osac/types';
import DeleteResourceButton from '@osac/ui-components/components/Resource/DeleteResourceButton';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import { useVmPowerAction } from '../useVmPowerAction';
import VmDeleteConfirmModal from '../VmDeleteConfirmModal';

interface VmActionsMenuProps {
  vm: ComputeInstance;
}

const VmActionsMenu = ({ vm }: VmActionsMenuProps) => {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
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

  const onDelete = () => {
    if (canDelete) {
      setDeleteOpen(true);
    }
    setOpen(false);
  };

  return (
    <>
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
          <StackItem>
            <DropdownList>
              <DropdownItem
                component={(props) => <Link {...props} to={`/vms/${encodeURIComponent(vm.id)}`} />}
                onClick={() => setOpen(false)}
              >
                {t('View details')}
              </DropdownItem>
              <DropdownItem
                isDisabled={!canOpenConsole}
                onClick={() => setOpen(false)}
                component={(props) => (
                  <Button
                    {...props}
                    component="a"
                    variant="link"
                    href={`/vms/${encodeURIComponent(vm.id)}/console`}
                    target="_blank"
                    rel="noopener noreferrer"
                  />
                )}
              >
                {t('Open console')}
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
              <DeleteResourceButton isDropdown canDelete={canDelete} onClick={onDelete} />
            </DropdownList>
          </StackItem>
        </Stack>
      </Dropdown>
      {deleteOpen && (
        <VmDeleteConfirmModal
          vm={vm}
          onClose={() => setDeleteOpen(false)}
          onSuccess={() => setDeleteOpen(false)}
        />
      )}
    </>
  );
};

export default VmActionsMenu;
