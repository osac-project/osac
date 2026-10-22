import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Alert, AlertActionLink, Button, Flex, Modal } from '@patternfly/react-core';
import DownloadIcon from '@patternfly/react-icons/dist/esm/icons/download-icon';
import KeyIcon from '@patternfly/react-icons/dist/esm/icons/key-icon';

import { type Cluster, ClusterState } from '@osac/types';
import DeleteResourceButton from '@osac/ui-components/components/Resource/DeleteResourceButton';
import { getErrorMessage } from '@osac/ui-components/utils/error';

import ClusterPasswordModal from './ClusterPasswordModal';
import { useDownloadKubeconfig } from '../../../api/v1/cluster';
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
  const { download, isPending, error, setError } = useDownloadKubeconfig();

  const isReady = cluster.status?.state === ClusterState.READY;
  const canDelete = cluster.status?.state !== ClusterState.DELETING;
  const clusterName = cluster.metadata?.name ?? cluster.id;
  const kubeconfigSecretId = cluster.status?.kubeconfigSecret?.id ?? '';
  const passwordSecretId = cluster.status?.passwordSecret?.id ?? '';

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
      {error && (
        <Modal
          variant="small"
          isOpen
          onClose={() => setError(undefined)}
          title={t('Failed to download kubeconfig')}
        >
          <Alert
            variant="danger"
            title={t('Failed to download kubeconfig')}
            isInline
            actionLinks={
              <AlertActionLink onClick={() => download(kubeconfigSecretId, clusterName)}>
                {t('Retry')}
              </AlertActionLink>
            }
          >
            {getErrorMessage(error)}
          </Alert>
        </Modal>
      )}
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
          onClick={() => void download(kubeconfigSecretId, clusterName)}
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
