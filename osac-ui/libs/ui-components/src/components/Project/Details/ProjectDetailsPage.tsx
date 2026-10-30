import { useParams } from 'react-router-dom';
import {
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Divider,
  Flex,
  FlexItem,
  Grid,
  GridItem,
  PageSection,
  Stack,
  StackItem,
  Title,
} from '@patternfly/react-core';

import { type Project, Projects } from '@osac/types';
import { cel } from '@osac/ui-components/api/cel';
import { useGetResource, useListResource } from '@osac/ui-components/api/use-resource';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import ProjectDetailsActionButtons from './ProjectDetailsActionButtons';
import { Timestamp } from '../../Primitives/Timestamp';
import ProjectMembership from '../../ProjectMembership/ProjectMembershipTable';
import { ResourceDetailHeader } from '../../Resource/ResourceDetailHeader';
import ResourceDetailsPage from '../../Resource/ResourceDetailsPage';
import ProjectStatusLabel from '../ProjectStatusLabel';
import { getProjectName, getProjectParentPaths } from '../utils';

interface ProjectDetailsPageContentProps {
  project: Project;
}

const ProjectDetailsPageContent = ({ project }: ProjectDetailsPageContentProps) => {
  const { t } = useTranslation();
  const parentPaths = getProjectParentPaths(project);

  const { data, isLoading } = useListResource(
    Projects,
    {
      filter: cel<Project>((filter) =>
        filter.and(
          filter.field('metadata.tenant').notEquals('shared'),
          filter.field('metadata.name').isIn(parentPaths),
        ),
      ),
    },
    { enabled: parentPaths.length > 0 },
  );

  const projectsByPath = new Map(
    (data?.items ?? []).map((parentProject) => [parentProject.metadata?.name ?? '', parentProject]),
  );
  const parents = [
    { to: '/projects', label: t('Projects') },
    ...(isLoading
      ? parentPaths.map(() => ({
          to: '',
          label: '',
          isLoading: true,
        }))
      : parentPaths
          .map((path) => projectsByPath.get(path))
          .filter((parentProject): parentProject is Project => !!parentProject)
          .map((parentProject) => ({
            to: `/projects/${parentProject.id}`,
            label: getProjectName(parentProject, t),
          }))),
  ];

  return (
    <>
      <PageSection hasBodyWrapper={false}>
        <Stack hasGutter>
          <StackItem>
            <Flex
              justifyContent={{ default: 'justifyContentSpaceBetween' }}
              alignItems={{ default: 'alignItemsFlexStart' }}
              flexWrap={{ default: 'wrap' }}
              spaceItems={{ default: 'spaceItemsMd' }}
            >
              <FlexItem>
                <ResourceDetailHeader
                  parents={parents}
                  resourceName={getProjectName(project, t)}
                  titleAddon={<ProjectStatusLabel project={project} />}
                />
              </FlexItem>
              <FlexItem>
                <ProjectDetailsActionButtons project={project} />
              </FlexItem>
            </Flex>
          </StackItem>
          <StackItem>
            <Divider />
          </StackItem>
        </Stack>
      </PageSection>

      <PageSection hasBodyWrapper={false}>
        <Grid hasGutter>
          <GridItem md={6}>
            <Stack hasGutter>
              <StackItem>
                <Title headingLevel="h5">{t('Overview')}</Title>
              </StackItem>
              <StackItem>
                <DescriptionList>
                  <DescriptionListGroup>
                    <DescriptionListTerm>{t('Description')}</DescriptionListTerm>
                    <DescriptionListDescription>
                      {project.metadata?.description || '-'}
                    </DescriptionListDescription>
                  </DescriptionListGroup>
                  <DescriptionListGroup>
                    <DescriptionListTerm>{t('Created')}</DescriptionListTerm>
                    <DescriptionListDescription>
                      <Timestamp value={project.metadata?.creationTimestamp} />
                    </DescriptionListDescription>
                  </DescriptionListGroup>
                </DescriptionList>
              </StackItem>
            </Stack>
          </GridItem>
          <GridItem md={6}>
            <ProjectMembership project={project} />
          </GridItem>
        </Grid>
      </PageSection>
    </>
  );
};

const ProjectDetailsPage = () => {
  const { t } = useTranslation();
  const { id } = useParams() as { id: string };

  const { data, isLoading, error, refetch } = useGetResource(Projects, { id });

  return (
    <ResourceDetailsPage
      error={error}
      found={!!data?.object}
      isLoading={isLoading}
      parentLabel={t('Projects')}
      parentTo="/projects"
      refetch={refetch}
    >
      {data?.object && <ProjectDetailsPageContent project={data.object} />}
    </ResourceDetailsPage>
  );
};

export default ProjectDetailsPage;
