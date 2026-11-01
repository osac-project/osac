import { useNavigate } from 'react-router-dom';
import {
  Alert,
  Bullseye,
  Button,
  Flex,
  FlexItem,
  Spinner,
  Stack,
  StackItem,
  Title,
} from '@patternfly/react-core';
import PlusCircleIcon from '@patternfly/react-icons/dist/esm/icons/plus-circle-icon';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';

import { type Project, type ProjectMembership } from '@osac/types';
import { cel } from '@osac/ui-components/api/cel';
import { useProjectMemberships } from '@osac/ui-components/api/v1/project-membership';
import ResourceNameField from '@osac/ui-components/components/Resource/ResourceNameField.tsx';
import { useSession } from '@osac/ui-components/hooks/use-session';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';
import { getErrorMessage } from '@osac/ui-components/utils/error';

import ProjectMembershipActionsMenu from './ProjectMembershipActionsMenu';
import { getRoleLabel } from './utils';
import { Timestamp } from '../Primitives/Timestamp';
import { SubtleContent } from '../SubtleContent/SubtleContent';

const ProjectMembership = ({ project }: { project: Project }) => {
  const navigate = useNavigate();
  const { role } = useSession();
  const { t } = useTranslation();
  const { data, isLoading, error } = useProjectMemberships({
    filter: cel<ProjectMembership>((filter) =>
      filter.field('metadata.project').equals(project.metadata?.name || ''),
    ),
  });

  if (isLoading) {
    return (
      <Bullseye>
        <Spinner />
      </Bullseye>
    );
  }
  if (error) {
    return (
      <Alert variant="danger" title={t('Failed to load project memberships')} isInline>
        {getErrorMessage(error)}
      </Alert>
    );
  }

  const content = data?.length ? (
    <Table aria-label={t('Project memberships')} variant="compact">
      <Thead>
        <Tr>
          <Th>{t('Name')}</Th>
          <Th>{t('Role')}</Th>
          <Th>{t('Users')}</Th>
          <Th>{t('Created')}</Th>
          {role === 'tenant-admin' && <Th aria-label={t('Actions')} />}
        </Tr>
      </Thead>
      <Tbody>
        {data?.map((pm) => (
          <Tr key={pm.id}>
            <Td dataLabel={t('Name')}>
              <ResourceNameField resource={pm} />
            </Td>
            <Td dataLabel={t('Role')}>{pm.spec?.role ? getRoleLabel(t)[pm.spec.role] : '-'}</Td>
            <Td dataLabel={t('Users')}>
              {pm.spec?.users ? t('{{count}} user', { count: pm.spec?.users.length }) : '-'}
            </Td>
            <Td dataLabel={t('Created')}>
              <Timestamp value={pm.metadata?.creationTimestamp} />
            </Td>
            {role === 'tenant-admin' && (
              <Td isActionCell>
                <ProjectMembershipActionsMenu projectMembership={pm} projectId={project.id} />
              </Td>
            )}
          </Tr>
        ))}
      </Tbody>
    </Table>
  ) : (
    <SubtleContent>{t('No project memberships yet')}</SubtleContent>
  );

  return (
    <>
      <Stack>
        <StackItem>
          <Flex
            alignItems={{ default: 'alignItemsBaseline' }}
            justifyContent={{ default: 'justifyContentSpaceBetween' }}
          >
            <FlexItem>
              <Title headingLevel="h5">{t('Project memberships')}</Title>
            </FlexItem>
            {role === 'tenant-admin' && (
              <FlexItem>
                <Button
                  variant="link"
                  icon={<PlusCircleIcon />}
                  onClick={() => navigate(`/project-membership/create/${project.id}`)}
                >
                  {t('Create project membership')}
                </Button>
              </FlexItem>
            )}
          </Flex>
        </StackItem>
        <StackItem>{content}</StackItem>
      </Stack>
    </>
  );
};

export default ProjectMembership;
