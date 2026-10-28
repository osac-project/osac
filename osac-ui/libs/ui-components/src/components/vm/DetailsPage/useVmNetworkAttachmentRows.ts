import { useMemo } from 'react';

import type { ComputeInstance } from '@osac/types';

import {
  formatResourceIdForReview,
  formatResourceIdsForReview,
  useSecurityGroups,
  useSubnets,
  useVirtualNetworks,
} from '../../../api/v1/networking';

export type VmNetworkAttachmentRow = {
  virtualNetwork: string;
  subnet: string;
  securityGroups: string;
  internalIp: string;
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
        virtualNetwork: formatResourceIdForReview(virtualNetworkId, virtualNetworks),
        subnet: formatResourceIdForReview(attachment.subnet?.id ?? '', subnets),
        securityGroups: formatResourceIdsForReview(
          attachment.securityGroups?.map(({ id }) => id) ?? [],
          securityGroups,
        ),
        internalIp: internalIp || '—',
      };
    });
  }, [vm.spec?.networkAttachments, vm.status, subnets, virtualNetworks, securityGroups]);
};
