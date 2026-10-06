import { useState } from 'react';
import { MenuToggle, Select, SelectList, SelectOption } from '@patternfly/react-core';

import {
  TYPE_FILTER_TO_ENUM,
  TYPE_FILTER_VALUES,
  type TypeFilterValue,
  getSecretType,
  isTypeFilterValue,
} from './utils';
import { useTranslation } from '../../hooks/useTranslation';

interface TypeFilterProps {
  type: TypeFilterValue[];
  onClear: () => void;
  onToggle: (value: TypeFilterValue) => void;
}

const TypeFilter = ({ type, onClear, onToggle }: TypeFilterProps) => {
  const { t } = useTranslation();
  const [isOpen, setIsOpen] = useState(false);
  const secretTypes = getSecretType(t);

  return (
    <Select
      role="menu"
      isOpen={isOpen}
      onSelect={(_, value) => {
        if (value === null) {
          onClear();
          setIsOpen(false);
          return;
        }
        if (typeof value === 'string' && isTypeFilterValue(value)) {
          onToggle(value);
        }
      }}
      onOpenChange={setIsOpen}
      toggle={(toggleRef) => (
        <MenuToggle ref={toggleRef} onClick={() => setIsOpen((open) => !open)} isExpanded={isOpen}>
          {type.length
            ? type.map((value) => secretTypes[TYPE_FILTER_TO_ENUM[value]]).join(', ')
            : t('All types')}
        </MenuToggle>
      )}
      shouldFocusToggleOnSelect
    >
      <SelectList>
        <SelectOption value={null}>{t('All types')}</SelectOption>
        {TYPE_FILTER_VALUES.map((value) => (
          <SelectOption key={value} value={value} hasCheckbox isSelected={type.includes(value)}>
            {secretTypes[TYPE_FILTER_TO_ENUM[value]]}
          </SelectOption>
        ))}
      </SelectList>
    </Select>
  );
};

export default TypeFilter;
