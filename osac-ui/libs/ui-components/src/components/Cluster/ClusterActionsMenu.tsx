import { useState } from 'react';
import { Link } from 'react-router-dom';
import { Dropdown, DropdownItem, DropdownList, MenuToggle } from '@patternfly/react-core';
import { EllipsisVIcon } from '@patternfly/react-icons/dist/esm/icons/ellipsis-v-icon';

import { type Cluster, ClusterState } from '@osac/types';
import ClusterPasswordModal from '@osac/ui-components/components/Cluster/Details/ClusterPasswordModal';
import { useDownloadKubeconfigHandler } from '@osac/ui-components/components/Cluster/use-download-kubeconfig-handler';
import DeleteResourceButton from '@osac/ui-components/components/Resource/DeleteResourceButton';

import ClusterDeleteConfirmModal from './ClusterDeleteConfirmModal';
import { useTranslation } from '../../hooks/useTranslation';

interface ClusterActionsMenuProps {
  cluster: Cluster;
}

const ClusterActionsMenu = ({ cluster }: ClusterActionsMenuProps) => {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [passwordOpen, setPasswordOpen] = useState(false);
  const kubeconfigSecretId = cluster.status?.kubeconfigSecret?.id ?? '';
  const passwordSecretId = cluster.status?.passwordSecret?.id ?? '';
  const isReady = cluster.status?.state === ClusterState.READY;
  const clusterName = cluster.metadata?.name ?? cluster.id;
  const canDelete = cluster.status?.state !== ClusterState.DELETING;

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
          onSuccess={() => setDeleteOpen(false)}
        />
      )}
      {passwordOpen && (
        <ClusterPasswordModal
          passwordSecretId={passwordSecretId}
          onClose={() => setPasswordOpen(false)}
        />
      )}
      {DownloadErrorModal}
      <Dropdown
        isOpen={open}
        onOpenChange={setOpen}
        toggle={(ref) => (
          <MenuToggle
            ref={ref}
            variant="plain"
            onClick={() => setOpen((o) => !o)}
            aria-label={t('Actions for {{name}}', { name: cluster.metadata?.name ?? cluster.id })}
          >
            <EllipsisVIcon />
          </MenuToggle>
        )}
        popperProps={{ position: 'right' }}
      >
        <DropdownList>
          <DropdownItem
            component={(props) => (
              <Link {...props} to={`/clusters/${encodeURIComponent(cluster.id)}`} />
            )}
            onClick={() => setOpen(false)}
          >
            {t('View details')}
          </DropdownItem>
          <DropdownItem
            isDisabled={!isReady || !kubeconfigSecretId || isPending}
            isLoading={isPending}
            onClick={() => {
              setOpen(false);
              void onDownload();
            }}
          >
            {t('Download kubeconfig')}
          </DropdownItem>
          <DropdownItem
            isDisabled={!isReady || !passwordSecretId}
            onClick={() => {
              setOpen(false);
              setPasswordOpen(true);
            }}
          >
            {t('View password')}
          </DropdownItem>
          <DeleteResourceButton
            isDropdown
            canDelete={canDelete}
            onClick={() => {
              if (!canDelete) {
                return;
              }
              setDeleteOpen(true);
              setOpen(false);
            }}
          />
        </DropdownList>
      </Dropdown>
    </>
  );
};

export default ClusterActionsMenu;
