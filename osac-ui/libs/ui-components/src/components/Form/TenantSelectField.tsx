import { Tenants } from '@osac/types/private';

import { ResourceSelectField } from './ResourceSelectField';
import type { ResourceSelectValue } from './resourceSelectValue';
import { useTranslation } from '../../hooks/useTranslation';

export interface TenantSelectFieldProps {
  isDisabled?: boolean;
  /** Called after the tenant selection changes, for example to reset dependent fields. */
  onSelect?: (value: ResourceSelectValue) => void;
}

const TenantSelectField = ({ isDisabled = false, onSelect }: TenantSelectFieldProps) => {
  const { t } = useTranslation();

  return (
    <ResourceSelectField
      name="metadata.tenant"
      label={t('Tenant')}
      fieldId="tenant-field"
      service={Tenants}
      isRequired
      isDisabled={isDisabled}
      placeholder={t('Search and select a tenant')}
      loadErrorTitle={t('Failed to fetch tenants')}
      filterItems={(tenant) => tenant.id !== 'system' && tenant.id !== 'shared'}
      onSelectResource={(value) => onSelect?.(value)}
    />
  );
};

export default TenantSelectField;
