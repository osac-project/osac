import { Alert, Stack, StackItem } from '@patternfly/react-core';

import { type Cluster, Clusters } from '@osac/types';
import DeleteResourceModal from '@osac/ui-components/components/Resource/DeleteResourceModal.tsx';

import { useDeleteResource } from '../../api/use-resource';
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
  const deleteCluster = useDeleteResource(Clusters);
  const clusterName = cluster.metadata?.name ?? cluster.id;
  const hasAutoExternalIp = cluster.spec?.autoExternalIpAttachment === true;

  return (
    <DeleteResourceModal
      resourceName={clusterName}
      label={
        <Stack hasGutter>
          <StackItem>
            {t(
              'This permanently deletes the cluster and all its resources. This action cannot be undone.',
            )}
          </StackItem>
          {hasAutoExternalIp && (
            <StackItem>
              <Alert variant="info" isInline isPlain title={t('Resource cleanup')}>
                {t(
                  'Auto-provisioned External IPs and External IP Attachments will be permanently deleted.',
                )}
              </Alert>
            </StackItem>
          )}
        </Stack>
      }
      errorLabel={t('Failed to delete cluster')}
      onClose={onClose}
      onSuccess={onSuccess}
      mutation={deleteCluster}
      variables={{ id: cluster.id }}
    />
  );
};

export default ClusterDeleteConfirmModal;
