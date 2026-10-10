import { useState } from 'react';
import { Dropdown, DropdownItem, DropdownList, MenuToggle } from '@patternfly/react-core';
import { EllipsisVIcon } from '@patternfly/react-icons/dist/esm/icons/ellipsis-v-icon';

import type { BareMetalInstance } from '@osac/types';
import DeleteResourceButton from '@osac/ui-components/components/Resource/DeleteResourceButton';

import BareMetalDeleteConfirmModal from './BareMetalDeleteConfirmModal';
import { useBareMetalActions } from './useBareMetalActions';
import { useTranslation } from '../../hooks/useTranslation';

interface BareMetalActionsMenuProps {
  instance: BareMetalInstance;
  onDeleted?: () => void;
}

export const BareMetalActionsMenu = ({ instance, onDeleted }: BareMetalActionsMenuProps) => {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);

  const { canStart, canStop, canRestart, canDelete, start, stop, restart } =
    useBareMetalActions(instance);

  return (
    <>
      {deleteOpen && (
        <BareMetalDeleteConfirmModal
          instance={instance}
          onClose={() => setDeleteOpen(false)}
          onSuccess={() => {
            setDeleteOpen(false);
            onDeleted?.();
          }}
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
            aria-label={t('Actions for {{name}}', { name: instance.metadata?.name ?? instance.id })}
          >
            <EllipsisVIcon />
          </MenuToggle>
        )}
        popperProps={{ position: 'right' }}
      >
        <DropdownList>
          <DropdownItem
            isDisabled={!canStart}
            onClick={() => {
              start();
              setOpen(false);
            }}
          >
            {t('Start')}
          </DropdownItem>
          <DropdownItem
            isDisabled={!canStop}
            onClick={() => {
              stop();
              setOpen(false);
            }}
          >
            {t('Stop')}
          </DropdownItem>
          <DropdownItem
            isDisabled={!canRestart}
            onClick={() => {
              restart();
              setOpen(false);
            }}
          >
            {t('Restart')}
          </DropdownItem>
          <DeleteResourceButton
            isDropdown
            canDelete={canDelete}
            onClick={() => {
              if (canDelete) {
                setDeleteOpen(true);
                setOpen(false);
              }
            }}
          />
        </DropdownList>
      </Dropdown>
    </>
  );
};
