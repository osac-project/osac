import { Tenants } from '@osac/types/private';

import { ResourceSelectField } from './ResourceSelectField';
import { useTranslation } from '../../hooks/useTranslation';

export interface TenantSelectFieldProps {
  isDisabled?: boolean;
}

const TenantSelectField = ({ isDisabled = false }: TenantSelectFieldProps) => {
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
    />
  );
};

export default TenantSelectField;
