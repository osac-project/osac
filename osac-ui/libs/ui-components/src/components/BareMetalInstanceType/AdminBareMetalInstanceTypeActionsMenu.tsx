import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Dropdown, DropdownItem, DropdownList, MenuToggle } from '@patternfly/react-core';
import { EllipsisVIcon } from '@patternfly/react-icons/dist/esm/icons/ellipsis-v-icon';

import type { BareMetalInstanceType as PrivateBareMetalInstanceType } from '@osac/types/private';
import DeleteResourceButton from '@osac/ui-components/components/Resource/DeleteResourceButton';

import BareMetalInstanceTypeDeleteModal from './BareMetalInstanceTypeDeleteModal';
import { useTranslation } from '../../hooks/useTranslation';

interface AdminBareMetalInstanceTypeActionsMenuProps {
  bareMetalInstanceType: PrivateBareMetalInstanceType;
}

const AdminBareMetalInstanceTypeActionsMenu = ({
  bareMetalInstanceType,
}: AdminBareMetalInstanceTypeActionsMenuProps) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);

  const name = bareMetalInstanceType.metadata?.name || bareMetalInstanceType.id;

  return (
    <>
      {deleteOpen && (
        <BareMetalInstanceTypeDeleteModal
          instanceType={bareMetalInstanceType}
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
            onClick={() => setOpen((value) => !value)}
            aria-label={t('Actions for {{name}}', { name })}
          >
            <EllipsisVIcon />
          </MenuToggle>
        )}
        popperProps={{ position: 'right' }}
      >
        <DropdownList>
          <DropdownItem
            value="edit"
            onClick={() => {
              navigate(
                `/admin/infrastructure/baremetal-instance-types/${bareMetalInstanceType.id}/edit`,
              );
              setOpen(false);
            }}
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

export default AdminBareMetalInstanceTypeActionsMenu;
