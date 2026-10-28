import { useMemo, useState } from 'react';
import { MenuToggle, Select, SelectList, SelectOption, ToolbarItem } from '@patternfly/react-core';

import {
  IDENTITY_PROVIDER_PHASE_FILTER_TO_ENUM,
  type IdentityProviderPhaseFilterValue,
  PHASE_FILTER_VALUES,
  getIdentityProviderPhaseLabels,
  isIdentityProviderPhaseFilterValue,
} from './utils';
import { useTranslation } from '../../hooks/useTranslation';

interface IdentityProviderPhaseFilterProps {
  phaseFilters: IdentityProviderPhaseFilterValue[];
  onToggle: (value: IdentityProviderPhaseFilterValue) => void;
}

const IdentityProviderPhaseFilter = ({
  phaseFilters,
  onToggle,
}: IdentityProviderPhaseFilterProps) => {
  const { t } = useTranslation();
  const [isOpen, setIsOpen] = useState(false);
  const phaseLabels = useMemo(() => getIdentityProviderPhaseLabels(t), [t]);

  const phaseToggleLabel =
    phaseFilters.length === 0
      ? t('All states')
      : phaseFilters
          .map((value) => phaseLabels[IDENTITY_PROVIDER_PHASE_FILTER_TO_ENUM[value]])
          .filter(Boolean)
          .join(', ');

  return (
    <ToolbarItem>
      <Select
        isOpen={isOpen}
        onOpenChange={() => setIsOpen((open) => !open)}
        onSelect={(_event, value) => {
          if (typeof value === 'string' && isIdentityProviderPhaseFilterValue(value)) {
            onToggle(value);
          }
        }}
        toggle={(toggleRef) => (
          <MenuToggle
            ref={toggleRef}
            onClick={() => setIsOpen((open) => !open)}
            isExpanded={isOpen}
          >
            {phaseToggleLabel}
          </MenuToggle>
        )}
        shouldFocusToggleOnSelect
      >
        <SelectList>
          {PHASE_FILTER_VALUES.map((value) => (
            <SelectOption
              key={value}
              value={value}
              hasCheckbox
              isSelected={phaseFilters.includes(value)}
            >
              {phaseLabels[IDENTITY_PROVIDER_PHASE_FILTER_TO_ENUM[value]]}
            </SelectOption>
          ))}
        </SelectList>
      </Select>
    </ToolbarItem>
  );
};

export default IdentityProviderPhaseFilter;
