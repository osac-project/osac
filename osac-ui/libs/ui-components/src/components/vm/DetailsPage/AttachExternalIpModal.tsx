import { ExternalIPAttachments, ExternalIPs } from '@osac/types';
import type { ComputeInstance } from '@osac/types';

import { useCreateResource, useInvalidateServiceQueries } from '../../../api/use-resource';
import { useTranslation } from '../../../hooks/useTranslation';
import ExternalIpAttachModal from '../../ExternalIp/AttachExternalIpModal';

interface AttachExternalIpModalProps {
  vm: ComputeInstance;
  onClose: () => void;
}

const AttachExternalIpModal = ({ vm, onClose }: AttachExternalIpModalProps) => {
  const { t } = useTranslation();
  const invalidateService = useInvalidateServiceQueries();
  const createAttachment = useCreateResource(ExternalIPAttachments, {
    onSuccess: async () => invalidateService(ExternalIPs),
  });
  const project = vm.metadata?.project ?? '';

  return (
    <ExternalIpAttachModal
      title={t('Attach external IP')}
      emptyDescription={t('Create an external IP first, then attach it to this virtual machine.')}
      project={project}
      onAttach={(externalIpId) =>
        createAttachment.mutateAsync({
          object: {
            metadata: {
              name: `eipa-${crypto.randomUUID()}`,
              project,
            },
            spec: {
              externalIp: { id: externalIpId },
              target: { case: 'computeInstance', value: { id: vm.id } },
            },
          },
        })
      }
      onClose={onClose}
    />
  );
};

export default AttachExternalIpModal;
