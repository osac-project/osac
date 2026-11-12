import { useMemo, useState } from 'react';
import { MenuToggle, Select, SelectList, SelectOption } from '@patternfly/react-core';

import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import {
  ROLE_BINDING_STATUS_FILTERS,
  type RoleBindingStatusFilter,
  isRoleBindingStatusFilter,
  roleBindingStatusFilterLabel,
} from './role-binding-list-filter';

interface RoleBindingStatusFilterProps {
  statusFilters: RoleBindingStatusFilter[];
  onToggle: (value: RoleBindingStatusFilter) => void;
}

const RoleBindingStatusFilter = ({ statusFilters, onToggle }: RoleBindingStatusFilterProps) => {
  const { t } = useTranslation();
  const [isOpen, setIsOpen] = useState(false);

  const toggleLabel = useMemo(
    () =>
      statusFilters.length === 0
        ? t('All statuses')
        : statusFilters.map((value) => roleBindingStatusFilterLabel(value, t)).join(', '),
    [statusFilters, t],
  );

  return (
    <Select
      isOpen={isOpen}
      onOpenChange={() => setIsOpen((open) => !open)}
      onSelect={(_event, value) => {
        if (typeof value === 'string' && isRoleBindingStatusFilter(value)) {
          onToggle(value);
        }
      }}
      shouldFocusToggleOnSelect
      toggle={(toggleRef) => (
        <MenuToggle
          ref={toggleRef}
          onClick={() => setIsOpen((open) => !open)}
          isExpanded={isOpen}
          aria-label={t('Filter role bindings by status')}
        >
          {toggleLabel}
        </MenuToggle>
      )}
    >
      <SelectList>
        {ROLE_BINDING_STATUS_FILTERS.map((option) => (
          <SelectOption
            key={option}
            value={option}
            hasCheckbox
            isSelected={statusFilters.includes(option)}
          >
            {roleBindingStatusFilterLabel(option, t)}
          </SelectOption>
        ))}
      </SelectList>
    </Select>
  );
};

export default RoleBindingStatusFilter;
