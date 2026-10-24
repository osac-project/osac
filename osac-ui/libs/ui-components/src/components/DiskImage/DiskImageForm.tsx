import { useNavigate } from 'react-router-dom';
import { Code, ConnectError } from '@connectrpc/connect';
import {
  ActionList,
  ActionListGroup,
  ActionListItem,
  Alert,
  Button,
  Content,
  FormGroup,
  Stack,
  StackItem,
} from '@patternfly/react-core';
import { Formik, type FormikHelpers } from 'formik';

import { SourceType } from '@osac/types';
import { Tenants } from '@osac/types/private';

import { architectureLabels, guestOsFamilyLabels } from './DiskImageTable';
import { diskImageFieldIds } from './fieldIds';
import { getDiskImageCreateSchema } from './validation';
import { DiskImageFormValues, diskImageCreateValues } from './values';
import { useListResource } from '../../api/use-resource';
import { useCreateDiskImage } from '../../api/v1/disk-image';
import { useSession } from '../../hooks/use-session';
import { useTranslation } from '../../hooks/useTranslation';
import { getErrorMessage } from '../../utils/error';
import { InputField } from '../Form/InputField';
import LeaveFormConfirmation from '../Form/LeaveFormConfirmation';
import { MultiSelectField } from '../Form/MultiSelectField';
import NameField from '../Form/NameField';
import OsacForm from '../Form/OsacForm';
import { SelectField } from '../Form/SelectField';

export const DISK_IMAGES_LIST_ROUTE = '/admin/infrastructure/disk-images';

const FIELD_ERROR_MATCHERS: { pattern: RegExp; field: string }[] = [
  { pattern: /source_ref/, field: 'spec.sourceRef' },
  { pattern: /architecture/, field: 'spec.architecture' },
];

const mapInvalidArgumentToField = (message: string): string | undefined =>
  FIELD_ERROR_MATCHERS.find(({ pattern }) => pattern.test(message))?.field;

const ReadOnlyField = ({
  label,
  fieldId,
  value,
}: {
  label: string;
  fieldId: string;
  value: string;
}) => (
  <FormGroup label={label} fieldId={fieldId}>
    <Content component="p">{value}</Content>
  </FormGroup>
);

const DiskImageForm = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const isAdmin = useSession().role === 'admin';
  const { data: tenantsResponse } = useListResource(Tenants, {}, { enabled: isAdmin });
  const tenants = tenantsResponse?.items ?? [];
  const { mutateAsync: create, error } = useCreateDiskImage();
  const errorMessage = error ? getErrorMessage(error) : undefined;
  const errorField =
    error instanceof ConnectError && error.code === Code.InvalidArgument
      ? mapInvalidArgumentToField(error.rawMessage)
      : undefined;

  const guestOsFamilyOptions = Object.entries(guestOsFamilyLabels(t))
    .filter(([value]) => Number(value) !== 0)
    .map(([value, label]) => ({ value: Number(value), label }));

  const architectureOptions = Object.entries(architectureLabels(t))
    .filter(([value]) => Number(value) !== 0)
    .map(([value, label]) => ({ value: Number(value), label }));

  const scopeOptions = [
    { value: '', label: t('Global') },
    ...tenants.map((tenant) => ({ value: tenant.id, label: tenant.metadata?.name || tenant.id })),
  ];

  const onSubmit = async (
    values: DiskImageFormValues,
    { setFieldError }: FormikHelpers<DiskImageFormValues>,
  ) => {
    try {
      const created = await create({
        metadata: {
          name: values.metadata.name,
          ...(isAdmin && values.metadata.tenant ? { tenant: values.metadata.tenant } : {}),
        },
        spec: {
          sourceType: SourceType.REGISTRY,
          sourceRef: values.spec.sourceRef,
          guestOsFamily: values.spec.guestOsFamily,
          architecture: values.spec.architecture,
        },
      });
      navigate(`${DISK_IMAGES_LIST_ROUTE}/${created.id}`);
    } catch (err) {
      if (err instanceof ConnectError && err.code === Code.InvalidArgument) {
        const field = mapInvalidArgumentToField(err.rawMessage);
        if (field) {
          setFieldError(field, err.rawMessage);
        }
      }
      // otherwise the generic Alert below (driven by the mutation's error state) applies
    }
  };

  return (
    <Formik
      initialValues={diskImageCreateValues}
      validationSchema={getDiskImageCreateSchema(t)}
      onSubmit={onSubmit}
    >
      {({ submitForm, isSubmitting }) => (
        <>
          <LeaveFormConfirmation />
          <Stack hasGutter>
            <StackItem>
              <OsacForm>
                <NameField fieldId={diskImageFieldIds.name} />
                <ReadOnlyField
                  label={t('Source type')}
                  fieldId={diskImageFieldIds.sourceType}
                  value={SourceType[SourceType.REGISTRY]}
                />
                <InputField
                  name="spec.sourceRef"
                  label={t('Source reference')}
                  fieldId={diskImageFieldIds.sourceReference}
                  isRequired
                />
                <SelectField
                  name="spec.guestOsFamily"
                  label={t('Guest OS family')}
                  fieldId={diskImageFieldIds.guestOsFamily}
                  isRequired
                  options={guestOsFamilyOptions}
                />
                <MultiSelectField
                  name="spec.architecture"
                  label={t('Architecture')}
                  fieldId={diskImageFieldIds.architecture}
                  isRequired
                  options={architectureOptions}
                />
                {isAdmin && (
                  <SelectField
                    name="metadata.tenant"
                    label={t('Scope')}
                    fieldId={diskImageFieldIds.scope}
                    options={scopeOptions}
                  />
                )}
              </OsacForm>
            </StackItem>
            {!!error && !errorField && (
              <StackItem>
                <Alert variant="danger" title={t('Failed to save disk image')} isInline>
                  {errorMessage}
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
                      {t('Create')}
                    </Button>
                  </ActionListItem>
                  <ActionListItem>
                    <Button
                      variant="link"
                      onClick={() => navigate(DISK_IMAGES_LIST_ROUTE)}
                      isDisabled={isSubmitting}
                    >
                      {t('Cancel')}
                    </Button>
                  </ActionListItem>
                </ActionListGroup>
              </ActionList>
            </StackItem>
          </Stack>
        </>
      )}
    </Formik>
  );
};

export default DiskImageForm;
