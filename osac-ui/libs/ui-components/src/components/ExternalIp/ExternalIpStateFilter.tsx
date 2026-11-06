import { useMemo, useState } from 'react';
import { MenuToggle, Select, SelectList, SelectOption, ToolbarItem } from '@patternfly/react-core';

import {
  EXTERNAL_IP_STATE_FILTER_TO_ENUM,
  type ExternalIpStateFilterValue,
  STATE_FILTER_VALUES,
  getExternalIpStateFilterLabels,
  isExternalIpStateFilterValue,
} from './utils';
import { useTranslation } from '../../hooks/useTranslation';

interface ExternalIpStateFilterProps {
  stateFilters: ExternalIpStateFilterValue[];
  onToggle: (value: ExternalIpStateFilterValue) => void;
}

const ExternalIpStateFilter = ({ stateFilters, onToggle }: ExternalIpStateFilterProps) => {
  const { t } = useTranslation();
  const [isOpen, setIsOpen] = useState(false);
  const stateLabels = useMemo(() => getExternalIpStateFilterLabels(t), [t]);

  const stateToggleLabel =
    stateFilters.length === 0
      ? t('All states')
      : stateFilters
          .map((value) => stateLabels[EXTERNAL_IP_STATE_FILTER_TO_ENUM[value]])
          .filter(Boolean)
          .join(', ');

  return (
    <ToolbarItem>
      <Select
        isOpen={isOpen}
        onOpenChange={() => setIsOpen((open) => !open)}
        onSelect={(_event, value) => {
          if (typeof value === 'string' && isExternalIpStateFilterValue(value)) {
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
              {stateLabels[EXTERNAL_IP_STATE_FILTER_TO_ENUM[value]]}
            </SelectOption>
          ))}
        </SelectList>
      </Select>
    </ToolbarItem>
  );
};

export default ExternalIpStateFilter;
