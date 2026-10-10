import { useEffect, useMemo, useState } from 'react';
import { MenuToggle, Select, SelectList, SelectOption } from '@patternfly/react-core';

import { Tenants } from '@osac/types/private';
import { useListResource } from '@osac/ui-components/api/use-resource';

import { GLOBAL_TENANT_VALUE } from './catalogItemDisplay';
import { useTranslation } from '../../hooks/useTranslation';

interface OptionEntry {
  value: string;
  label: string;
}

interface CatalogTenantFilterProps {
  selected: string | undefined;
  onChange: (value: string | undefined) => void;
}

const CatalogTenantFilter = ({ selected, onChange }: CatalogTenantFilterProps) => {
  const { t } = useTranslation();
  const [isOpen, setIsOpen] = useState(false);
  const { data: tenantsResponse, isSuccess } = useListResource(Tenants);

  useEffect(() => {
    if (!selected || !isSuccess) {
      return;
    }
    const tenants = tenantsResponse?.items ?? [];
    if (tenants.some((tenant) => tenant.id === selected)) {
      return;
    }
    onChange(undefined);
  }, [isSuccess, onChange, selected, tenantsResponse?.items]);

  const selection = useMemo(() => {
    if (!selected) {
      return GLOBAL_TENANT_VALUE;
    }
    return selected;
  }, [selected]);

  const options = useMemo<ReadonlyArray<OptionEntry>>(() => {
    const tenants = tenantsResponse?.items ?? [];

    return [
      { value: GLOBAL_TENANT_VALUE, label: t('All tenants') },
      ...tenants
        .filter((tenant) => tenant.id !== GLOBAL_TENANT_VALUE)
        .map((tenant) => ({
          value: tenant.id,
          label: tenant.id,
        }))
        .sort((a, b) => a.label.localeCompare(b.label)),
    ];
  }, [tenantsResponse?.items, t]);

  const selectedLabel =
    options.find((option) => option.value === selection)?.label ??
    (selection === GLOBAL_TENANT_VALUE ? t('All tenants') : selection);

  return (
    <Select
      isOpen={isOpen}
      selected={selection}
      onOpenChange={setIsOpen}
      onSelect={(_event, value: string) => {
        onChange(value === GLOBAL_TENANT_VALUE ? undefined : value);
        setIsOpen(false);
      }}
      shouldFocusToggleOnSelect
      toggle={(toggleRef) => (
        <MenuToggle
          ref={toggleRef}
          onClick={() => setIsOpen((open) => !open)}
          isExpanded={isOpen}
          aria-label={t('Filter catalog by tenant')}
        >
          {selectedLabel}
        </MenuToggle>
      )}
    >
      <SelectList>
        {options.map((option) => (
          <SelectOption
            key={option.value}
            value={option.value}
            isSelected={selection === option.value}
          >
            {option.label}
          </SelectOption>
        ))}
      </SelectList>
    </Select>
  );
};

export default CatalogTenantFilter;
