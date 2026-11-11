import { Tenants } from '@osac/types/private';

import { ResourceSelectField } from './ResourceSelectField';
import { useTranslation } from '../../hooks/useTranslation';

export interface TenantSelectFieldProps {
  isDisabled?: boolean;
  /** Called after a tenant is selected, e.g. to clear fields that depend on it. */
  onSelect?: () => void;
}

const TenantSelectField = ({ isDisabled = false, onSelect }: TenantSelectFieldProps) => {
  const { t } = useTranslation();

  return (
    <ResourceSelectField
      name="metadata.tenant"
      label={t('Tenant')}
      fieldId="catalog-item-tenant"
      service={Tenants}
      isRequired
      isDisabled={isDisabled}
      placeholder={t('Search and select a tenant')}
      loadErrorTitle={t('Failed to fetch tenants')}
      // The typeahead searches the loaded items, so every page is loaded.
      loadAllPages
      filterItems={(tenant) => tenant.id !== 'system' && tenant.id !== 'shared'}
      onSelectResource={onSelect}
    />
  );
};

export default TenantSelectField;
