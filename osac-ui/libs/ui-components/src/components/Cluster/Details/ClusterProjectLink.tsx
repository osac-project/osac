import { type Cluster, type Project, Projects } from '@osac/types';

import ClusterResourceLink from './ClusterResourceLink';
import { cel } from '../../../api/cel';
import { useListResource } from '../../../api/use-resource';
import { usePermissions } from '../../../hooks/use-permissions';
import { useSession } from '../../../hooks/use-session';
import { useTranslation } from '../../../hooks/useTranslation';
import { displayValue } from '../../../utils/detailFormatters';
import { getProjectName } from '../../Project/utils';

interface ClusterProjectLinkProps {
  cluster: Cluster;
}

const ClusterProjectLink = ({ cluster }: ClusterProjectLinkProps) => {
  const { t } = useTranslation();
  const { tenantId } = useSession();
  const { allowed } = usePermissions(Projects, 'list');
  const projectPath = cluster.metadata?.project;
  const tenant = cluster.metadata?.tenant || tenantId;
  const { data, error, isPlaceholderData } = useListResource(
    Projects,
    {
      filter: cel<Project>((filter) =>
        filter.and(
          filter.field('metadata.tenant').equals(tenant),
          filter.field('metadata.name').equals(projectPath ?? ''),
        ),
      ),
      limit: 2,
    },
    { enabled: allowed && Boolean(tenant) && projectPath !== undefined },
  );
  const matches = data?.items.filter(
    (project) => project.metadata?.tenant === tenant && project.metadata?.name === projectPath,
  );
  const project =
    allowed && !error && !isPlaceholderData && matches?.length === 1 && (data?.total ?? 0) <= 1
      ? matches[0]
      : undefined;
  const name = project
    ? getProjectName(project, t)
    : projectPath === ''
      ? t('Root')
      : displayValue(projectPath);

  return (
    <ClusterResourceLink service={Projects} basePath="/projects" id={project?.id} name={name} />
  );
};

export default ClusterProjectLink;
