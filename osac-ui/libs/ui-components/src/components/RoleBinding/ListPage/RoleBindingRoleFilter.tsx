import { useMemo, useState } from 'react';
import { Alert, MenuToggle, Select, SelectList, SelectOption } from '@patternfly/react-core';

import type { Role } from '@osac/types';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';
import { getErrorMessage } from '@osac/ui-components/utils/error';

interface RoleBindingRoleFilterProps {
  roles: readonly Role[];
  roleFilters: readonly string[];
  onToggle: (roleId: string) => void;
  loadError?: unknown;
}

const roleLabel = (role: Role): string =>
  role.spec?.title?.trim() || role.metadata?.name?.trim() || role.id;

const RoleBindingRoleFilter = ({
  roles,
  roleFilters,
  onToggle,
  loadError,
}: RoleBindingRoleFilterProps) => {
  const { t } = useTranslation();
  const [isOpen, setIsOpen] = useState(false);

  const roleOptions = useMemo(() => {
    const options = roles
      .filter((role) => role.id.trim().length > 0)
      .map((role) => ({ value: role.id, label: roleLabel(role) }));
    return options.sort((left, right) => left.label.localeCompare(right.label));
  }, [roles]);

  const labelByValue = useMemo(
    () => new Map(roleOptions.map((option) => [option.value, option.label])),
    [roleOptions],
  );

  const invalidSelections = useMemo(
    () => roleFilters.filter((roleId) => !labelByValue.has(roleId)),
    [labelByValue, roleFilters],
  );

  const toggleLabel = useMemo(() => {
    if (roleFilters.length === 0) {
      return t('All roles');
    }
    if (roleFilters.length === 1) {
      return labelByValue.get(roleFilters[0]) ?? roleFilters[0];
    }
    return roleFilters.map((roleId) => labelByValue.get(roleId) ?? roleId).join(', ');
  }, [labelByValue, roleFilters, t]);

  return (
    <Select
      maxMenuHeight="350px"
      isOpen={isOpen}
      onOpenChange={setIsOpen}
      onSelect={(_event, value) => {
        if (typeof value === 'string' && value.trim().length > 0) {
          onToggle(value);
        }
      }}
      shouldFocusToggleOnSelect
      toggle={(toggleRef) => (
        <MenuToggle
          ref={toggleRef}
          onClick={() => setIsOpen((open) => !open)}
          isExpanded={isOpen}
          aria-label={t('Filter role bindings by role')}
        >
          {toggleLabel}
        </MenuToggle>
      )}
    >
      <SelectList>
        {loadError ? (
          <Alert isInline variant="danger" title={t('Unable to load roles')}>
            {getErrorMessage(loadError)}
          </Alert>
        ) : (
          <>
            {roleOptions.map((option) => (
              <SelectOption
                key={option.value}
                value={option.value}
                hasCheckbox
                isSelected={roleFilters.includes(option.value)}
              >
                {option.label}
              </SelectOption>
            ))}
            {invalidSelections.map((roleId) => (
              <SelectOption key={roleId} value={roleId} hasCheckbox isSelected>
                {roleId}
              </SelectOption>
            ))}
          </>
        )}
      </SelectList>
    </Select>
  );
};

export default RoleBindingRoleFilter;
