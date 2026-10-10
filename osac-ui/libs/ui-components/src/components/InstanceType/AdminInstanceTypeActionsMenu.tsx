import { useState } from 'react';
import { Dropdown, DropdownItem, DropdownList, MenuToggle } from '@patternfly/react-core';
import { EllipsisVIcon } from '@patternfly/react-icons/dist/esm/icons/ellipsis-v-icon';

import { InstanceTypeState, type InstanceType as PrivateInstanceType } from '@osac/types/private';
import DeleteResourceButton from '@osac/ui-components/components/Resource/DeleteResourceButton';

import InstanceTypeDeleteConfirmModal from './InstanceTypeDeleteConfirmModal';
import {
  getInstanceTypeLifecycleActions,
  useInstanceTypeLifecycleAction,
} from './useInstanceTypeLifecycleAction';
import { useTranslation } from '../../hooks/useTranslation';

interface AdminInstanceTypeActionsMenuProps {
  instanceType: PrivateInstanceType;
}

const AdminInstanceTypeActionsMenu = ({ instanceType }: AdminInstanceTypeActionsMenuProps) => {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const { runLifecycleAction } = useInstanceTypeLifecycleAction();

  const { canDeprecate, canObsolete, canReactivate, canDelete } = getInstanceTypeLifecycleActions(
    instanceType.spec?.state,
  );

  const name = instanceType.metadata?.name || instanceType.id;

  return (
    <>
      {deleteOpen && (
        <InstanceTypeDeleteConfirmModal
          instanceType={instanceType}
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
                runLifecycleAction(instanceType.id, InstanceTypeState.DEPRECATED);
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
                runLifecycleAction(instanceType.id, InstanceTypeState.OBSOLETE);
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
                runLifecycleAction(instanceType.id, InstanceTypeState.ACTIVE);
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

export default AdminInstanceTypeActionsMenu;
