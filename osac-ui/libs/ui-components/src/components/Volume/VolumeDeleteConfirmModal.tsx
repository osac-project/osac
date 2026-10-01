import { useNavigate } from 'react-router-dom';

import type { Volume } from '@osac/types';
import { Volumes } from '@osac/types';

import { useDeleteResource } from '../../api/use-resource';
import { useTranslation } from '../../hooks/useTranslation';
import DeleteResourceModal from '../Resource/DeleteResourceModal';

const VOLUMES_LIST_PATH = '/storage/volumes';

interface VolumeDeleteConfirmModalProps {
  volume: Volume;
  onClose: () => void;
  onSuccess: () => void;
}

const VolumeDeleteConfirmModal = ({
  volume,
  onClose,
  onSuccess,
}: VolumeDeleteConfirmModalProps) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const deleteVolume = useDeleteResource(Volumes);
  const volumeName = volume.metadata?.name ?? volume.id;

  const handleSuccess = () => {
    navigate(VOLUMES_LIST_PATH);
    onSuccess();
  };

  return (
    <DeleteResourceModal
      resourceName={volumeName}
      label={t(
        'This permanently deletes the volume and all of its data. This action cannot be undone.',
      )}
      errorLabel={t('Failed to delete volume')}
      onClose={onClose}
      onSuccess={handleSuccess}
      mutation={deleteVolume}
      variables={{ id: volume.id }}
    />
  );
};

export default VolumeDeleteConfirmModal;
