import { useState } from 'react';
import { Dropdown, DropdownList, MenuToggle } from '@patternfly/react-core';
import { EllipsisVIcon } from '@patternfly/react-icons/dist/esm/icons/ellipsis-v-icon';

import type { Volume } from '@osac/types';
import { VolumeState } from '@osac/types';
import DeleteResourceButton from '@osac/ui-components/components/Resource/DeleteResourceButton';

import VolumeDeleteConfirmModal from './VolumeDeleteConfirmModal';
import { useTranslation } from '../../hooks/useTranslation';

interface VolumeActionsMenuProps {
  volume: Volume;
}

const VolumeActionsMenu = ({ volume }: VolumeActionsMenuProps) => {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);

  const isDeleting = volume.status?.state === VolumeState.DELETING;

  const name = volume.metadata?.name ?? volume.id;

  return (
    <>
      {deleteOpen && (
        <VolumeDeleteConfirmModal
          volume={volume}
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
            aria-label={t('Actions for {{name}}', { name })}
          >
            <EllipsisVIcon />
          </MenuToggle>
        )}
        popperProps={{ position: 'right' }}
      >
        <DropdownList>
          <DeleteResourceButton
            isDropdown
            canDelete={!isDeleting}
            value="delete"
            isDisabled={isDeleting}
            onClick={() => {
              if (isDeleting) {
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

export default VolumeActionsMenu;
