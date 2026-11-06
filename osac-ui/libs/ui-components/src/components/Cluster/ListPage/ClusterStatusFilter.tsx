import { useState } from 'react';
import { MenuToggle, Select, SelectList, SelectOption } from '@patternfly/react-core';

import {
  CLUSTER_STATUS_FILTERS,
  type ClusterStatusFilter,
  clusterStatusFilterLabel,
} from './clusterDisplay';
import { useTranslation } from '../../../hooks/useTranslation';

const ALL_OPTION_VALUE = '__all__';

interface ClusterStatusFilterProps {
  selected: ClusterStatusFilter | undefined;
  onChange: (value: ClusterStatusFilter | undefined) => void;
}

const ClusterStatusFilter = ({ selected, onChange }: ClusterStatusFilterProps) => {
  const { t } = useTranslation();
  const [isOpen, setIsOpen] = useState(false);

  const allLabel = t('All statuses');
  const selectedLabel = selected ? clusterStatusFilterLabel(selected, t) : allLabel;

  return (
    <Select
      isOpen={isOpen}
      selected={selected ?? ALL_OPTION_VALUE}
      onOpenChange={setIsOpen}
      onSelect={(_event, value) => {
        onChange(value === ALL_OPTION_VALUE ? undefined : (value as ClusterStatusFilter));
        setIsOpen(false);
      }}
      shouldFocusToggleOnSelect
      toggle={(toggleRef) => (
        <MenuToggle
          ref={toggleRef}
          onClick={() => setIsOpen((open) => !open)}
          isExpanded={isOpen}
          aria-label={t('Filter clusters by status')}
        >
          {selectedLabel}
        </MenuToggle>
      )}
    >
      <SelectList>
        <SelectOption value={ALL_OPTION_VALUE} isSelected={selected === undefined}>
          {allLabel}
        </SelectOption>
        {CLUSTER_STATUS_FILTERS.map((option) => (
          <SelectOption key={option} value={option} isSelected={selected === option}>
            {clusterStatusFilterLabel(option, t)}
          </SelectOption>
        ))}
      </SelectList>
    </Select>
  );
};

export default ClusterStatusFilter;
