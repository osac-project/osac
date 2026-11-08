import { useState } from 'react';
import { Dropdown, DropdownItem, DropdownList, MenuToggle } from '@patternfly/react-core';
import { EllipsisVIcon } from '@patternfly/react-icons/dist/esm/icons/ellipsis-v-icon';

import type { Subnet } from '@osac/types';

import { useSubnetDeleteDisabledReason } from './deleteDisabledReasons';
import { useDeleteSubnet } from '../../api/v1/networking';
import { useTranslation } from '../../hooks/useTranslation';
import DeleteResourceModal from '../Resource/DeleteResourceModal';

interface SubnetActionsMenuProps {
  subnet: Subnet;
}

const SubnetActionsMenu = ({ subnet }: SubnetActionsMenuProps) => {
  const { t } = useTranslation();
  const [isOpen, setIsOpen] = useState(false);
  const [isDeleteOpen, setIsDeleteOpen] = useState(false);
  const deleteSubnet = useDeleteSubnet();
  const name = subnet.metadata?.name || subnet.id;
  // Default and automatic subnets, and subnets that workloads use, cannot be deleted.
  const deleteDisabledReason = useSubnetDeleteDisabledReason(subnet);

  return (
    <>
      <Dropdown
        isOpen={isOpen}
        onOpenChange={setIsOpen}
        onSelect={() => setIsOpen(false)}
        toggle={(ref) => (
          <MenuToggle
            ref={ref}
            variant="plain"
            onClick={() => setIsOpen((wasOpen) => !wasOpen)}
            isExpanded={isOpen}
            aria-label={t('Actions for {{name}}', { name })}
          >
            <EllipsisVIcon />
          </MenuToggle>
        )}
        popperProps={{ position: 'right' }}
      >
        <DropdownList>
          <DropdownItem
            isAriaDisabled={Boolean(deleteDisabledReason)}
            tooltipProps={deleteDisabledReason ? { content: deleteDisabledReason } : undefined}
            onClick={() => {
              if (deleteDisabledReason) {
                return;
              }
              setIsDeleteOpen(true);
              setIsOpen(false);
            }}
          >
            {t('Delete subnet')}
          </DropdownItem>
        </DropdownList>
      </Dropdown>
      {isDeleteOpen ? (
        <DeleteResourceModal
          resourceName={name}
          label={t(
            'The subnet cannot be deleted while a workload uses it. This action cannot be undone.',
          )}
          errorLabel={t('Failed to delete subnet')}
          onClose={() => setIsDeleteOpen(false)}
          onSuccess={() => setIsDeleteOpen(false)}
          mutation={deleteSubnet}
          variables={subnet.id}
        />
      ) : null}
    </>
  );
};

export default SubnetActionsMenu;
