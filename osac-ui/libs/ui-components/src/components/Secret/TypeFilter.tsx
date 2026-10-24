import { useState } from 'react';
import {
  Badge,
  Flex,
  FlexItem,
  MenuToggle,
  Select,
  SelectList,
  SelectOption,
  ToolbarFilter,
} from '@patternfly/react-core';

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
    <ToolbarFilter
      labels={type.map((value) => secretTypes[TYPE_FILTER_TO_ENUM[value]])}
      deleteLabel={(_category, label) => {
        const filterValue = TYPE_FILTER_VALUES.find(
          (value) => secretTypes[TYPE_FILTER_TO_ENUM[value]] === label,
        );
        if (filterValue) {
          onToggle(filterValue);
        }
      }}
      deleteLabelGroup={() => onClear()}
      categoryName={t('Type')}
    >
      <Select
        role="menu"
        isOpen={isOpen}
        onSelect={(_, value) => {
          if (typeof value === 'string' && isTypeFilterValue(value)) {
            onToggle(value);
          }
        }}
        onOpenChange={setIsOpen}
        toggle={(toggleRef) => (
          <MenuToggle
            ref={toggleRef}
            onClick={() => setIsOpen((open) => !open)}
            isExpanded={isOpen}
          >
            <Flex flexWrap={{ default: 'nowrap' }} gap={{ default: 'gapXs' }}>
              <FlexItem>{t('Types')}</FlexItem>
              {type.length > 0 ? (
                <FlexItem>
                  <Badge isRead>{type.length}</Badge>
                </FlexItem>
              ) : null}
            </Flex>
          </MenuToggle>
        )}
        shouldFocusToggleOnSelect
      >
        <SelectList>
          {TYPE_FILTER_VALUES.map((value) => (
            <SelectOption key={value} value={value} hasCheckbox isSelected={type.includes(value)}>
              {secretTypes[TYPE_FILTER_TO_ENUM[value]]}
            </SelectOption>
          ))}
        </SelectList>
      </Select>
    </ToolbarFilter>
  );
};

export default TypeFilter;
