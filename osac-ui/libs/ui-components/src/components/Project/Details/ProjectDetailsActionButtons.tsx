import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Flex } from '@patternfly/react-core';

import type { Project } from '@osac/types';
import DeleteResourceButton from '@osac/ui-components/components/Resource/DeleteResourceButton';

import ProjectDeleteModal from '../ProjectDeleteModal';

interface ProjectDetailsActionButtonsProps {
  project: Project;
}

const ProjectDetailsActionButtons = ({ project }: ProjectDetailsActionButtonsProps) => {
  const navigate = useNavigate();
  const [deleteOpen, setDeleteOpen] = useState(false);

  return (
    <>
      {deleteOpen && (
        <ProjectDeleteModal
          project={project}
          onClose={() => setDeleteOpen(false)}
          onSuccess={() => navigate('/projects', { replace: true })}
        />
      )}
      <Flex
        justifyContent={{ default: 'justifyContentFlexEnd' }}
        spaceItems={{ default: 'spaceItemsSm' }}
        flexWrap={{ default: 'wrap' }}
      >
        <DeleteResourceButton showIcon onClick={() => setDeleteOpen(true)} />
      </Flex>
    </>
  );
};

export default ProjectDetailsActionButtons;
