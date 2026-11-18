import { useMemo, useState } from 'react';
import { MenuToggle, Select, SelectList, SelectOption } from '@patternfly/react-core';

import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import {
  ADMIN_INSTANCE_TYPE_LIFECYCLE_FILTERS,
  type AdminInstanceTypeLifecycleFilter,
  adminInstanceTypeLifecycleFilterLabel,
  isAdminInstanceTypeLifecycleFilter,
} from './admin-instance-type-list-filter';

interface AdminInstanceTypeLifecycleFilterProps {
  lifecycleFilters: AdminInstanceTypeLifecycleFilter[];
  onToggle: (value: AdminInstanceTypeLifecycleFilter) => void;
}

const AdminInstanceTypeLifecycleFilter = ({
  lifecycleFilters,
  onToggle,
}: AdminInstanceTypeLifecycleFilterProps) => {
  const { t } = useTranslation();
  const [isOpen, setIsOpen] = useState(false);

  const toggleLabel = useMemo(
    () =>
      lifecycleFilters.length === 0
        ? t('All lifecycle states')
        : lifecycleFilters
            .map((value) => adminInstanceTypeLifecycleFilterLabel(value, t))
            .join(', '),
    [lifecycleFilters, t],
  );

  return (
    <Select
      isOpen={isOpen}
      onOpenChange={() => setIsOpen((open) => !open)}
      onSelect={(_event, value) => {
        if (typeof value === 'string' && isAdminInstanceTypeLifecycleFilter(value)) {
          onToggle(value);
        }
      }}
      shouldFocusToggleOnSelect
      toggle={(toggleRef) => (
        <MenuToggle
          ref={toggleRef}
          onClick={() => setIsOpen((open) => !open)}
          isExpanded={isOpen}
          aria-label={t('Filter instance types by lifecycle state')}
        >
          {toggleLabel}
        </MenuToggle>
      )}
    >
      <SelectList>
        {ADMIN_INSTANCE_TYPE_LIFECYCLE_FILTERS.map((option) => (
          <SelectOption
            key={option}
            value={option}
            hasCheckbox
            isSelected={lifecycleFilters.includes(option)}
          >
            {adminInstanceTypeLifecycleFilterLabel(option, t)}
          </SelectOption>
        ))}
      </SelectList>
    </Select>
  );
};

export default AdminInstanceTypeLifecycleFilter;
