import { useState } from 'react';
import { MenuToggle, Select, SelectList, SelectOption, ToolbarItem } from '@patternfly/react-core';

import { useTranslation } from '../../hooks/useTranslation';

interface IdentityProviderEnabledFilterProps {
  enabled?: boolean;
  onChange: (value: boolean | null) => void;
}

const IdentityProviderEnabledFilter = ({
  enabled,
  onChange,
}: IdentityProviderEnabledFilterProps) => {
  const { t } = useTranslation();
  const [isOpen, setIsOpen] = useState(false);

  const enabledToggleLabel =
    enabled === undefined ? t('All enablements') : enabled ? t('Enabled') : t('Disabled');

  return (
    <ToolbarItem>
      <Select
        isOpen={isOpen}
        onOpenChange={() => setIsOpen((open) => !open)}
        onSelect={(_event, value) => {
          onChange(value === null ? null : Boolean(value));
          setIsOpen(false);
        }}
        toggle={(toggleRef) => (
          <MenuToggle
            ref={toggleRef}
            onClick={() => setIsOpen((open) => !open)}
            isExpanded={isOpen}
          >
            {enabledToggleLabel}
          </MenuToggle>
        )}
        shouldFocusToggleOnSelect
      >
        <SelectList>
          <SelectOption value={null} isSelected={enabled === undefined}>
            {t('All enablements')}
          </SelectOption>
          <SelectOption value={true} isSelected={enabled === true}>
            {t('Enabled')}
          </SelectOption>
          <SelectOption value={false} isSelected={enabled === false}>
            {t('Disabled')}
          </SelectOption>
        </SelectList>
      </Select>
    </ToolbarItem>
  );
};

export default IdentityProviderEnabledFilter;
