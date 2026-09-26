import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Dropdown, DropdownItem, DropdownList, MenuToggle } from '@patternfly/react-core';
import { EllipsisVIcon } from '@patternfly/react-icons/dist/esm/icons/ellipsis-v-icon';

import { type RoleBinding, RoleBindings } from '@osac/types';
import { useDeleteResource } from '@osac/ui-components/api/use-resource';
import DeleteResourceButton from '@osac/ui-components/components/Resource/DeleteResourceButton';
import DeleteResourceModal from '@osac/ui-components/components/Resource/DeleteResourceModal';

import { useTranslation } from '../../hooks/useTranslation';

interface RoleBindingActionsMenuProps {
  roleBinding: RoleBinding;
}

const RoleBindingActionsMenu = ({ roleBinding }: RoleBindingActionsMenuProps) => {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const navigate = useNavigate();
  const deleteRoleBinding = useDeleteResource(RoleBindings);

  return (
    <>
      {deleteOpen && (
        <DeleteResourceModal
          resourceName={roleBinding.metadata?.name || roleBinding.id}
          label={t(
            'This permanently deletes the role binding. Users will lose the permissions granted by this binding. This action cannot be undone.',
          )}
          errorLabel={t('Failed to delete role binding')}
          onClose={() => setDeleteOpen(false)}
          onSuccess={() => setDeleteOpen(false)}
          mutation={deleteRoleBinding}
          variables={{ id: roleBinding.id }}
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
            aria-label={t('Actions')}
          >
            <EllipsisVIcon />
          </MenuToggle>
        )}
        popperProps={{ position: 'right' }}
      >
        <DropdownList>
          <DropdownItem onClick={() => navigate(`${roleBinding.id}/edit`)}>
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

export default RoleBindingActionsMenu;
