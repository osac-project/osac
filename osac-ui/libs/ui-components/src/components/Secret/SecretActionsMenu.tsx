import { FC, useState } from 'react';
import {
  Dropdown,
  DropdownItem,
  DropdownList,
  MenuToggle,
  MenuToggleElement,
} from '@patternfly/react-core';
import { EllipsisVIcon } from '@patternfly/react-icons/dist/esm/icons/ellipsis-v-icon';

import { Secret } from '@osac/types';
import DeleteResourceButton from '@osac/ui-components/components/Resource/DeleteResourceButton';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

interface SecretActionsMenuProps {
  secret: Secret;
  onEdit: () => void;
  onDelete: () => void;
}

const SecretActionsMenu: FC<SecretActionsMenuProps> = ({ secret, onEdit, onDelete }) => {
  const { t } = useTranslation();
  const [isOpen, setIsOpen] = useState(false);

  const onSelect = () => {
    setIsOpen(!isOpen);
  };

  return (
    <Dropdown
      onSelect={onSelect}
      toggle={(toggleRef: React.Ref<MenuToggleElement>) => (
        <MenuToggle
          ref={toggleRef}
          variant="plain"
          isExpanded={isOpen}
          onClick={() => setIsOpen(!isOpen)}
          aria-label={t('Actions for {{name}}', { name: secret.metadata?.name })}
        >
          <EllipsisVIcon />
        </MenuToggle>
      )}
      isOpen={isOpen}
      onOpenChange={(isOpen: boolean) => setIsOpen(isOpen)}
    >
      <DropdownList>
        <DropdownItem onClick={() => onEdit()}>{t('Edit')}</DropdownItem>
        <DeleteResourceButton
          isDropdown
          onClick={() => {
            onDelete();
            setIsOpen(false);
          }}
        />
      </DropdownList>
    </Dropdown>
  );
};

export default SecretActionsMenu;
