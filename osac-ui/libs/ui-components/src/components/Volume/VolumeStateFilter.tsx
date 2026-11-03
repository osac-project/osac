import { useMemo, useState } from 'react';
import { MenuToggle, Select, SelectList, SelectOption, ToolbarItem } from '@patternfly/react-core';

import {
  STATE_FILTER_VALUES,
  VOLUME_STATE_FILTER_TO_ENUM,
  type VolumeStateFilterValue,
  getVolumeStatusMap,
  isVolumeStateFilterValue,
} from './utils';
import { useTranslation } from '../../hooks/useTranslation';

interface VolumeStateFilterProps {
  stateFilters: VolumeStateFilterValue[];
  onToggle: (value: VolumeStateFilterValue) => void;
}

const VolumeStateFilter = ({ stateFilters, onToggle }: VolumeStateFilterProps) => {
  const { t } = useTranslation();
  const [isOpen, setIsOpen] = useState(false);
  const volumeStatusMap = useMemo(() => getVolumeStatusMap(t), [t]);

  const stateToggleLabel =
    stateFilters.length === 0
      ? t('All states')
      : stateFilters
          .map((v) => volumeStatusMap[VOLUME_STATE_FILTER_TO_ENUM[v]].text)
          .filter(Boolean)
          .join(', ');

  return (
    <ToolbarItem>
      <Select
        isOpen={isOpen}
        onOpenChange={() => setIsOpen((o) => !o)}
        onSelect={(_event, value) => {
          if (typeof value === 'string' && isVolumeStateFilterValue(value)) {
            onToggle(value);
          }
        }}
        toggle={(toggleRef) => (
          <MenuToggle ref={toggleRef} onClick={() => setIsOpen((o) => !o)} isExpanded={isOpen}>
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
              {volumeStatusMap[VOLUME_STATE_FILTER_TO_ENUM[value]].text}
            </SelectOption>
          ))}
        </SelectList>
      </Select>
    </ToolbarItem>
  );
};

export default VolumeStateFilter;
