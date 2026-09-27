import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Dropdown, DropdownItem, DropdownList, MenuToggle } from '@patternfly/react-core';
import { EllipsisVIcon } from '@patternfly/react-icons/dist/esm/icons/ellipsis-v-icon';

import type { Volume } from '@osac/types';
import { VolumeState } from '@osac/types';

import VolumeDeleteConfirmModal from './VolumeDeleteConfirmModal';
import { useTranslation } from '../../hooks/useTranslation';

interface VolumeActionsMenuProps {
  volume: Volume;
}

const VolumeActionsMenu = ({ volume }: VolumeActionsMenuProps) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);

  const state = volume.status?.state;
  const canEdit =
    state === VolumeState.AVAILABLE ||
    state === VolumeState.CREATING ||
    state === VolumeState.FAILED;
  const canDelete = state === VolumeState.AVAILABLE || state === VolumeState.FAILED;

  if (!canEdit && !canDelete) {
    return null;
  }

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
          {canEdit && (
            <DropdownItem
              value="edit"
              onClick={() => {
                navigate(`/storage/volumes/${volume.id}/edit`);
                setOpen(false);
              }}
            >
              {t('Edit')}
            </DropdownItem>
          )}
          {canDelete && (
            <DropdownItem
              value="delete"
              onClick={() => {
                setDeleteOpen(true);
                setOpen(false);
              }}
            >
              {t('Delete')}
            </DropdownItem>
          )}
        </DropdownList>
      </Dropdown>
    </>
  );
};

export default VolumeActionsMenu;
