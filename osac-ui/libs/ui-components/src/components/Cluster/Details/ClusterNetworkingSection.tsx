import { useState } from 'react';
import { Grid, GridItem, Stack, StackItem, Title } from '@patternfly/react-core';

import { ExternalIPAttachmentEndpoint } from '@osac/types';
import type { Cluster, ExternalIPAttachment } from '@osac/types';

import AttachExternalIpModal from './AttachExternalIpModal';
import ClusterExternalIpCard from './ClusterExternalIpCard';
import ClusterNetworkingCard from './ClusterNetworkingCard';
import DetachExternalIpModal from './DetachExternalIpModal';
import { useTranslation } from '../../../hooks/useTranslation';

interface ClusterNetworkingSectionProps {
  cluster: Cluster;
}

const ClusterNetworkingSection = ({ cluster }: ClusterNetworkingSectionProps) => {
  const { t } = useTranslation();
  const [attachEndpoint, setAttachEndpoint] = useState<ExternalIPAttachmentEndpoint>();
  const [detachAttachment, setDetachAttachment] = useState<ExternalIPAttachment>();

  return (
    <>
      <Stack hasGutter>
        <StackItem>
          <Title headingLevel="h2" size="lg">
            {t('Networking')}
          </Title>
        </StackItem>
        <StackItem>
          <Grid hasGutter>
            <GridItem lg={6}>
              <ClusterNetworkingCard cluster={cluster} />
            </GridItem>
            <GridItem lg={6}>
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
            </GridItem>
          </Grid>
        </StackItem>
      </Stack>
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
