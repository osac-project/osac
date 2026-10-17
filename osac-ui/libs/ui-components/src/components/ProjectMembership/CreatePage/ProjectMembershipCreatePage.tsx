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
import { Formik } from 'formik';

import { ProjectMembershipRole, Users } from '@osac/types';
import { useListResource } from '@osac/ui-components/api/use-resource';
import { useProject } from '@osac/ui-components/api/v1/project';
import {
  useCreateProjectMembership,
  useProjectMembership,
  useUpdateProjectMembership,
} from '@osac/ui-components/api/v1/project-membership';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';
import { getErrorMessage } from '@osac/ui-components/utils/error';

import { getCreateProjectMembershipPayload, getUpdateProjectMembershipPayload } from './payload';
import { getProjectMembershipValidationSchema } from './validation';
import { getInitialValues } from './values';
import LeaveFormConfirmation from '../../Form/LeaveFormConfirmation';
import { MultiSelectField } from '../../Form/MultiSelectField';
import NameField from '../../Form/NameField';
import OsacForm from '../../Form/OsacForm';
import { SelectField } from '../../Form/SelectField';
import { getFullProjectPath, getProjectName } from '../../Project/utils';
import { getRoleLabel } from '../utils';

const ProjectMembershipCreatePage = () => {
  const { projectId, pmId } = useParams() as { projectId?: string; pmId?: string };
  const { t } = useTranslation();
  const navigate = useNavigate();

  const { mutateAsync: create, error: createErr } = useCreateProjectMembership();
  const { mutateAsync: update, error: updateErr } = useUpdateProjectMembership();

  const { data: project, isLoading: projectLoading, error: projectErr } = useProject(projectId);
  const { data: users, isLoading: usersLoading, error: usersError } = useListResource(Users);
  const { data: pm, isLoading: pmLoading, error: pmError } = useProjectMembership(pmId);

  const roles = getRoleLabel(t);

  if (projectLoading || pmLoading) {
    return (
      <Bullseye>
        <Spinner />
      </Bullseye>
    );
  }

  if (projectErr) {
    return (
      <Alert variant="danger" isInline title={t('Failed to load project')}>
        {getErrorMessage(projectErr)}
      </Alert>
    );
  }

  if (pmError) {
    return (
      <Alert variant="danger" isInline title={t('Failed to load project membership')}>
        {getErrorMessage(pmError)}
      </Alert>
    );
  }

  return (
    <>
      <PageSection hasBodyWrapper={false}>
        <Stack hasGutter>
          <Breadcrumb>
            <BreadcrumbItem>
              <Button variant="link" isInline onClick={() => navigate('/projects')}>
                {t('Projects')}
              </Button>
            </BreadcrumbItem>
            <BreadcrumbItem>
              <Button variant="link" isInline onClick={() => navigate(`/projects/${projectId}`)}>
                {project && getProjectName(project, t)}
              </Button>
            </BreadcrumbItem>
            <BreadcrumbItem isActive>
              {pmId ? t('Edit project membership') : t('Create project membership')}
            </BreadcrumbItem>
          </Breadcrumb>
          <Title headingLevel="h1" size="3xl">
            {pmId ? t('Edit project membership') : t('Create project membership')}
          </Title>
        </Stack>
      </PageSection>
      <PageSection hasBodyWrapper={false}>
        <Formik
          initialValues={getInitialValues(pm)}
          validationSchema={getProjectMembershipValidationSchema(t)}
          onSubmit={async (values) => {
            try {
              if (pmId) {
                await update({ id: pmId, body: getUpdateProjectMembershipPayload(values) });
              } else {
                await create(
                  getCreateProjectMembershipPayload(values, getFullProjectPath(project)),
                );
              }
              navigate(`/projects/${projectId}`);
            } catch {
              // tanstack handles the err
            }
          }}
        >
          {({ submitForm, isSubmitting }) => (
            <>
              <LeaveFormConfirmation />
              <Stack hasGutter>
                <StackItem>
                  <OsacForm>
                    <NameField isDisabled={!!pmId} />

                    <MultiSelectField
                      fieldId="users"
                      label={t('Users')}
                      name="users"
                      options={(users?.items || []).map((user) => ({
                        label: user.spec?.username || user.metadata?.name || user.id,
                        value: user.metadata?.name || user.id,
                      }))}
                      isLoading={usersLoading}
                      isDisabled={!!usersError}
                    />
                    {!!usersError && (
                      <Alert variant="danger" isInline title={t('Failed to load users')}>
                        {getErrorMessage(usersError)}
                      </Alert>
                    )}
                    <SelectField
                      fieldId="role"
                      label={t('Role')}
                      name="role"
                      options={[
                        {
                          label: roles[ProjectMembershipRole.VIEWER],
                          value: ProjectMembershipRole.VIEWER,
                        },
                        {
                          label: roles[ProjectMembershipRole.MANAGER],
                          value: ProjectMembershipRole.MANAGER,
                        },
                      ]}
                    />
                  </OsacForm>
                </StackItem>

                {!!createErr && (
                  <StackItem>
                    <Alert
                      variant="danger"
                      title={t('Failed to create project membership')}
                      isInline
                    >
                      {getErrorMessage(createErr)}
                    </Alert>
                  </StackItem>
                )}
                {!!updateErr && (
                  <StackItem>
                    <Alert
                      variant="danger"
                      title={t('Failed to update project membership')}
                      isInline
                    >
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
                          {pmId ? t('Edit') : t('Create')}
                        </Button>
                      </ActionListItem>
                      <ActionListItem>
                        <Button
                          variant="link"
                          onClick={() => navigate(`/projects/${projectId}`)}
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
      </PageSection>
    </>
  );
};

export default ProjectMembershipCreatePage;
