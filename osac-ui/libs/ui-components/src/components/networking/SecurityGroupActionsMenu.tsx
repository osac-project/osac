import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Dropdown, DropdownItem, DropdownList, MenuToggle } from '@patternfly/react-core';
import { EllipsisVIcon } from '@patternfly/react-icons/dist/esm/icons/ellipsis-v-icon';

import type { SecurityGroup } from '@osac/types';
import DeleteResourceButton from '@osac/ui-components/components/Resource/DeleteResourceButton';

import { securityGroupDetailsPath } from './security-group-list-utils';
import SecurityGroupDeleteConfirmModal from './SecurityGroupDeleteConfirmModal';
import { isDefaultNetworkingResource } from '../../api/v1/networking';
import { useTranslation } from '../../hooks/useTranslation';
import { defaultResourceDeleteReason } from '../../utils/networking';

interface SecurityGroupActionsMenuProps {
  securityGroup: SecurityGroup;
}

const SecurityGroupActionsMenu = ({ securityGroup }: SecurityGroupActionsMenuProps) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);

  const name = securityGroup.metadata?.name ?? securityGroup.id;
  const isDefault = isDefaultNetworkingResource(securityGroup);
  const canDelete = !isDefault;

  return (
    <>
      {deleteOpen && (
        <SecurityGroupDeleteConfirmModal
          securityGroup={securityGroup}
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
            isExpanded={open}
          >
            <EllipsisVIcon />
          </MenuToggle>
        )}
        popperProps={{ position: 'right' }}
      >
        <DropdownList>
          <DropdownItem
            value="view-details"
            onClick={() => navigate(securityGroupDetailsPath(securityGroup.id))}
          >
            {t('View details')}
          </DropdownItem>
          <DeleteResourceButton
            isDropdown
            value="delete"
            canDelete={canDelete}
            disabledReason={isDefault ? defaultResourceDeleteReason(t) : undefined}
            onClick={() => {
              if (!canDelete) {
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

export default SecurityGroupActionsMenu;
