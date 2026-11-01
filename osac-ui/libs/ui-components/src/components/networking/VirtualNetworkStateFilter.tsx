import { useMemo, useState } from 'react';
import { MenuToggle, Select, SelectList, SelectOption, ToolbarItem } from '@patternfly/react-core';

import {
  STATE_FILTER_VALUES,
  type VirtualNetworkStateFilterValue,
  getVirtualNetworkStateFilterLabels,
  isVirtualNetworkStateFilterValue,
} from './virtual-network-list-utils';
import { useTranslation } from '../../hooks/useTranslation';

interface VirtualNetworkStateFilterProps {
  stateFilters: VirtualNetworkStateFilterValue[];
  onToggle: (value: VirtualNetworkStateFilterValue) => void;
}

const VirtualNetworkStateFilter = ({ stateFilters, onToggle }: VirtualNetworkStateFilterProps) => {
  const { t } = useTranslation();
  const [isOpen, setIsOpen] = useState(false);
  const stateLabels = useMemo(() => getVirtualNetworkStateFilterLabels(t), [t]);

  const stateToggleLabel =
    stateFilters.length === 0
      ? t('All states')
      : stateFilters
          .map((value) => stateLabels[value])
          .filter(Boolean)
          .join(', ');

  return (
    <ToolbarItem>
      <Select
        isOpen={isOpen}
        onOpenChange={() => setIsOpen((open) => !open)}
        onSelect={(_event, value) => {
          if (typeof value === 'string' && isVirtualNetworkStateFilterValue(value)) {
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
              {stateLabels[value]}
            </SelectOption>
          ))}
        </SelectList>
      </Select>
    </ToolbarItem>
  );
};

export default VirtualNetworkStateFilter;
