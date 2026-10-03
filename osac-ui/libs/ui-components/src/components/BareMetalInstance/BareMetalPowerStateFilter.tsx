import { useState } from 'react';
import { MenuToggle, Select, SelectList, SelectOption } from '@patternfly/react-core';

import {
  BARE_METAL_POWER_STATE_FILTERS,
  type BareMetalPowerStateFilter,
  bareMetalPowerStateFilterLabel,
} from './bareMetalInstanceDisplay';
import { useTranslation } from '../../hooks/useTranslation';

const ALL_OPTION_VALUE = '__all__';

interface BareMetalPowerStateFilterProps {
  selected: BareMetalPowerStateFilter | undefined;
  onChange: (value: BareMetalPowerStateFilter | undefined) => void;
}

const BareMetalPowerStateFilter = ({ selected, onChange }: BareMetalPowerStateFilterProps) => {
  const { t } = useTranslation();
  const [isOpen, setIsOpen] = useState(false);

  const allLabel = t('All power states');
  const selectedLabel = selected ? bareMetalPowerStateFilterLabel(selected, t) : allLabel;

  return (
    <Select
      isOpen={isOpen}
      selected={selected ?? ALL_OPTION_VALUE}
      onOpenChange={setIsOpen}
      onSelect={(_event, value) => {
        onChange(value === ALL_OPTION_VALUE ? undefined : (value as BareMetalPowerStateFilter));
        setIsOpen(false);
      }}
      shouldFocusToggleOnSelect
      toggle={(toggleRef) => (
        <MenuToggle
          ref={toggleRef}
          onClick={() => setIsOpen((open) => !open)}
          isExpanded={isOpen}
          aria-label={t('Filter bare metal by power state')}
        >
          {selectedLabel}
        </MenuToggle>
      )}
    >
      <SelectList>
        <SelectOption value={ALL_OPTION_VALUE} isSelected={selected === undefined}>
          {allLabel}
        </SelectOption>
        {BARE_METAL_POWER_STATE_FILTERS.map((option) => (
          <SelectOption key={option} value={option} isSelected={selected === option}>
            {bareMetalPowerStateFilterLabel(option, t)}
          </SelectOption>
        ))}
      </SelectList>
    </Select>
  );
};

export default BareMetalPowerStateFilter;
