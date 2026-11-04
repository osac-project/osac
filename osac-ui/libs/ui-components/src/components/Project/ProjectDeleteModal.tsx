import { Project, Projects } from '@osac/types';
import { useDeleteResource } from '@osac/ui-components/api/use-resource';

import { getProjectName } from './utils';
import { useTranslation } from '../../hooks/useTranslation';
import DeleteResourceModal from '../Resource/DeleteResourceModal';

const ProjectDeleteModal = ({
  project,
  onClose,
  onSuccess,
}: {
  project: Project;
  onClose: VoidFunction;
  onSuccess: VoidFunction;
}) => {
  const { t } = useTranslation();
  const deleteProject = useDeleteResource(Projects);

  return (
    <DeleteResourceModal
      resourceName={getProjectName(project, t)}
      label={t('This permanently deletes the Project. This action cannot be undone.')}
      errorLabel={t('Failed to delete Project')}
      onClose={onClose}
      onSuccess={onSuccess}
      mutation={deleteProject}
      variables={{ id: project.id }}
    />
  );
};

export default ProjectDeleteModal;
