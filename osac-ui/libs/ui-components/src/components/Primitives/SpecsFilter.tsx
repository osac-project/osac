import { useMemo, useState } from 'react';
import { MenuToggle, Select, SelectGroup, SelectList, SelectOption } from '@patternfly/react-core';

import { useTranslation } from '../../hooks/useTranslation';

const OPTION_VALUE_SEPARATOR = '::';

const toOptionValue = (categoryId: string, option: string): string =>
  `${categoryId}${OPTION_VALUE_SEPARATOR}${option}`;

const parseOptionValue = (
  value: string,
  validCategoryIds: ReadonlySet<string>,
): { categoryId: string; option: string } | undefined => {
  const separatorIndex = value.indexOf(OPTION_VALUE_SEPARATOR);
  if (separatorIndex <= 0) {
    return undefined;
  }

  const categoryId = value.slice(0, separatorIndex);
  if (!validCategoryIds.has(categoryId)) {
    return undefined;
  }

  const option = value.slice(separatorIndex + OPTION_VALUE_SEPARATOR.length);
  if (!option) {
    return undefined;
  }

  return { categoryId, option };
};

export interface SpecFilterCategory<K extends string = string> {
  id: K;
  label: string;
  options: readonly string[];
}

export interface SpecsFilterProps<K extends string> {
  categories: readonly SpecFilterCategory<K>[];
  selected: Readonly<{ [P in K]: readonly string[] }>;
  onToggle: (categoryId: K, value: string) => void;
  ariaLabel: string;
}

const SpecsFilter = <K extends string>({
  categories,
  selected,
  onToggle,
  ariaLabel,
}: SpecsFilterProps<K>) => {
  const { t } = useTranslation();
  const [isOpen, setIsOpen] = useState(false);

  const validCategoryIds = useMemo(
    () => new Set(categories.map((category) => category.id)),
    [categories],
  );

  const toggleLabel = useMemo(() => {
    let selectedCount = 0;
    let singleCategoryLabel: string | undefined;
    let singleValue: string | undefined;

    for (const { id, label } of categories) {
      const values = selected[id] ?? [];
      selectedCount += values.length;
      if (values.length === 1 && selectedCount === 1) {
        singleCategoryLabel = label;
        singleValue = values[0];
      } else if (values.length > 0 && selectedCount > 1) {
        singleCategoryLabel = undefined;
        singleValue = undefined;
      }
    }

    if (!selectedCount) {
      return t('All specs');
    }
    if (selectedCount === 1 && singleCategoryLabel && singleValue) {
      return `${singleCategoryLabel}: ${singleValue}`;
    }
    return t('{{count}} spec', { count: selectedCount });
  }, [categories, selected, t]);

  return (
    <Select
      role="menu"
      maxMenuHeight="300px"
      isOpen={isOpen}
      onOpenChange={setIsOpen}
      onSelect={(_event, value) => {
        if (typeof value !== 'string') {
          return;
        }

        const parsed = parseOptionValue(value, validCategoryIds);
        if (!parsed) {
          return;
        }

        onToggle(parsed.categoryId as K, parsed.option);
      }}
      toggle={(toggleRef) => (
        <MenuToggle
          ref={toggleRef}
          onClick={() => setIsOpen((open) => !open)}
          isExpanded={isOpen}
          aria-label={ariaLabel}
        >
          {toggleLabel}
        </MenuToggle>
      )}
    >
      <SelectList>
        {categories.map(({ id, label, options }) => {
          if (options.length === 0) {
            return null;
          }

          const selectedForCategory = selected[id] ?? [];

          return (
            <SelectGroup key={id} label={label}>
              <SelectList>
                {options.map((option) => {
                  const optionValue = toOptionValue(id, option);
                  return (
                    <SelectOption
                      key={optionValue}
                      value={optionValue}
                      hasCheckbox
                      isSelected={selectedForCategory.includes(option)}
                    >
                      {option}
                    </SelectOption>
                  );
                })}
              </SelectList>
            </SelectGroup>
          );
        })}
      </SelectList>
    </Select>
  );
};

export default SpecsFilter;
