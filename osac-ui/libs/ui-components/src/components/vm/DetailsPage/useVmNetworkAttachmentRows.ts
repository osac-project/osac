import { useMemo } from 'react';

import type { ComputeInstance } from '@osac/types';

import {
  formatResourceIdForReview,
  resourceDisplayName,
  useSecurityGroups,
  useSubnets,
  useVirtualNetworks,
} from '../../../api/v1/networking';

export interface VmNetworkResource {
  id: string;
  name: string;
}

export type VmNetworkAttachmentRow = {
  virtualNetwork?: VmNetworkResource;
  subnet: string;
  securityGroups: VmNetworkResource[];
  internalIp: string;
};

const findResourceReference = (
  id: string | undefined,
  resources: Array<{ id: string; metadata?: { name?: string } }>,
): VmNetworkResource | undefined => {
  if (!id) {
    return undefined;
  }

  const resource = resources.find((item) => item.id === id);
  return { id, name: resourceDisplayName(resource?.metadata, id) };
};

export const useVmNetworkAttachmentRows = (vm: ComputeInstance): VmNetworkAttachmentRow[] => {
  const { data: virtualNetworks = [] } = useVirtualNetworks();
  const { data: subnets = [] } = useSubnets();
  const { data: securityGroups = [] } = useSecurityGroups();

  return useMemo((): VmNetworkAttachmentRow[] => {
    const attachments = vm.spec?.networkAttachments ?? [];
    const attachmentStatuses = vm.status?.computeNetworkAttachmentStatuses ?? [];
    return attachments.map((attachment) => {
      const subnet = subnets.find((item) => item.id === attachment.subnet?.id);
      const virtualNetworkId = subnet?.spec?.virtualNetwork?.id ?? '';
      const attachmentStatus = attachmentStatuses.find(
        (status) => status.subnetRef === attachment.subnet?.id,
      );
      const internalIp =
        attachmentStatus?.ipAddress ||
        (attachments.length === 1 ? (vm.status?.internalIpAddress ?? '') : '');
      return {
        virtualNetwork: findResourceReference(virtualNetworkId, virtualNetworks),
        subnet: formatResourceIdForReview(attachment.subnet?.id ?? '', subnets),
        securityGroups: (attachment.securityGroups ?? [])
          .map(({ id }) => findResourceReference(id, securityGroups))
          .filter((resource): resource is VmNetworkResource => resource !== undefined),
        internalIp: internalIp || '-',
      };
    });
  }, [vm.spec?.networkAttachments, vm.status, subnets, virtualNetworks, securityGroups]);
};
