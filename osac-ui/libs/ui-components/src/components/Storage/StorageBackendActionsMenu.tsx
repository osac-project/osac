import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Dropdown, DropdownItem, DropdownList, MenuToggle } from '@patternfly/react-core';
import { EllipsisVIcon } from '@patternfly/react-icons/dist/esm/icons/ellipsis-v-icon';

import type { StorageBackend } from '@osac/types/private';
import DeleteResourceButton from '@osac/ui-components/components/Resource/DeleteResourceButton';

import StorageBackendDeleteConfirmModal from './StorageBackendDeleteConfirmModal';
import { useTranslation } from '../../hooks/useTranslation';

interface StorageBackendActionsMenuProps {
  backend: StorageBackend;
}

const StorageBackendActionsMenu = ({ backend }: StorageBackendActionsMenuProps) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);

  const backendName = backend.metadata?.name ?? backend.id;

  return (
    <>
      {deleteOpen && (
        <StorageBackendDeleteConfirmModal
          backend={backend}
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
            isExpanded={open}
            aria-label={t('Actions for {{name}}', { name: backendName })}
          >
            <EllipsisVIcon />
          </MenuToggle>
        )}
        popperProps={{ position: 'right' }}
      >
        <DropdownList>
          <DropdownItem
            onClick={() => navigate(`/admin/infrastructure/storage/backends/${backend.id}/edit`)}
          >
            {t('Edit')}
          </DropdownItem>
          <DeleteResourceButton
            isDropdown
            onClick={() => {
              setDeleteOpen(true);
              setOpen(false);
            }}
          />
        </DropdownList>
      </Dropdown>
    </>
  );
};

export default StorageBackendActionsMenu;
