import { useNavigate, useParams } from 'react-router-dom';
import {
  ActionList,
  ActionListGroup,
  ActionListItem,
  Alert,
  Breadcrumb,
  BreadcrumbItem,
  Bullseye,
  Button,
  PageSection,
  Spinner,
  Stack,
  StackItem,
  Title,
  Tooltip,
} from '@patternfly/react-core';
import HelpIcon from '@patternfly/react-icons/dist/esm/icons/help-icon';
import { Formik } from 'formik';
import type { TFunction } from 'i18next';
import * as Yup from 'yup';

import type { StorageBackend } from '@osac/types/private';
import {
  useCreateStorageBackend,
  usePrivateStorageBackend,
  useUpdateStorageBackend,
} from '@osac/ui-components/api/v1/private/storage-backends';
import { CheckboxField } from '@osac/ui-components/components/Form/CheckboxField';
import { InputField } from '@osac/ui-components/components/Form/InputField';
import LeaveFormConfirmation from '@osac/ui-components/components/Form/LeaveFormConfirmation';
import NameField from '@osac/ui-components/components/Form/NameField';
import OsacForm from '@osac/ui-components/components/Form/OsacForm';
import {
  SelectField,
  type SelectFieldOption,
} from '@osac/ui-components/components/Form/SelectField';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';
import { getErrorMessage } from '@osac/ui-components/utils/error';
import { resourceNameSchema } from '@osac/ui-components/validation/resource-name';

const BACKENDS_LIST_PATH = '/admin/infrastructure/storage/backends';

interface StorageBackendFormValues {
  metadata: { name: string };
  provider: string;
  cspManaged: boolean;
  endpoint: string;
  description: string;
  credentials: { username: string; password: string };
}

const getInitialValues = (backend?: StorageBackend): StorageBackendFormValues => ({
  metadata: { name: backend?.metadata?.name ?? '' },
  provider: backend?.spec?.provider ?? '',
  cspManaged: backend?.spec?.cspManaged ?? false,
  endpoint: backend?.spec?.endpoint ?? '',
  description: backend?.spec?.description ?? '',
  credentials: { username: '', password: '' },
});

const getStorageBackendSchema = (t: TFunction, isEdit: boolean) => {
  const pairError = t('Enter both username and password, or leave both blank');
  const pairedCredentials = Yup.object({
    username: Yup.string().test('credentials-pair', pairError, function (value) {
      const parent = this.parent as { username?: string; password?: string } | undefined;
      return !!value === !!parent?.password;
    }),
    password: Yup.string().test('credentials-pair', pairError, function (value) {
      const parent = this.parent as { username?: string; password?: string } | undefined;
      return !!value === !!parent?.username;
    }),
  });
  return Yup.object({
    metadata: Yup.object({ name: resourceNameSchema(t) }),
    provider: Yup.string().required(t('Provider is required')),
    cspManaged: isEdit
      ? Yup.boolean()
      : Yup.boolean().when('provider', {
          is: 'netapp',
          then: (schema) => schema.oneOf([true], t('NetApp requires CSP-managed ownership')),
          otherwise: (schema) =>
            schema.oneOf([false], t('CSP-managed ownership is only available for NetApp')),
        }),
    endpoint: Yup.string().when('cspManaged', {
      is: false,
      then: (schema) => schema.required(t('Endpoint is required')),
    }),
    description: Yup.string(),
    // On edit, both blank keeps the credentials unchanged. CSP-managed backends
    // may also omit them on create; a partial pair is never valid.
    credentials: isEdit
      ? pairedCredentials
      : Yup.object().when('cspManaged', {
          is: true,
          then: () => pairedCredentials,
          otherwise: () =>
            Yup.object({
              username: Yup.string().required(t('Username is required')),
              password: Yup.string().required(t('Password is required')),
            }),
        }),
  });
};

const StorageBackendForm = ({ backend }: { backend?: StorageBackend }) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const isEdit = !!backend;
  const { mutateAsync: create, error: createError } = useCreateStorageBackend();
  const { mutateAsync: update, error: updateError } = useUpdateStorageBackend();
  const error = isEdit ? updateError : createError;

  const providerOptions: SelectFieldOption[] = [
    { value: 'vast', label: t('VAST') },
    { value: 'ceph', label: t('Ceph') },
    { value: 'pure', label: t('Pure') },
    { value: 'netapp', label: t('NetApp') },
  ];

  const onSubmit = async (values: StorageBackendFormValues) => {
    const credentials =
      values.credentials.username && values.credentials.password
        ? { credentials: values.credentials }
        : {};
    try {
      if (backend) {
        await update({
          id: backend.id,
          version: backend.metadata?.version ?? 0,
          spec: {
            endpoint: values.endpoint,
            description: values.description,
            ...credentials,
          },
        });
        navigate(BACKENDS_LIST_PATH);
      } else {
        const created = await create({
          metadata: values.metadata,
          spec: {
            provider: values.provider,
            cspManaged: values.cspManaged,
            endpoint: values.endpoint,
            description: values.description,
            ...credentials,
          },
        });
        navigate(`${BACKENDS_LIST_PATH}/${created.id}`);
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
              <Button variant="link" isInline onClick={() => navigate(BACKENDS_LIST_PATH)}>
                {t('Storage Backends')}
              </Button>
            </BreadcrumbItem>
            <BreadcrumbItem isActive>{isEdit ? t('Edit') : t('Create')}</BreadcrumbItem>
          </Breadcrumb>
          <Title headingLevel="h1" size="3xl">
            {isEdit ? t('Edit storage backend') : t('Create storage backend')}
          </Title>
        </Stack>
      </PageSection>
      <PageSection hasBodyWrapper={false}>
        <Formik
          initialValues={getInitialValues(backend)}
          validationSchema={getStorageBackendSchema(t, isEdit)}
          onSubmit={onSubmit}
        >
          {({ submitForm, isSubmitting, setFieldValue, values }) => (
            <Stack hasGutter>
              <LeaveFormConfirmation />
              <StackItem>
                <OsacForm>
                  <NameField isDisabled={isEdit} />
                  <SelectField
                    name="provider"
                    label={t('Provider')}
                    fieldId="storage-backend-provider"
                    isRequired
                    isDisabled={isEdit}
                    options={providerOptions}
                    onSelect={(provider) => void setFieldValue('cspManaged', provider === 'netapp')}
                  />
                  <CheckboxField
                    name="cspManaged"
                    label={
                      <>
                        {t('CSP-managed')}{' '}
                        <Tooltip content={t('Automatically set based on the provider.')}>
                          <span
                            role="img"
                            aria-label={t('Automatically set based on the provider.')}
                            tabIndex={0}
                          >
                            <HelpIcon aria-hidden />
                          </span>
                        </Tooltip>
                      </>
                    }
                    ariaLabel={t('CSP-managed')}
                    fieldId="storage-backend-csp-managed"
                    isDisabled
                  />
                  <InputField
                    name="endpoint"
                    label={t('Endpoint')}
                    fieldId="storage-backend-endpoint"
                    isRequired={!values.cspManaged}
                  />
                  <InputField
                    name="description"
                    label={t('Description')}
                    fieldId="storage-backend-description"
                  />
                  <InputField
                    name="credentials.username"
                    label={t('Username')}
                    fieldId="storage-backend-username"
                    isRequired={!isEdit && !values.cspManaged}
                    helperText={
                      isEdit ? t('Leave blank to keep the current credentials.') : undefined
                    }
                  />
                  <InputField
                    name="credentials.password"
                    label={t('Password')}
                    fieldId="storage-backend-password"
                    type="password"
                    isRequired={!isEdit && !values.cspManaged}
                  />
                </OsacForm>
              </StackItem>

              {!!error && (
                <StackItem>
                  <Alert
                    variant="danger"
                    title={
                      isEdit
                        ? t('Failed to update storage backend')
                        : t('Failed to create storage backend')
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
                        onClick={() => navigate(BACKENDS_LIST_PATH)}
                        isDisabled={isSubmitting}
                      >
                        {t('Cancel')}
                      </Button>
                    </ActionListItem>
                  </ActionListGroup>
                </ActionList>
              </StackItem>
            </Stack>
          )}
        </Formik>
      </PageSection>
    </>
  );
};

export const StorageBackendCreatePage = () => {
  const { t } = useTranslation();
  const { id } = useParams<{ id: string }>();
  const { data, isLoading, error } = usePrivateStorageBackend(id ?? '');

  if (id) {
    if (isLoading) {
      return (
        <Bullseye>
          <Spinner />
        </Bullseye>
      );
    }

    if (error) {
      return (
        <Alert variant="danger" isInline title={t('Failed to fetch storage backend')}>
          {getErrorMessage(error)}
        </Alert>
      );
    }
  }

  return <StorageBackendForm backend={data} />;
};
