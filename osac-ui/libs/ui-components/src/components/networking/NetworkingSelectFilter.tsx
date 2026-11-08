import { useState } from 'react';
import { MenuToggle, Select, SelectList, SelectOption } from '@patternfly/react-core';

const ALL_OPTION_VALUE = '__all__';

export interface NetworkingSelectFilterOption {
  value: string;
  label: string;
}

interface NetworkingSelectFilterProps {
  /** Toggle label when nothing is selected, and the first option. */
  allLabel: string;
  ariaLabel: string;
  options: NetworkingSelectFilterOption[];
  selected: string;
  onChange: (value: string) => void;
}

/** A single-choice toolbar filter, as on the bare-metal list. */
const NetworkingSelectFilter = ({
  allLabel,
  ariaLabel,
  options,
  selected,
  onChange,
}: NetworkingSelectFilterProps) => {
  const [isOpen, setIsOpen] = useState(false);
  const selectedLabel = options.find((option) => option.value === selected)?.label ?? allLabel;

  return (
    <Select
      maxMenuHeight="350px"
      isScrollable
      isOpen={isOpen}
      selected={selected || ALL_OPTION_VALUE}
      onOpenChange={setIsOpen}
      onSelect={(_event, value) => {
        onChange(value === ALL_OPTION_VALUE ? '' : String(value));
        setIsOpen(false);
      }}
      shouldFocusToggleOnSelect
      toggle={(toggleRef) => (
        <MenuToggle
          ref={toggleRef}
          onClick={() => setIsOpen((open) => !open)}
          isExpanded={isOpen}
          aria-label={ariaLabel}
        >
          {selectedLabel}
        </MenuToggle>
      )}
    >
      <SelectList>
        <SelectOption value={ALL_OPTION_VALUE} isSelected={!selected}>
          {allLabel}
        </SelectOption>
        {options.map((option) => (
          <SelectOption
            key={option.value}
            value={option.value}
            isSelected={selected === option.value}
          >
            {option.label}
          </SelectOption>
        ))}
      </SelectList>
    </Select>
  );
};

export default NetworkingSelectFilter;
