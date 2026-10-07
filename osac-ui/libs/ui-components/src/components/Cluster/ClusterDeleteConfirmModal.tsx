import type { Cluster } from '@osac/types';
import DeleteResourceModal from '@osac/ui-components/components/Resource/DeleteResourceModal.tsx';

import { useDeleteCluster } from '../../api/v1/cluster';
import { useTranslation } from '../../hooks/useTranslation';

interface ClusterDeleteConfirmModalProps {
  cluster: Cluster;
  onClose: () => void;
  onSuccess: () => void;
}

const ClusterDeleteConfirmModal = ({
  cluster,
  onClose,
  onSuccess,
}: ClusterDeleteConfirmModalProps) => {
  const { t } = useTranslation();
  const deleteCluster = useDeleteCluster();
  const clusterName = cluster.metadata?.name ?? cluster.id;

  return (
    <DeleteResourceModal
      resourceName={clusterName}
      label={t(
        'This permanently deletes the cluster and all its resources. This action cannot be undone.',
      )}
      errorLabel={t('Failed to delete cluster')}
      onClose={onClose}
      onSuccess={onSuccess}
      mutation={deleteCluster}
      variables={cluster.id}
    />
  );
};

export default ClusterDeleteConfirmModal;
