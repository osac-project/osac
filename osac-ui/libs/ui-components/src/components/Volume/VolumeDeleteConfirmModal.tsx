import type { Volume } from '@osac/types';
import { Volumes } from '@osac/types';

import { useDeleteResource } from '../../api/use-resource';
import { useTranslation } from '../../hooks/useTranslation';
import DeleteResourceModal from '../Resource/DeleteResourceModal';

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
  const deleteVolume = useDeleteResource(Volumes);
  const volumeName = volume.metadata?.name ?? volume.id;

  return (
    <DeleteResourceModal
      resourceName={volumeName}
      label={t('This permanently deletes the volume. This action cannot be undone.')}
      errorLabel={t('Failed to delete volume')}
      onClose={onClose}
      onSuccess={onSuccess}
      mutation={deleteVolume}
      variables={{ id: volume.id }}
    />
  );
};

export default VolumeDeleteConfirmModal;
