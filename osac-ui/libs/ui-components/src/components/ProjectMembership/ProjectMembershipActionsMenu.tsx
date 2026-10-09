import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Dropdown, DropdownItem, DropdownList, MenuToggle } from '@patternfly/react-core';
import { EllipsisVIcon } from '@patternfly/react-icons/dist/esm/icons/ellipsis-v-icon';

import type { ProjectMembership } from '@osac/types';
import { useDeleteProjectMembership } from '@osac/ui-components/api/v1/project-membership';
import DeleteResourceButton from '@osac/ui-components/components/Resource/DeleteResourceButton';

import { useTranslation } from '../../hooks/useTranslation';
import DeleteResourceModal from '../Resource/DeleteResourceModal';

interface ProjectMembershipActionsMenuProps {
  projectId: string;
  projectMembership: ProjectMembership;
}

const ProjectMembershipActionsMenu = ({
  projectMembership,
  projectId,
}: ProjectMembershipActionsMenuProps) => {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const navigate = useNavigate();

  const deleteProjectMembership = useDeleteProjectMembership();

  return (
    <>
      {deleteOpen && (
        <DeleteResourceModal
          resourceName={projectMembership.metadata?.name || ''}
          label={t(
            'This permanently deletes the Project membership. This action cannot be undone.',
          )}
          errorLabel={t('Failed to delete Project membership')}
          onClose={() => setDeleteOpen(false)}
          onSuccess={() => setDeleteOpen(false)}
          mutation={deleteProjectMembership}
          variables={projectMembership.id}
        />
      )}
      <Dropdown
        isOpen={open}
        onOpenChange={setOpen}
        toggle={(ref) => (
          <MenuToggle
            ref={ref}
            variant="plain"
            onClick={() => setOpen((o) => !o)}
            aria-label={t('Actions')}
          >
            <EllipsisVIcon />
          </MenuToggle>
        )}
        popperProps={{ position: 'right' }}
      >
        <DropdownList>
          <DropdownItem
            onClick={() =>
              navigate(`/project-membership/edit/${projectId}/${projectMembership.id}`)
            }
          >
            {t('Edit')}
          </DropdownItem>
          <DeleteResourceButton
            isDropdown
            onClick={() => {
              setDeleteOpen(true);
              setOpen(false);
            }}
          />
        </DropdownList>
      </Dropdown>
    </>
  );
};

export default ProjectMembershipActionsMenu;
