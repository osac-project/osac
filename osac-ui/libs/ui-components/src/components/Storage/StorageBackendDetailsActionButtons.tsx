import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Button, Flex } from '@patternfly/react-core';
import PencilAltIcon from '@patternfly/react-icons/dist/esm/icons/pencil-alt-icon';

import type { StorageBackend } from '@osac/types/private';
import DeleteResourceButton from '@osac/ui-components/components/Resource/DeleteResourceButton';

import StorageBackendDeleteConfirmModal from './StorageBackendDeleteConfirmModal';
import { useTranslation } from '../../hooks/useTranslation';

const BACKENDS_LIST_PATH = '/admin/infrastructure/storage/backends';

interface StorageBackendDetailsActionButtonsProps {
  backend: StorageBackend;
}

const StorageBackendDetailsActionButtons = ({
  backend,
}: StorageBackendDetailsActionButtonsProps) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [deleteOpen, setDeleteOpen] = useState(false);

  return (
    <>
      {deleteOpen && (
        <StorageBackendDeleteConfirmModal
          backend={backend}
          onClose={() => setDeleteOpen(false)}
          onSuccess={() => navigate(BACKENDS_LIST_PATH)}
        />
      )}
      <Flex
        justifyContent={{ default: 'justifyContentFlexEnd' }}
        spaceItems={{ default: 'spaceItemsSm' }}
        flexWrap={{ default: 'wrap' }}
      >
        <Button
          variant="secondary"
          icon={<PencilAltIcon />}
          onClick={() => navigate(`${BACKENDS_LIST_PATH}/${backend.id}/edit`)}
        >
          {t('Edit')}
        </Button>
        <DeleteResourceButton showIcon onClick={() => setDeleteOpen(true)} />
      </Flex>
    </>
  );
};

export default StorageBackendDetailsActionButtons;
