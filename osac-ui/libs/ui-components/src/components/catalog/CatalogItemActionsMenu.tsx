import { type ReactNode, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Dropdown, DropdownItem, DropdownList, MenuToggle } from '@patternfly/react-core';
import { EllipsisVIcon } from '@patternfly/react-icons/dist/esm/icons/ellipsis-v-icon';

import { UserRole } from '@osac/ui-components/shellTypes';

import { useTranslation } from '../../hooks/useTranslation';
import DeleteResourceButton from '../Resource/DeleteResourceButton';

export interface CatalogItemDeleteModalProps {
  onClose: VoidFunction;
  onSuccess: VoidFunction;
}

interface CatalogItemActionsMenuProps {
  name: string;
  published: boolean;
  detailsPath: string;
  launchPath: string;
  editPath: string;
  role: UserRole;
  canManage: boolean;
  onSetPublished: (published: boolean) => void;
  renderDeleteModal: (props: CatalogItemDeleteModalProps) => ReactNode;
}

const CatalogItemActionsMenu = ({
  name,
  published,
  detailsPath,
  launchPath,
  editPath,
  role,
  canManage,
  onSetPublished,
  renderDeleteModal,
}: CatalogItemActionsMenuProps) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const deleteModal = deleteOpen
    ? renderDeleteModal({
        onClose: () => setDeleteOpen(false),
        onSuccess: () => setDeleteOpen(false),
      })
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
            aria-label={t('Actions for {{name}}', { name })}
          >
            <EllipsisVIcon />
          </MenuToggle>
        )}
      >
        <DropdownList>
          <DropdownItem
            onClick={() => {
              setOpen(false);
              navigate(detailsPath);
            }}
          >
            {t('View details')}
          </DropdownItem>
          {role === 'tenant-admin' || role === 'tenant-user' ? (
            <DropdownItem
              isDisabled={!published}
              onClick={() => {
                setOpen(false);
                navigate(launchPath);
              }}
            >
              {t('Launch instance')}
            </DropdownItem>
          ) : null}
          {canManage ? (
            <DropdownItem
              onClick={() => {
                setOpen(false);
                navigate(editPath);
              }}
            >
              {t('Edit')}
            </DropdownItem>
          ) : null}
          {canManage ? (
            <DropdownItem
              onClick={() => {
                setOpen(false);
                onSetPublished(!published);
              }}
            >
              {published ? t('Unpublish') : t('Publish')}
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
