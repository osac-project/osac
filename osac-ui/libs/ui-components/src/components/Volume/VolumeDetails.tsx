import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  Button,
  Divider,
  Flex,
  FlexItem,
  Grid,
  GridItem,
  PageSection,
  Stack,
  StackItem,
} from '@patternfly/react-core';

import type { Volume } from '@osac/types';
import { VolumeState } from '@osac/types';

import VolumeDeleteConfirmModal from './VolumeDeleteConfirmModal';
import VolumeDetailsCard from './VolumeDetailsCard';
import { VolumeStatusLabel } from './VolumeStatusLabel';
import { useTranslation } from '../../hooks/useTranslation';
import { ResourceDetailHeader } from '../Resource/ResourceDetailHeader';

interface VolumeDetailsProps {
  volume: Volume;
}

const VOLUMES_LIST_PATH = '/storage/volumes';

const VolumeDetails = ({ volume }: VolumeDetailsProps) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [deleteOpen, setDeleteOpen] = useState(false);

  const state = volume.status?.state;
  const canDelete = state === VolumeState.AVAILABLE || state === VolumeState.FAILED;

  return (
    <>
      {deleteOpen && (
        <VolumeDeleteConfirmModal
          volume={volume}
          onClose={() => setDeleteOpen(false)}
          onSuccess={() => navigate(VOLUMES_LIST_PATH)}
        />
      )}

      <PageSection hasBodyWrapper={false}>
        <Stack hasGutter>
          <StackItem>
            <Flex
              justifyContent={{ default: 'justifyContentSpaceBetween' }}
              alignItems={{ default: 'alignItemsFlexStart' }}
              flexWrap={{ default: 'wrap' }}
              spaceItems={{ default: 'spaceItemsMd' }}
            >
              <FlexItem>
                <ResourceDetailHeader
                  parentTo={VOLUMES_LIST_PATH}
                  parentLabel={t('Volumes')}
                  resourceName={volume.metadata?.name?.trim() || volume.id}
                  titleAddon={<VolumeStatusLabel state={volume.status?.state} />}
                />
              </FlexItem>
              {canDelete ? (
                <FlexItem>
                  <Button variant="danger" onClick={() => setDeleteOpen(true)}>
                    {t('Delete')}
                  </Button>
                </FlexItem>
              ) : null}
            </Flex>
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
