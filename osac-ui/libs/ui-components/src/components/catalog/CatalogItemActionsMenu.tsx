import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Dropdown, DropdownItem, DropdownList, MenuToggle } from '@patternfly/react-core';
import { EllipsisVIcon } from '@patternfly/react-icons/dist/esm/icons/ellipsis-v-icon';

import {
  BareMetalInstanceCatalogItems,
  ClusterCatalogItems,
  ComputeInstanceCatalogItems,
} from '@osac/types';
import { UserRole } from '@osac/ui-components/shellTypes';

import {
  type CatalogItem,
  catalogItemDetailsPath,
  catalogItemEditPath,
  getCatalogCreateActionPath,
  selectCatalogItemValue,
} from './catalogItemDisplay';
import { usePermissions } from '../../hooks/use-permissions';
import { useTranslation } from '../../hooks/useTranslation';
import BareMetalInstanceCatalogItemDeleteModal from '../BareMetalInstanceCatalogItem/Details/BareMetalInstanceCatalogItemDeleteModal';
import { useSetCatalogItemPublished } from '../CatalogItem/useSetCatalogItemPublished';
import ClusterCatalogItemDeleteModal from '../ClusterCatalogItem/Details/ClusterCatalogItemDeleteModal';
import DeleteResourceButton from '../Resource/DeleteResourceButton';
import VMCatalogItemDeleteModal from '../VMCatalogItem/Details/VMCatalogItemDeleteModal';

interface CatalogItemActionsMenuProps {
  item: CatalogItem;
  role: UserRole;
}

const CatalogItemActionsMenu = ({ item, role }: CatalogItemActionsMenuProps) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const { setPublished } = useSetCatalogItemPublished(item);
  const canUpdate = selectCatalogItemValue(item, {
    vm: usePermissions(ComputeInstanceCatalogItems, 'update'),
    cluster: usePermissions(ClusterCatalogItems, 'update'),
    bm: usePermissions(BareMetalInstanceCatalogItems, 'update'),
  }).allowed;
  const canDelete = selectCatalogItemValue(item, {
    vm: usePermissions(ComputeInstanceCatalogItems, 'delete'),
    cluster: usePermissions(ClusterCatalogItems, 'delete'),
    bm: usePermissions(BareMetalInstanceCatalogItems, 'delete'),
  }).allowed;
  const canManage = canUpdate && canDelete;

  const deleteModal = deleteOpen
    ? (() => {
        const modalProps = {
          onClose: () => setDeleteOpen(false),
          onSuccess: () => setDeleteOpen(false),
        };
        switch (item.$typeName) {
          case 'osac.public.v1.ComputeInstanceCatalogItem':
            return <VMCatalogItemDeleteModal item={item} {...modalProps} />;
          case 'osac.public.v1.ClusterCatalogItem':
            return <ClusterCatalogItemDeleteModal item={item} {...modalProps} />;
          case 'osac.public.v1.BareMetalInstanceCatalogItem':
            return <BareMetalInstanceCatalogItemDeleteModal item={item} {...modalProps} />;
        }
      })()
    : null;

  return (
    <>
      {deleteModal}
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
            aria-label={t('Actions for {{name}}', { name: item.metadata?.name || '' })}
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
          {canManage ? (
            <DropdownItem
              onClick={() => {
                setOpen(false);
                navigate(catalogItemEditPath(item));
              }}
            >
              {t('Edit')}
            </DropdownItem>
          ) : null}
          {canManage ? (
            <DropdownItem
              onClick={() => {
                setOpen(false);
                setPublished(!item.published);
              }}
            >
              {item.published ? t('Unpublish') : t('Publish')}
            </DropdownItem>
          ) : null}
          {canManage ? (
            <DeleteResourceButton
              isDropdown
              onClick={() => {
                setDeleteOpen(true);
                setOpen(false);
              }}
            />
          ) : null}
        </DropdownList>
      </Dropdown>
    </>
  );
};

export default CatalogItemActionsMenu;
