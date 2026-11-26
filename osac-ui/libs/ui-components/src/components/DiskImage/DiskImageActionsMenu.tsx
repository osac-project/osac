import { useState } from 'react';
import { Dropdown, DropdownItem, DropdownList, MenuToggle } from '@patternfly/react-core';
import { EllipsisVIcon } from '@patternfly/react-icons/dist/esm/icons/ellipsis-v-icon';

import { type DiskImage, DiskImageLifecycle } from '@osac/types';
import DeleteResourceButton from '@osac/ui-components/components/Resource/DeleteResourceButton';

import DiskImageDeleteConfirmModal from './DiskImageDeleteConfirmModal';
import {
  getDiskImageLifecycleActions,
  useDiskImageLifecycleAction,
} from './useDiskImageLifecycleAction';
import { useTranslation } from '../../hooks/useTranslation';

interface DiskImageActionsMenuProps {
  diskImage: DiskImage;
}

const DiskImageActionsMenu = ({ diskImage }: DiskImageActionsMenuProps) => {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const { runLifecycleAction } = useDiskImageLifecycleAction();

  const { canDeprecate, canObsolete, canReactivate, canDelete } = getDiskImageLifecycleActions(
    diskImage.spec?.lifecycle,
  );

  if (!canDeprecate && !canObsolete && !canReactivate && !canDelete) {
    return null;
  }

  const name = diskImage.metadata?.name || diskImage.id;

  return (
    <>
      {deleteOpen && (
        <DiskImageDeleteConfirmModal
          diskImage={diskImage}
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
          {canDeprecate && (
            <DropdownItem
              value="deprecate"
              onClick={() => {
                runLifecycleAction(diskImage.id, DiskImageLifecycle.DEPRECATED);
                setOpen(false);
              }}
            >
              {t('Deprecate')}
            </DropdownItem>
          )}
          {canObsolete && (
            <DropdownItem
              value="obsolete"
              onClick={() => {
                runLifecycleAction(diskImage.id, DiskImageLifecycle.OBSOLETE);
                setOpen(false);
              }}
            >
              {t('Obsolete')}
            </DropdownItem>
          )}
          {canReactivate && (
            <DropdownItem
              value="reactivate"
              onClick={() => {
                runLifecycleAction(diskImage.id, DiskImageLifecycle.AVAILABLE);
                setOpen(false);
              }}
            >
              {t('Reactivate')}
            </DropdownItem>
          )}
          {canDelete && (
            <DeleteResourceButton
              isDropdown
              onClick={() => {
                setDeleteOpen(true);
                setOpen(false);
              }}
            />
          )}
        </DropdownList>
      </Dropdown>
    </>
  );
};

export default DiskImageActionsMenu;
