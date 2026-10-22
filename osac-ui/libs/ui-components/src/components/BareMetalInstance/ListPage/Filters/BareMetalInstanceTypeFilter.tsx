import { useMemo, useState } from 'react';
import { Alert, MenuToggle, Select, SelectList, SelectOption } from '@patternfly/react-core';

import { BareMetalInstanceTypes } from '@osac/types';
import { useListResource } from '@osac/ui-components/api/use-resource';
import { getErrorMessage } from '@osac/ui-components/utils/error';

import { useTranslation } from '../../../../hooks/useTranslation';

const ALL_OPTION_VALUE = '__all__';

interface BareMetalInstanceTypeFilterProps {
  selected: string | undefined;
  onChange: (instanceTypeId: string | undefined) => void;
}

const BareMetalInstanceTypeFilter = ({ selected, onChange }: BareMetalInstanceTypeFilterProps) => {
  const { t } = useTranslation();
  const [isOpen, setIsOpen] = useState(false);

  const { data: instanceTypes, error } = useListResource(BareMetalInstanceTypes);

  const sortedInstanceTypes = useMemo(() => {
    const names = instanceTypes?.items
      .map((instanceType) => instanceType.metadata?.name?.trim())
      .filter((name): name is string => name !== undefined);
    return [...new Set(names)].sort((left, right) => left.localeCompare(right));
  }, [instanceTypes]);

  const allLabel = t('All instance types');
  const selectedLabel = selected || allLabel;

  return (
    <Select
      maxMenuHeight="350px"
      isOpen={isOpen}
      selected={selected ?? ALL_OPTION_VALUE}
      onOpenChange={setIsOpen}
      onSelect={(_event, value) => {
        onChange(value === ALL_OPTION_VALUE ? undefined : String(value));
        setIsOpen(false);
      }}
      shouldFocusToggleOnSelect
      toggle={(toggleRef) => (
        <MenuToggle
          ref={toggleRef}
          onClick={() => setIsOpen((open) => !open)}
          isExpanded={isOpen}
          aria-label={t('Filter bare metal by instance type')}
        >
          {selectedLabel}
        </MenuToggle>
      )}
    >
      <SelectList>
        {error ? (
          <Alert isInline variant="danger" title={t('Unable to load instance types')}>
            {getErrorMessage(error)}
          </Alert>
        ) : (
          <>
            <SelectOption value={ALL_OPTION_VALUE} isSelected={!selected}>
              {allLabel}
            </SelectOption>
            {sortedInstanceTypes.map((instanceType) => (
              <SelectOption
                key={instanceType}
                value={instanceType}
                isSelected={selected === instanceType}
              >
                {instanceType}
              </SelectOption>
            ))}
          </>
        )}
      </SelectList>
    </Select>
  );
};

export default BareMetalInstanceTypeFilter;
