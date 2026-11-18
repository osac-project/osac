import { useNavigate, useParams } from 'react-router-dom';
import {
  ActionList,
  ActionListGroup,
  ActionListItem,
  Alert,
  Breadcrumb,
  BreadcrumbItem,
  Button,
  PageSection,
  Stack,
  StackItem,
  Title,
} from '@patternfly/react-core';
import { Formik } from 'formik';
import type { TFunction } from 'i18next';
import * as Yup from 'yup';

import type { StorageBackend, StorageTier } from '@osac/types/private';
import {
  StorageBackendState,
  StorageBackends,
  StorageProtocol,
  StorageTiers,
} from '@osac/types/private';
import { cel } from '@osac/ui-components/api/cel';
import {
  useCreateResource,
  useGetResource,
  useListResource,
  useUpdateResource,
} from '@osac/ui-components/api/use-resource';

import { storageTierFieldIds } from './fieldIds';
import { useTranslation } from '../../hooks/useTranslation';
import { getErrorMessage } from '../../utils/error';
import { resourceNameSchema } from '../../validation/resource-name';
import TextField from '../Form/base/TextField';
import LeaveFormConfirmation from '../Form/LeaveFormConfirmation';
import NameField from '../Form/NameField';
import OsacForm from '../Form/OsacForm';
import { RadioButtonField } from '../Form/RadioButtonField';
import { SelectField, type SelectFieldOption } from '../Form/SelectField';
import ListPageBody from '../Page/ListPageBody';

const STORAGE_BACKEND_READY_LIST_FILTER = cel<StorageBackend>((filter) =>
  filter.field('status.state').equals(StorageBackendState.READY),
);

const TIERS_LIST_PATH = '/admin/infrastructure/storage/tiers';

const PROTOCOL_BY_VALUE: Record<'NFS' | 'BLOCK', StorageProtocol> = {
  NFS: StorageProtocol.NFS,
  BLOCK: StorageProtocol.BLOCK,
};

const VALUE_BY_PROTOCOL: Partial<Record<StorageProtocol, 'NFS' | 'BLOCK'>> = {
  [StorageProtocol.NFS]: 'NFS',
  [StorageProtocol.BLOCK]: 'BLOCK',
};

interface StorageTierFormValues {
  metadata: { name: string };
  description: string;
  backendId: string;
  protocol: '' | 'NFS' | 'BLOCK';
}

const getInitialValues = (tier?: StorageTier): StorageTierFormValues => {
  const backend = tier?.spec?.backends[0];
  return {
    metadata: { name: tier?.metadata?.name ?? '' },
    description: tier?.spec?.description ?? '',
    backendId: backend?.backendId ?? '',
    protocol: tier?.spec ? (VALUE_BY_PROTOCOL[tier.spec.protocol] ?? '') : '',
  };
};

const getStorageTierSchema = (t: TFunction) =>
  Yup.object({
    metadata: Yup.object({ name: resourceNameSchema(t) }),
    description: Yup.string(),
    backendId: Yup.string().required(t('Backend is required')),
    protocol: Yup.string()
      .oneOf(['NFS', 'BLOCK'], t('Protocol is required'))
      .required(t('Protocol is required')),
  });

interface StorageTierFormProps {
  tier?: StorageTier;
  backendOptions: SelectFieldOption[];
  backendsLoading: boolean;
}

const StorageTierForm = ({ tier, backendOptions, backendsLoading }: StorageTierFormProps) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const isEdit = !!tier;

  const { mutateAsync: create, error: createError } = useCreateResource(StorageTiers);
  const { mutateAsync: update, error: updateError } = useUpdateResource(StorageTiers);
  const error = isEdit ? updateError : createError;

  const initialValues = getInitialValues(tier);

  const onSubmit = async (values: StorageTierFormValues) => {
    try {
      const protocol = PROTOCOL_BY_VALUE[values.protocol as 'NFS' | 'BLOCK'];
      const backendPayload = {
        backendId: values.backendId,
      };

      if (tier) {
        const descriptionChanged = values.description !== initialValues.description;
        const spec: {
          protocol: StorageProtocol;
          backends: [Record<string, unknown>];
          description?: string;
        } = {
          protocol,
          backends: [backendPayload],
        };
        if (descriptionChanged) {
          spec.description = values.description;
        }
        await update({
          object: { id: tier.id, metadata: { version: tier.metadata?.version ?? 0 }, spec },
        });
        navigate(TIERS_LIST_PATH);
      } else {
        const created = await create({
          object: {
            metadata: values.metadata,
            spec: {
              description: values.description,
              protocol,
              backends: [backendPayload],
            },
          },
        });
        navigate(`${TIERS_LIST_PATH}/${created.object?.id}`);
      }
    } catch {
      // Surfaced via the mutation's own `error` state below; nothing further to do here.
    }
  };

  return (
    <>
      <PageSection hasBodyWrapper={false}>
        <Stack hasGutter>
          <Breadcrumb>
            <BreadcrumbItem>
              <Button variant="link" isInline onClick={() => navigate(TIERS_LIST_PATH)}>
                {t('Storage tiers')}
              </Button>
            </BreadcrumbItem>
            <BreadcrumbItem isActive>{isEdit ? t('Edit') : t('Create')}</BreadcrumbItem>
          </Breadcrumb>
          <Title headingLevel="h1" size="3xl">
            {isEdit ? t('Edit storage tier') : t('Create storage tier')}
          </Title>
        </Stack>
      </PageSection>
      <PageSection hasBodyWrapper={false}>
        <Formik
          initialValues={initialValues}
          validationSchema={getStorageTierSchema(t)}
          onSubmit={onSubmit}
        >
          {({ values, submitForm, isSubmitting }) => {
            const qosChanged = isEdit && values.protocol !== initialValues.protocol;

            return (
              <Stack hasGutter>
                <LeaveFormConfirmation />
                <StackItem>
                  <OsacForm>
                    <NameField fieldId={storageTierFieldIds.name} isDisabled={isEdit} />
                    <TextField
                      name="description"
                      label={t('Description')}
                      fieldId={storageTierFieldIds.description}
                    />
                    <SelectField
                      name="backendId"
                      label={t('Backend')}
                      fieldId={storageTierFieldIds.backend}
                      isRequired
                      isLoading={backendsLoading}
                      placeholder={t('Select a backend')}
                      options={backendOptions}
                    />
                    <RadioButtonField
                      name="protocol"
                      label={t('Protocol')}
                      fieldId={storageTierFieldIds.protocol}
                      isRequired
                      isInline
                      options={[
                        { value: 'NFS', label: t('NFS') },
                        { value: 'BLOCK', label: t('Block') },
                      ]}
                    />
                  </OsacForm>
                </StackItem>

                {qosChanged && (
                  <StackItem>
                    <Alert variant="info" isInline title={t('QoS settings changed')}>
                      {t(
                        'Protocol changes require the associated StorageClass to be recreated before new volumes pick them up; existing volumes are unaffected.',
                      )}
                    </Alert>
                  </StackItem>
                )}

                {!!error && (
                  <StackItem>
                    <Alert
                      variant="danger"
                      title={
                        isEdit
                          ? t('Failed to update storage tier')
                          : t('Failed to create storage tier')
                      }
                      isInline
                    >
                      {getErrorMessage(error)}
                    </Alert>
                  </StackItem>
                )}
                <StackItem>
                  <ActionList>
                    <ActionListGroup>
                      <ActionListItem>
                        <Button
                          variant="primary"
                          onClick={submitForm}
                          isDisabled={isSubmitting}
                          isLoading={isSubmitting}
                        >
                          {isEdit ? t('Save') : t('Create')}
                        </Button>
                      </ActionListItem>
                      <ActionListItem>
                        <Button
                          variant="link"
                          onClick={() => navigate(TIERS_LIST_PATH)}
                          isDisabled={isSubmitting}
                        >
                          {t('Cancel')}
                        </Button>
                      </ActionListItem>
                    </ActionListGroup>
                  </ActionList>
                </StackItem>
              </Stack>
            );
          }}
        </Formik>
      </PageSection>
    </>
  );
};

const StorageTierCreatePage = () => {
  const { id } = useParams<{ id: string }>();
  const {
    data: tier,
    isLoading,
    error,
  } = useGetResource(StorageTiers, { id: id ?? '' }, { enabled: !!id });

  const assignedBackendId = tier?.object?.spec?.backends[0]?.backendId ?? '';
  const { data: readyBackends, isLoading: backendsLoading } = useListResource(StorageBackends, {
    filter: STORAGE_BACKEND_READY_LIST_FILTER,
  });
  const { data: assignedBackend } = useGetResource(
    StorageBackends,
    { id: assignedBackendId },
    { enabled: !!assignedBackendId },
  );

  const backendOptions = (readyBackends?.items || []).map((backend) => ({
    value: backend.id,
    label: backend.metadata?.name ?? backend.id,
  }));
  if (
    assignedBackend?.object &&
    !backendOptions.some((option) => option.value === assignedBackend.object?.id)
  ) {
    backendOptions.push({
      value: assignedBackend.object.id,
      label: assignedBackend.object.metadata?.name ?? assignedBackend.object.id,
    });
  }

  if (id) {
    return (
      <ListPageBody isLoading={isLoading} error={error}>
        {tier && (
          <StorageTierForm
            tier={tier.object}
            backendOptions={backendOptions}
            backendsLoading={backendsLoading}
          />
        )}
      </ListPageBody>
    );
  }

  return <StorageTierForm backendOptions={backendOptions} backendsLoading={backendsLoading} />;
};

export default StorageTierCreatePage;
