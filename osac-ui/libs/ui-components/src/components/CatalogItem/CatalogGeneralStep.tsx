import {
  Content,
  FormGroupLabelHelp,
  Popover,
  Stack,
  StackItem,
  Title,
} from '@patternfly/react-core';
import { useFormikContext } from 'formik';

import CatalogScopeField from './CatalogScopeField';
import { useSession } from '../../hooks/use-session';
import { useTranslation } from '../../hooks/useTranslation';
import { GLOBAL_TENANT_VALUE } from '../catalog/catalogItemDisplay';
import TextAreaField from '../Form/base/TextAreaField';
import NameField from '../Form/NameField';
import OsacForm from '../Form/OsacForm';
import ProjectField from '../Form/ProjectField';
import {
  ResourceSelectField,
  type ResourceSelectFieldProps,
  type ResourceSelectValue,
  emptyResourceSelectValue,
} from '../Form/ResourceSelectField';
import TenantSelectField from '../Form/TenantSelectField';

export type CatalogItemScope = 'global' | 'tenant';

export interface CatalogGeneralValues {
  scope: CatalogItemScope;
  description: string;
  metadata: {
    name: string;
    project: string;
    /** Selected tenant when scope is `tenant`. Empty when scope is `global`. */
    tenant: ResourceSelectValue;
  };
  template: ResourceSelectValue;
}

export const catalogGeneralValues = (): CatalogGeneralValues => ({
  scope: 'tenant',
  description: '',
  metadata: { name: '', project: '', tenant: emptyResourceSelectValue() },
  template: emptyResourceSelectValue(),
});

/** Shape shared by all catalog item kinds, used to prefill `CatalogGeneralValues` for edit. */
export interface CatalogItemGeneralLike {
  description?: string;
  metadata?: {
    name?: string;
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
    description: item.description ?? '',
    metadata: {
      name: metadata?.name ?? '',
      project: metadata?.project ?? '',
      tenant: scope === 'tenant' && tenant ? { id: tenant, name: '' } : emptyResourceSelectValue(),
    },
    template: item.template?.id ? { id: item.template.id, name: '' } : emptyResourceSelectValue(),
  };
};

/**
 * Encodes `CatalogGeneralValues` into the `metadata`/`template` shape shared by every
 * catalog item create payload. `tenant` and `project` are mutually exclusive by
 * construction: an admin populates `scope`/`metadata.tenant`, while a
 * tenant-admin-created item omits `metadata.project` and is scoped to the
 * authenticated tenant by the API. Existing project metadata is retained when
 * editing legacy tenant-admin items.
 */
export const catalogGeneralItemPayload = (
  values: CatalogGeneralValues,
): { name: string; tenant?: string; project?: string } => {
  const tenant = values.scope === 'global' ? GLOBAL_TENANT_VALUE : values.metadata.tenant.id;
  const project = values.metadata.project.trim();
  return {
    name: values.metadata.name.trim(),
    ...(tenant ? { tenant } : {}),
    ...(project ? { project } : {}),
  };
};

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
  const { values } = useFormikContext<CatalogGeneralValues>();

  return (
    <Stack hasGutter>
      <StackItem>
        <Title headingLevel="h2" size="lg">
          {t('General')}
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
              {values.scope === 'tenant' && <TenantSelectField isDisabled={isEdit} />}
            </>
          ) : (
            <ProjectField isDisabled={isEdit} />
          )}
          <NameField isDisabled={isEdit} />
          <TextAreaField
            name="description"
            label={t('Description')}
            fieldId="catalog-item-description"
          />
          <ResourceSelectField
            name="template"
            label={t('Provisioning template')}
            fieldId={templateFieldId}
            service={templateService}
            isRequired
            isDisabled={isEdit}
            autoSelectSingleOption
            placeholder={t('Select a provisioning template')}
            loadErrorTitle={t('Failed to fetch provisioning templates')}
            labelHelp={
              <Popover
                aria-label={t('Provisioning template information')}
                bodyContent={t('Defines the Ansible role used to provision this catalog item')}
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
