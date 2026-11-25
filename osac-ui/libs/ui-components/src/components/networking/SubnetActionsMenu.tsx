import { useState } from 'react';
import { Dropdown, DropdownList, MenuToggle } from '@patternfly/react-core';
import { EllipsisVIcon } from '@patternfly/react-icons/dist/esm/icons/ellipsis-v-icon';

import { type Subnet, Subnets } from '@osac/types';
import { useDeleteResource } from '@osac/ui-components/api/use-resource';

import { subnetDeleteGuard } from './subnet-delete-reason';
import { useTranslation } from '../../hooks/useTranslation';
import DeleteResourceButton from '../Resource/DeleteResourceButton';
import DeleteResourceModal from '../Resource/DeleteResourceModal';

interface SubnetActionsMenuProps {
  subnet: Subnet;
  /** Workloads attached to the subnet; `undefined` when they could not be loaded. */
  workloadNames?: readonly string[];
}

const SubnetActionsMenu = ({ subnet, workloadNames }: SubnetActionsMenuProps) => {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const deleteSubnet = useDeleteResource(Subnets);

  const name = subnet.metadata?.name ?? subnet.id;
  const { canDelete, disabledReason } = subnetDeleteGuard(t, { subnet, workloadNames });

  return (
    <>
      {deleteOpen && (
        <DeleteResourceModal
          resourceName={name}
          label={t('This will permanently delete the subnet. This action cannot be undone.')}
          errorLabel={t('Failed to delete subnet')}
          onClose={() => setDeleteOpen(false)}
          onSuccess={() => setDeleteOpen(false)}
          mutation={deleteSubnet}
          variables={{ id: subnet.id }}
        />
      )}
      <Dropdown
        isOpen={open}
        onOpenChange={setOpen}
        toggle={(ref) => (
          <MenuToggle
            ref={ref}
            variant="plain"
            onClick={() => setOpen((isOpen) => !isOpen)}
            aria-label={t('Actions for {{name}}', { name })}
            isExpanded={open}
          >
            <EllipsisVIcon />
          </MenuToggle>
        )}
        popperProps={{ position: 'right' }}
      >
        <DropdownList>
          <DeleteResourceButton
            isDropdown
            value="delete"
            canDelete={canDelete}
            disabledReason={disabledReason}
            onClick={() => {
              if (!canDelete) {
                return;
              }
              setDeleteOpen(true);
              setOpen(false);
            }}
          >
            {t('Delete subnet')}
          </DeleteResourceButton>
        </DropdownList>
      </Dropdown>
    </>
  );
};

export default SubnetActionsMenu;
