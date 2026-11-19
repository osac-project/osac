import { useState } from 'react';

import { ExternalIPAttachmentEndpoint } from '@osac/types';
import type { Cluster, ExternalIPAttachment } from '@osac/types';

import AttachExternalIpModal from './AttachExternalIpModal';
import ClusterExternalIpCard from './ClusterExternalIpCard';
import DetachExternalIpModal from './DetachExternalIpModal';

interface ClusterNetworkingSectionProps {
  cluster: Cluster;
}

const ClusterNetworkingSection = ({ cluster }: ClusterNetworkingSectionProps) => {
  const [attachEndpoint, setAttachEndpoint] = useState<ExternalIPAttachmentEndpoint>();
  const [detachAttachment, setDetachAttachment] = useState<ExternalIPAttachment>();

  return (
    <>
      <ClusterExternalIpCard
        cluster={cluster}
        onAttach={(endpoint) => {
          setDetachAttachment(undefined);
          setAttachEndpoint(endpoint);
        }}
        onDetach={(attachment) => {
          setAttachEndpoint(undefined);
          setDetachAttachment(attachment);
        }}
      />
      {attachEndpoint !== undefined && (
        <AttachExternalIpModal
          cluster={cluster}
          endpoint={attachEndpoint}
          onClose={() => setAttachEndpoint(undefined)}
        />
      )}
      {detachAttachment && (
        <DetachExternalIpModal
          attachment={detachAttachment}
          onClose={() => setDetachAttachment(undefined)}
        />
      )}
    </>
  );
};

export default ClusterNetworkingSection;
