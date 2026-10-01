import { Divider, Grid, GridItem, PageSection, Stack, StackItem } from '@patternfly/react-core';

import type { Volume } from '@osac/types';

import VolumeDetailsCard from './VolumeDetailsCard';
import { VolumeStatusLabel } from './VolumeStatusLabel';
import { useTranslation } from '../../hooks/useTranslation';
import { ResourceDetailHeader } from '../Resource/ResourceDetailHeader';

interface VolumeDetailsProps {
  volume: Volume;
}

const VolumeDetails = ({ volume }: VolumeDetailsProps) => {
  const { t } = useTranslation();

  return (
    <>
      <PageSection hasBodyWrapper={false}>
        <Stack hasGutter>
          <StackItem>
            <ResourceDetailHeader
              parentTo="/storage/volumes"
              parentLabel={t('Volumes')}
              resourceName={volume.metadata?.name ?? volume.id}
              titleAddon={<VolumeStatusLabel state={volume.status?.state} />}
            />
          </StackItem>
          <StackItem>
            <Divider />
          </StackItem>
        </Stack>
      </PageSection>

      <PageSection hasBodyWrapper={false}>
        <Grid hasGutter>
          <GridItem md={6}>
            <VolumeDetailsCard volume={volume} />
          </GridItem>
        </Grid>
      </PageSection>
    </>
  );
};

export default VolumeDetails;
