import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Button, Flex } from '@patternfly/react-core';
import DownloadIcon from '@patternfly/react-icons/dist/esm/icons/download-icon';
import KeyIcon from '@patternfly/react-icons/dist/esm/icons/key-icon';

import { type Cluster, ClusterState } from '@osac/types';
import { useDownloadKubeconfigHandler } from '@osac/ui-components/components/Cluster/use-download-kubeconfig-handler';
import DeleteResourceButton from '@osac/ui-components/components/Resource/DeleteResourceButton';

import ClusterPasswordModal from './ClusterPasswordModal';
import { useTranslation } from '../../../hooks/useTranslation';
import ClusterDeleteConfirmModal from '../ClusterDeleteConfirmModal';

interface ClusterDetailsActionButtonsProps {
  cluster: Cluster;
}

const ClusterDetailsActionButtons = ({ cluster }: ClusterDetailsActionButtonsProps) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [passwordOpen, setPasswordOpen] = useState(false);
  const isReady = cluster.status?.state === ClusterState.READY;
  const canDelete = cluster.status?.state !== ClusterState.DELETING;
  const clusterName = cluster.metadata?.name ?? cluster.id;
  const kubeconfigSecretId = cluster.status?.kubeconfigSecret?.id ?? '';
  const passwordSecretId = cluster.status?.passwordSecret?.id ?? '';

  const { onDownload, isPending, DownloadErrorModal } = useDownloadKubeconfigHandler(
    clusterName,
    kubeconfigSecretId,
  );

  return (
    <>
      {deleteOpen && (
        <ClusterDeleteConfirmModal
          cluster={cluster}
          onClose={() => setDeleteOpen(false)}
          onSuccess={() => navigate('/clusters', { replace: true })}
        />
      )}
      {passwordOpen && (
        <ClusterPasswordModal
          passwordSecretId={passwordSecretId}
          onClose={() => setPasswordOpen(false)}
        />
      )}
      {DownloadErrorModal}
      <Flex
        justifyContent={{ default: 'justifyContentFlexEnd' }}
        spaceItems={{ default: 'spaceItemsSm' }}
        flexWrap={{ default: 'wrap' }}
      >
        <Button
          variant="secondary"
          icon={<DownloadIcon />}
          isDisabled={!isReady || !kubeconfigSecretId || isPending}
          isLoading={isPending}
          onClick={() => void onDownload()}
        >
          {t('Download kubeconfig')}
        </Button>
        <Button
          variant="secondary"
          icon={<KeyIcon />}
          isDisabled={!isReady || !passwordSecretId}
          onClick={() => setPasswordOpen(true)}
        >
          {t('View password')}
        </Button>
        <DeleteResourceButton
          showIcon
          canDelete={canDelete}
          onClick={() => {
            if (canDelete) {
              setDeleteOpen(true);
            }
          }}
        />
      </Flex>
    </>
  );
};

export default ClusterDetailsActionButtons;
