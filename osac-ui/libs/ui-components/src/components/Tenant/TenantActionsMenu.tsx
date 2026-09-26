import { useState } from 'react';
import { Dropdown, DropdownList, MenuToggle } from '@patternfly/react-core';
import { EllipsisVIcon } from '@patternfly/react-icons/dist/esm/icons/ellipsis-v-icon';

import { type Tenant, Tenants } from '@osac/types/private';
import { useDeleteResource } from '@osac/ui-components/api/use-resource';
import DeleteResourceButton from '@osac/ui-components/components/Resource/DeleteResourceButton';
import DeleteResourceModal from '@osac/ui-components/components/Resource/DeleteResourceModal';

import { useTranslation } from '../../hooks/useTranslation';

interface TenantActionsMenuProps {
  tenant: Tenant;
}

const TenantActionsMenu = ({ tenant }: TenantActionsMenuProps) => {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const deleteTenant = useDeleteResource(Tenants);
  const tenantName = tenant.metadata?.name ?? tenant.id;

  return (
    <>
      {deleteOpen && (
        <DeleteResourceModal
          resourceName={tenantName}
          label={t(
            'This permanently deletes the tenant and all its resources. This action cannot be undone.',
          )}
          errorLabel={t('Failed to delete tenant')}
          onClose={() => setDeleteOpen(false)}
          onSuccess={() => setDeleteOpen(false)}
          mutation={deleteTenant}
          variables={{ id: tenant.id }}
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
            aria-label={t('Actions for {{name}}', { name: tenantName })}
          >
            <EllipsisVIcon />
          </MenuToggle>
        )}
        popperProps={{ position: 'right' }}
      >
        <DropdownList>
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

export default TenantActionsMenu;
