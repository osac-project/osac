import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Dropdown, DropdownItem, DropdownList, MenuToggle } from '@patternfly/react-core';
import { EllipsisVIcon } from '@patternfly/react-icons/dist/esm/icons/ellipsis-v-icon';

import { IdentityProvider, IdentityProviders } from '@osac/types';
import { useDeleteResource } from '@osac/ui-components/api/use-resource';
import DeleteResourceButton from '@osac/ui-components/components/Resource/DeleteResourceButton';
import DeleteResourceModal from '@osac/ui-components/components/Resource/DeleteResourceModal';

import IdentityProviderEnableModal from './IdentityProviderEnableModal';
import { getIdpName } from './utils';
import { useTranslation } from '../../hooks/useTranslation';

interface IdentityProviderActionsMenuProps {
  idp: IdentityProvider;
}

const IdentityProviderActionsMenu = ({ idp }: IdentityProviderActionsMenuProps) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [enableOpen, setEnableOpen] = useState(false);
  const deleteIdp = useDeleteResource(IdentityProviders);

  return (
    <>
      {deleteOpen && (
        <DeleteResourceModal
          resourceName={getIdpName(idp)}
          label={t(
            'This permanently deletes the Identity provider and all its resources. This action cannot be undone.',
          )}
          errorLabel={t('Failed to delete Identity provider')}
          onClose={() => setDeleteOpen(false)}
          onSuccess={() => setDeleteOpen(false)}
          mutation={deleteIdp}
          variables={{ id: idp.id }}
        />
      )}
      {enableOpen && (
        <IdentityProviderEnableModal
          idp={idp}
          onClose={() => setEnableOpen(false)}
          onSuccess={() => setEnableOpen(false)}
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
            aria-label={t('Actions for {{name}}', { name: getIdpName(idp) })}
          >
            <EllipsisVIcon />
          </MenuToggle>
        )}
        popperProps={{ position: 'right' }}
      >
        <DropdownList>
          <DropdownItem onClick={() => navigate(`/tenant/identity-provider/${idp.id}/edit`)}>
            {t('Edit')}
          </DropdownItem>
          <DropdownItem
            onClick={() => {
              setEnableOpen(true);
              setOpen(false);
            }}
          >
            {idp.spec?.enabled ? t('Disable') : t('Enable')}
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

export default IdentityProviderActionsMenu;
