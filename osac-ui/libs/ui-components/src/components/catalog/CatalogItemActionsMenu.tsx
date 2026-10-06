import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Dropdown, DropdownItem, DropdownList, MenuToggle } from '@patternfly/react-core';
import { EllipsisVIcon } from '@patternfly/react-icons/dist/esm/icons/ellipsis-v-icon';

import { UserRole } from '@osac/ui-components/shellTypes';

import {
  type CatalogItem,
  catalogItemDetailsPath,
  getCatalogCreateActionPath,
} from './catalogItemDisplay';
import { useTranslation } from '../../hooks/useTranslation';

interface CatalogItemActionsMenuProps {
  item: CatalogItem;
  role: UserRole;
}

const CatalogItemActionsMenu = ({ item, role }: CatalogItemActionsMenuProps) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);

  return (
    <Dropdown
      isOpen={open}
      onOpenChange={setOpen}
      popperProps={{ position: 'right' }}
      toggle={(ref) => (
        <MenuToggle
          ref={ref}
          variant="plain"
          onClick={(event) => {
            event.stopPropagation();
            setOpen((isOpen) => !isOpen);
          }}
          aria-label={t('Actions for {{name}}', { name: item.metadata?.name ?? item.id })}
        >
          <EllipsisVIcon />
        </MenuToggle>
      )}
    >
      <DropdownList>
        <DropdownItem
          onClick={() => {
            setOpen(false);
            navigate(catalogItemDetailsPath(item));
          }}
        >
          {t('View details')}
        </DropdownItem>
        {role === 'tenant-admin' || role === 'tenant-user' ? (
          <DropdownItem
            isDisabled={!item.published}
            onClick={() => {
              setOpen(false);
              navigate(getCatalogCreateActionPath(item));
            }}
          >
            {t('Launch instance')}
          </DropdownItem>
        ) : null}
      </DropdownList>
    </Dropdown>
  );
};

export default CatalogItemActionsMenu;
