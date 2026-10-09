import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Divider, Dropdown, DropdownItem, DropdownList, MenuToggle } from '@patternfly/react-core';
import { EllipsisVIcon } from '@patternfly/react-icons/dist/esm/icons/ellipsis-v-icon';

import type { ExternalIPPool } from '@osac/types/private';
import DeleteResourceButton from '@osac/ui-components/components/Resource/DeleteResourceButton';

import ExternalIpPoolDeleteConfirmModal from './ExternalIpPoolDeleteConfirmModal';
import { useTranslation } from '../../hooks/useTranslation';

interface ExternalIpPoolActionsMenuProps {
  pool: ExternalIPPool;
}

const ExternalIpPoolActionsMenu = ({ pool }: ExternalIpPoolActionsMenuProps) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);

  const poolName = pool.metadata?.name ?? pool.id;

  return (
    <>
      {deleteOpen && (
        <ExternalIpPoolDeleteConfirmModal
          pool={pool}
          onClose={() => setDeleteOpen(false)}
          onSuccess={() => setDeleteOpen(false)}
        />
      )}
      <Dropdown
        isOpen={open}
        onOpenChange={setOpen}
        onSelect={() => setOpen(false)}
        toggle={(ref) => (
          <MenuToggle
            ref={ref}
            variant="plain"
            onClick={() => setOpen((o) => !o)}
            isExpanded={open}
            aria-label={t('Actions for {{name}}', { name: poolName })}
          >
            <EllipsisVIcon />
          </MenuToggle>
        )}
        popperProps={{ position: 'right' }}
      >
        <DropdownList>
          <DropdownItem
            value="view-details"
            onClick={() => navigate(`/admin/infrastructure/external-ip-pools/${pool.id}`)}
          >
            {t('View details')}
          </DropdownItem>
          <Divider component="li" />
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

export default ExternalIpPoolActionsMenu;
