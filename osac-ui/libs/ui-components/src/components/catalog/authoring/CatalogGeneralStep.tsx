import { useEffect } from 'react';
import { Content, Stack, StackItem, Title } from '@patternfly/react-core';
import { useFormikContext } from 'formik';

import { Tenants } from '@osac/types/private';

import CatalogScopeField from './CatalogScopeField';
import { useSession } from '../../../hooks/use-session';
import { useTranslation } from '../../../hooks/useTranslation';
import { InputField } from '../../Form/InputField';
import OsacForm from '../../Form/OsacForm';
import ProjectField from '../../Form/ProjectField';
import {
  ResourceSelectField,
  type ResourceSelectFieldProps,
  type ResourceSelectValue,
  emptyResourceSelectValue,
} from '../../Form/ResourceSelectField';

export type CatalogItemScope = 'global' | 'tenant';

export interface CatalogGeneralValues {
  scope: CatalogItemScope;
  metadata: {
    name: string;
    project: string;
    description: string;
    /** Selected tenant when scope is `tenant`. Empty when scope is `global`. */
    tenant: ResourceSelectValue;
  };
  template: ResourceSelectValue;
}

export const catalogGeneralValues = (): CatalogGeneralValues => ({
  scope: 'tenant',
  metadata: { name: '', project: '', description: '', tenant: emptyResourceSelectValue() },
  template: emptyResourceSelectValue(),
});

interface CatalogGeneralStepProps {
  isEdit: boolean;
  templateService: ResourceSelectFieldProps['service'];
  templateFieldId: string;
}

const CatalogGeneralStep = ({
  isEdit,
  templateService,
  templateFieldId,
}: CatalogGeneralStepProps) => {
  const { t } = useTranslation();
  const { role } = useSession();
  const isAdmin = role === 'admin';
  const { values, setFieldValue } = useFormikContext<CatalogGeneralValues>();

  // Global catalog items have no tenant: drop a stale selection the moment the
  // author switches away from "Tenant scoped".
  useEffect(() => {
    if (values.scope === 'global' && values.metadata.tenant.id !== '') {
      void setFieldValue('metadata.tenant', emptyResourceSelectValue());
    }
  }, [values.scope, values.metadata.tenant, setFieldValue]);

  return (
    <Stack hasGutter>
      <StackItem>
        <Title headingLevel="h2" size="lg">
          {t('General')}
        </Title>
      </StackItem>
      <StackItem>
        <Content component="p">
          {t(
            'Define the name, scope, description, and provisioning template for this catalog item.',
          )}
        </Content>
      </StackItem>
      <StackItem>
        <OsacForm>
          {isAdmin ? (
            <>
              <Content component="p">
                {t(
                  'Assign the catalog item to a tenant (or make it global), then enter a name. Names are unique per tenant.',
                )}
              </Content>
              <CatalogScopeField isDisabled={isEdit} />
              <Content component="p">
                {t(
                  'Global resources are available to all tenants. Tenant-scoped resources must be assigned to one tenant.',
                )}
              </Content>
              {values.scope === 'tenant' && (
                <ResourceSelectField
                  name="metadata.tenant"
                  label={t('Tenant')}
                  fieldId="catalog-item-tenant"
                  service={Tenants}
                  isRequired
                  isDisabled={isEdit}
                  placeholder={t('Search and select a tenant')}
                  loadErrorTitle={t('Failed to fetch tenants')}
                />
              )}
            </>
          ) : (
            <ProjectField isDisabled={isEdit} />
          )}
          <InputField
            name="metadata.name"
            label={t('Name')}
            fieldId="catalog-item-name"
            isRequired
            isDisabled={isEdit}
            helperText={t('Name must be a valid DNS label (RFC 1035).')}
          />
          <InputField
            name="metadata.description"
            label={t('Description')}
            fieldId="catalog-item-description"
            multiline
          />
          <ResourceSelectField
            name="template"
            label={t('Provisioning template')}
            fieldId={templateFieldId}
            service={templateService}
            isRequired
            isDisabled={isEdit}
            placeholder={t('Select a provisioning template')}
            loadErrorTitle={t('Failed to fetch provisioning templates')}
          />
          <Content component="p">
            {isEdit
              ? t('The provisioning template cannot be changed after the catalog item is created.')
              : t(
                  'The selected template is the provisioning template used to create resources from this catalog item.',
                )}
          </Content>
        </OsacForm>
      </StackItem>
    </Stack>
  );
};

export default CatalogGeneralStep;
