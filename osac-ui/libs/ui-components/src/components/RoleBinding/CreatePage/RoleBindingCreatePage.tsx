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
} from '@patternfly/react-core';
import { Formik, useFormikContext } from 'formik';

import { RoleBinding, RoleBindings, Roles, User, Users } from '@osac/types';
import { Tenants } from '@osac/types/private';
import { cel } from '@osac/ui-components/api/cel';

import { getRoleBindingSpec } from './payload';
import { validationSchema } from './validation';
import { RoleBindingCreateFormValues, getInitialValues } from './values';
import {
  useCreateResource,
  useGetResource,
  useListResource,
  useUpdateResource,
} from '../../../api/use-resource';
import { useSession } from '../../../hooks/use-session';
import { useTranslation } from '../../../hooks/useTranslation';
import { getErrorMessage } from '../../../utils/error';
import LeaveFormConfirmation from '../../Form/LeaveFormConfirmation';
import { MultiSelectField } from '../../Form/MultiSelectField';
import NameField from '../../Form/NameField';
import OsacForm from '../../Form/OsacForm';
import { SelectField } from '../../Form/SelectField';

const RoleBindingCreateForm = ({ isEdit }: { isEdit: boolean }) => {
  const { t } = useTranslation();
  const { values } = useFormikContext<RoleBindingCreateFormValues>();
  const { role: sessionRole } = useSession();

  const isAdmin = sessionRole === 'admin';

  const { data: tenantsResponse, isLoading: tenantsLoading } = useListResource(
    Tenants,
    {},
    { enabled: isAdmin },
  );
  const tenants = tenantsResponse?.items ?? [];

  const { data: users, isLoading: usersLoading } = useListResource(
    Users,
    {
      filter: cel<User>((filter) => filter.field('metadata.tenant').equals(values.metadata.tenant)),
    },
    { enabled: !!values.metadata.tenant },
  );

  const { data: roles, isLoading: rolesLoading } = useListResource(Roles);

  return (
    <OsacForm>
      {isAdmin && (
        <SelectField
          fieldId="metadata.tenant"
          name="metadata.tenant"
          label={t('Tenant')}
          isRequired
          isLoading={tenantsLoading}
          options={tenants.map((tenant) => ({
            label: tenant.metadata?.name || tenant.id,
            value: tenant.id,
          }))}
          isDisabled={isEdit}
        />
      )}
      <NameField isDisabled={isEdit} />
      <MultiSelectField
        fieldId="users"
        name="users"
        label={t('Users')}
        isRequired
        isDisabled={!values.metadata.tenant}
        isLoading={usersLoading}
        options={(users?.items || []).map((user) => ({
          label: user.spec?.username || user.metadata?.name || user.id,
          value: user.metadata?.name || user.id,
        }))}
      />
      <SelectField
        fieldId="role"
        name="role"
        label={t('Role')}
        isRequired
        isLoading={rolesLoading}
        options={(roles?.items || []).map((role) => ({
          label: role.spec?.title || role.metadata?.name || role.id,
          value: role.metadata?.name || role.id,
        }))}
      />
    </OsacForm>
  );
};

interface RoleBindingCreatePageProps {
  roleBinding?: RoleBinding;
}

const RoleBindingCreatePageInner = ({ roleBinding }: RoleBindingCreatePageProps) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { tenantId } = useSession();
  const { mutateAsync: create, error: createErr } = useCreateResource(RoleBindings);
  const { mutateAsync: update, error: updateErr } = useUpdateResource(RoleBindings);

  const initialValues = getInitialValues(roleBinding, tenantId);

  const navigateToList = () => navigate('/tenant/role-binding');

  const onSubmit = async (values: RoleBindingCreateFormValues) => {
    try {
      if (roleBinding) {
        await update({
          object: {
            id: roleBinding.id,
            spec: getRoleBindingSpec(values),
          },
        });
      } else {
        await create({
          object: {
            metadata: values.metadata,
            spec: getRoleBindingSpec(values),
          },
        });
      }

      navigateToList();
    } catch {
      // tanstack handles the err
    }
  };

  return (
    <>
      <PageSection hasBodyWrapper={false}>
        <Stack hasGutter>
          <Breadcrumb>
            <BreadcrumbItem>
              <Button variant="link" isInline onClick={navigateToList}>
                {t('Role Bindings')}
              </Button>
            </BreadcrumbItem>
            {roleBinding && (
              <BreadcrumbItem>{roleBinding.metadata?.name || roleBinding.id}</BreadcrumbItem>
            )}
            <BreadcrumbItem isActive>{roleBinding ? t('Edit') : t('Create')}</BreadcrumbItem>
          </Breadcrumb>
          <Title headingLevel="h1" size="3xl">
            {roleBinding ? t('Update role binding') : t('Create role binding')}
          </Title>
        </Stack>
      </PageSection>
      <PageSection hasBodyWrapper={false}>
        <Formik<RoleBindingCreateFormValues>
          initialValues={initialValues}
          validationSchema={validationSchema(t)}
          onSubmit={onSubmit}
        >
          {({ isSubmitting, submitForm }) => (
            <>
              <LeaveFormConfirmation />
              <Stack hasGutter>
                <StackItem>
                  <RoleBindingCreateForm isEdit={!!roleBinding} />
                </StackItem>
                {createErr && (
                  <StackItem>
                    <Alert variant="danger" title={t('Failed to create role binding')} isInline>
                      {getErrorMessage(createErr)}
                    </Alert>
                  </StackItem>
                )}
                {updateErr && (
                  <StackItem>
                    <Alert variant="danger" title={t('Failed to update role binding')} isInline>
                      {getErrorMessage(updateErr)}
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
                          {roleBinding ? t('Update') : t('Create')}
                        </Button>
                      </ActionListItem>
                      <ActionListItem>
                        <Button variant="link" onClick={navigateToList} isDisabled={isSubmitting}>
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
      </PageSection>
    </>
  );
};

const RoleBindingCreatePage = () => {
  const { t } = useTranslation();
  const { id } = useParams<{ id: string }>();

  const { data, isLoading, error } = useGetResource(RoleBindings, { id }, { enabled: !!id });
  if (isLoading) {
    return (
      <Bullseye>
        <Spinner />
      </Bullseye>
    );
  }

  if (error) {
    return (
      <Alert isInline variant="danger" title={t('Failed to fetch role binding')}>
        {getErrorMessage(error)}
      </Alert>
    );
  }

  return <RoleBindingCreatePageInner roleBinding={data?.object} />;
};

export default RoleBindingCreatePage;
