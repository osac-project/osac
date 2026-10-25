import { useEffect } from 'react';
import {
  Content,
  FormGroupLabelHelp,
  Popover,
  Stack,
  StackItem,
  Title,
} from '@patternfly/react-core';
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
import { GLOBAL_TENANT_VALUE } from '../catalogItemDisplay';

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

/** Shape shared by all catalog item kinds, used to prefill `CatalogGeneralValues` for edit. */
export interface CatalogItemGeneralLike {
  metadata?: {
    name?: string;
    description?: string;
    project?: string;
    tenant?: string;
  };
  template?: { id?: string };
}

export const catalogGeneralValuesFromItem = (
  item: CatalogItemGeneralLike,
): CatalogGeneralValues => {
  const metadata = item.metadata;
  const tenant = metadata?.tenant ?? '';
  const scope: CatalogItemScope = tenant === GLOBAL_TENANT_VALUE ? 'global' : 'tenant';
  return {
    scope,
    metadata: {
      name: metadata?.name ?? '',
      project: metadata?.project ?? '',
      description: metadata?.description ?? '',
      tenant: scope === 'tenant' && tenant ? { id: tenant, name: '' } : emptyResourceSelectValue(),
    },
    template: item.template?.id ? { id: item.template.id, name: '' } : emptyResourceSelectValue(),
  };
};

/**
 * Encodes `CatalogGeneralValues` into the `metadata`/`template` shape shared by every
 * catalog item create payload. `tenant` and `project` are mutually exclusive by
 * construction: an admin populates `scope`/`metadata.tenant` and leaves
 * `metadata.project` empty, while a tenant admin does the opposite, so a single
 * derivation works for both roles.
 */
export const catalogGeneralItemPayload = (
  values: CatalogGeneralValues,
): { name: string; description: string; tenant?: string; project?: string } => {
  const tenant = values.scope === 'global' ? GLOBAL_TENANT_VALUE : values.metadata.tenant.id.trim();
  const project = values.metadata.project.trim();
  return {
    name: values.metadata.name.trim(),
    description: values.metadata.description.trim(),
    ...(tenant ? { tenant } : {}),
    ...(project ? { project } : {}),
  };
};

interface CatalogGeneralStepProps {
  isEdit: boolean;
  templateService: ResourceSelectFieldProps['service'];
  templateFieldId: string;
  heading?: string;
}

const CatalogGeneralStep = ({
  isEdit,
  templateService,
  templateFieldId,
  heading,
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
          {heading ?? t('General')}
        </Title>
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
            rows={4}
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
            labelHelp={
              <Popover
                aria-label={t('Provisioning template information')}
                bodyContent={t(
                  'Infrastructure automation that fulfills a VM request by resolving infrastructure dependencies, creating VM and storage resources, applying provider-specific integrations, and exposing the status.',
                )}
                headerContent={t('Provisioning template')}
                position="right"
              >
                <FormGroupLabelHelp aria-label={t('More information for Provisioning template')} />
              </Popover>
            }
          />
        </OsacForm>
      </StackItem>
    </Stack>
  );
};

export default CatalogGeneralStep;
