import { useMemo, useState } from 'react';
import { MenuToggle, Select, SelectList, SelectOption, ToolbarItem } from '@patternfly/react-core';

import {
  STATE_FILTER_VALUES,
  TENANT_STATE_FILTER_TO_ENUM,
  type TenantStateFilterValue,
  getTenantStateLabels,
  isTenantStateFilterValue,
} from './utils';
import { useTranslation } from '../../hooks/useTranslation';

interface TenantStateFilterProps {
  stateFilters: TenantStateFilterValue[];
  onToggle: (value: TenantStateFilterValue) => void;
}

const TenantStateFilter = ({ stateFilters, onToggle }: TenantStateFilterProps) => {
  const { t } = useTranslation();
  const [isOpen, setIsOpen] = useState(false);
  const stateLabels = useMemo(() => getTenantStateLabels(t), [t]);

  const stateToggleLabel =
    stateFilters.length === 0
      ? t('All states')
      : stateFilters
          .map((value) => stateLabels[TENANT_STATE_FILTER_TO_ENUM[value]])
          .filter(Boolean)
          .join(', ');

  return (
    <ToolbarItem>
      <Select
        isOpen={isOpen}
        onOpenChange={() => setIsOpen((open) => !open)}
        onSelect={(_event, value) => {
          if (typeof value === 'string' && isTenantStateFilterValue(value)) {
            onToggle(value);
          }
        }}
        toggle={(toggleRef) => (
          <MenuToggle
            ref={toggleRef}
            onClick={() => setIsOpen((open) => !open)}
            isExpanded={isOpen}
          >
            {stateToggleLabel}
          </MenuToggle>
        )}
        shouldFocusToggleOnSelect
      >
        <SelectList>
          {STATE_FILTER_VALUES.map((value) => (
            <SelectOption
              key={value}
              value={value}
              hasCheckbox
              isSelected={stateFilters.includes(value)}
            >
              {stateLabels[TENANT_STATE_FILTER_TO_ENUM[value]]}
            </SelectOption>
          ))}
        </SelectList>
      </Select>
    </ToolbarItem>
  );
};

export default TenantStateFilter;
