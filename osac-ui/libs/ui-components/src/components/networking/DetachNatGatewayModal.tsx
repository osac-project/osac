import { ExternalIPs, type NATGateway, NATGateways } from '@osac/types';

import { useDeleteResource, useInvalidateServiceQueries } from '../../api/use-resource';
import { useTranslation } from '../../hooks/useTranslation';
import DeleteResourceModal from '../Resource/DeleteResourceModal';

export interface DetachNatGatewayModalProps {
  natGateway: NATGateway;
  onClose: () => void;
}

export const DetachNatGatewayModal = ({ natGateway, onClose }: DetachNatGatewayModalProps) => {
  const { t } = useTranslation();
  const invalidateService = useInvalidateServiceQueries();
  const deleteNatGateway = useDeleteResource(NATGateways, {
    onSuccess: async () => {
      await invalidateService(ExternalIPs);
    },
  });

  return (
    <DeleteResourceModal
      resourceName={natGateway.metadata?.name ?? natGateway.id}
      label={t(
        'Workloads in the virtual network lose the outbound access to destinations outside the virtual network that {{name}} provides.',
        { name: natGateway.metadata?.name ?? natGateway.id },
      )}
      errorLabel={t('Failed to delete NAT gateway')}
      onClose={onClose}
      onSuccess={onClose}
      mutation={deleteNatGateway}
      variables={{ id: natGateway.id }}
    />
  );
};
