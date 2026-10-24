import { useState } from 'react';
import { MenuToggle, Select, SelectList, SelectOption } from '@patternfly/react-core';

import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import {
  VM_POWER_STATE_FILTERS,
  type VmPowerStateFilter,
  vmPowerStateFilterLabel,
} from '../vmInstanceDisplay';

const ALL_OPTION_VALUE = '__all__';

interface VmPowerStateFilterProps {
  selected: VmPowerStateFilter | undefined;
  onChange: (value: VmPowerStateFilter | undefined) => void;
}

const VmPowerStateFilter = ({ selected, onChange }: VmPowerStateFilterProps) => {
  const { t } = useTranslation();
  const [isOpen, setIsOpen] = useState(false);

  const allLabel = t('All power states');
  const selectedLabel = selected ? vmPowerStateFilterLabel(selected, t) : allLabel;

  return (
    <Select
      isOpen={isOpen}
      selected={selected ?? ALL_OPTION_VALUE}
      onOpenChange={setIsOpen}
      onSelect={(_event, value) => {
        onChange(value === ALL_OPTION_VALUE ? undefined : (value as VmPowerStateFilter));
        setIsOpen(false);
      }}
      shouldFocusToggleOnSelect
      toggle={(toggleRef) => (
        <MenuToggle
          ref={toggleRef}
          onClick={() => setIsOpen((open) => !open)}
          isExpanded={isOpen}
          aria-label={t('Filter virtual machines by power state')}
        >
          {selectedLabel}
        </MenuToggle>
      )}
    >
      <SelectList>
        <SelectOption value={ALL_OPTION_VALUE} isSelected={selected === undefined}>
          {allLabel}
        </SelectOption>
        {VM_POWER_STATE_FILTERS.map((option) => (
          <SelectOption key={option} value={option} isSelected={selected === option}>
            {vmPowerStateFilterLabel(option, t)}
          </SelectOption>
        ))}
      </SelectList>
    </Select>
  );
};

export default VmPowerStateFilter;
