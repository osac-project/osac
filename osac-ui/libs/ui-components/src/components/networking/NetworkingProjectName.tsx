import { Link } from 'react-router-dom';

import { useNetworkingProjects } from './useNetworkingProjects';
import { useTranslation } from '../../hooks/useTranslation';
import { getProjectName } from '../Project/utils';

interface NetworkingProjectNameProps {
  /** The resource's `metadata.project`: a full project path, empty for the root project. */
  project?: string;
  /** The resource's tenant; tenants reuse project names, so the admin's lookup needs it. */
  tenant?: string;
}

/**
 * A resource's project, named as everywhere else in the console ("Root" for the root
 * project) and linked to the project's details page when the user can see that project.
 */
const NetworkingProjectName = ({ project = '', tenant }: NetworkingProjectNameProps) => {
  const { t } = useTranslation();
  const { findProject } = useNetworkingProjects();
  const match = findProject(project, tenant);

  if (!match) {
    return <>{project || t('Root')}</>;
  }
  return <Link to={`/projects/${encodeURIComponent(match.id)}`}>{getProjectName(match, t)}</Link>;
};

export default NetworkingProjectName;
