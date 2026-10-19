import { ExternalIPAttachmentEndpoint, ExternalIPAttachments, ExternalIPs } from '@osac/types';
import type { Cluster } from '@osac/types';

import { useCreateResource, useInvalidateServiceQueries } from '../../../api/use-resource';
import { useTranslation } from '../../../hooks/useTranslation';
import ExternalIpAttachModal from '../../ExternalIp/AttachExternalIpModal';

export interface AttachExternalIpModalProps {
  cluster: Cluster;
  endpoint: ExternalIPAttachmentEndpoint;
  onClose: () => void;
}

const AttachExternalIpModal = ({ cluster, endpoint, onClose }: AttachExternalIpModalProps) => {
  const { t } = useTranslation();
  const invalidateService = useInvalidateServiceQueries();
  const createAttachment = useCreateResource(ExternalIPAttachments, {
    onSuccess: async () => invalidateService(ExternalIPs),
  });
  const project = cluster.metadata?.project ?? '';
  const title =
    endpoint === ExternalIPAttachmentEndpoint.EXTERNAL_IP_ATTACHMENT_ENDPOINT_API
      ? t('Attach external IP to API endpoint')
      : t('Attach external IP to Ingress endpoint');

  return (
    <ExternalIpAttachModal
      title={title}
      emptyDescription={t('Create an external IP first, then attach it to this cluster endpoint.')}
      project={project}
      onAttach={(externalIpId) =>
        createAttachment.mutateAsync({
          object: {
            metadata: { name: `eipa-${crypto.randomUUID()}`, project },
            spec: {
              externalIp: { id: externalIpId },
              target: { case: 'cluster', value: { id: cluster.id } },
              targetEndpoint: endpoint,
            },
          },
        })
      }
      onClose={onClose}
    />
  );
};

export default AttachExternalIpModal;
