import type { VirtualNetwork } from '@osac/types';

import { useDeleteVirtualNetwork } from '../../api/v1/networking';
import { useTranslation } from '../../hooks/useTranslation';
import DeleteResourceModal from '../Resource/DeleteResourceModal';

export interface VirtualNetworkDeleteModalProps {
  virtualNetwork: VirtualNetwork;
  onClose: () => void;
  onSuccess: () => void;
}

/**
 * Confirms the delete of a virtual network without known dependents. The details page
 * disables Delete while it knows of subnets, security groups or a NAT gateway; the
 * server's guard decides and its message stays in the modal.
 */
const VirtualNetworkDeleteModal = ({
  virtualNetwork,
  onClose,
  onSuccess,
}: VirtualNetworkDeleteModalProps) => {
  const { t } = useTranslation();
  const deleteVirtualNetwork = useDeleteVirtualNetwork();

  return (
    <DeleteResourceModal
      resourceName={virtualNetwork.metadata?.name || virtualNetwork.id}
      label={t(
        'This permanently deletes the virtual network. It cannot be deleted while it has subnets, security groups, or a NAT gateway. This action cannot be undone.',
      )}
      errorLabel={t('Failed to delete virtual network')}
      onClose={onClose}
      onSuccess={onSuccess}
      mutation={deleteVirtualNetwork}
      variables={virtualNetwork.id}
    />
  );
};

export default VirtualNetworkDeleteModal;
