import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  Button,
  Card,
  CardBody,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Divider,
  Flex,
  FlexItem,
  PageSection,
  Stack,
  StackItem,
} from '@patternfly/react-core';

import type { Volume } from '@osac/types';
import { VolumeState } from '@osac/types';

import { VolumeAccessModeLabel } from './VolumeAccessModeLabel';
import VolumeDeleteConfirmModal from './VolumeDeleteConfirmModal';
import { VolumeStatusLabel } from './VolumeStatusLabel';
import { useTranslation } from '../../hooks/useTranslation';
import { displayValue } from '../../utils/detailFormatters';
import { Timestamp } from '../Primitives/Timestamp';
import { ResourceDetailHeader } from '../Resource/ResourceDetailHeader';

interface VolumeDetailsProps {
  volume: Volume;
}

const VOLUMES_LIST_PATH = '/storage/volumes';

const formatSizeGib = (sizeGib: bigint | undefined): string => {
  if (sizeGib === undefined) {
    return '—';
  }
  return `${Number(sizeGib)} GiB`;
};

const VolumeDetails = ({ volume }: VolumeDetailsProps) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [deleteOpen, setDeleteOpen] = useState(false);

  const state = volume.status?.state;
  const canEdit =
    state === VolumeState.AVAILABLE ||
    state === VolumeState.CREATING ||
    state === VolumeState.FAILED;
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
              <FlexItem>
                <Flex spaceItems={{ default: 'spaceItemsSm' }} flexWrap={{ default: 'wrap' }}>
                  <FlexItem>
                    <Button
                      variant="secondary"
                      isDisabled={!canEdit}
                      onClick={() => navigate(`${VOLUMES_LIST_PATH}/${volume.id}/edit`)}
                    >
                      {t('Edit')}
                    </Button>
                  </FlexItem>
                  <FlexItem>
                    <Button
                      variant="danger"
                      isDisabled={!canDelete}
                      onClick={() => {
                        if (canDelete) {
                          setDeleteOpen(true);
                        }
                      }}
                    >
                      {t('Delete')}
                    </Button>
                  </FlexItem>
                </Flex>
              </FlexItem>
            </Flex>
          </StackItem>
          <StackItem>
            <Divider />
          </StackItem>
        </Stack>
      </PageSection>

      <PageSection hasBodyWrapper={false}>
        <Card>
          <CardBody>
            <DescriptionList isHorizontal>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Name')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {displayValue(volume.metadata?.name)}
                </DescriptionListDescription>
              </DescriptionListGroup>

              <DescriptionListGroup>
                <DescriptionListTerm>{t('Storage Tier')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {displayValue(volume.spec?.storageTier)}
                </DescriptionListDescription>
              </DescriptionListGroup>

              <DescriptionListGroup>
                <DescriptionListTerm>{t('Size')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {formatSizeGib(volume.spec?.sizeGib)}
                </DescriptionListDescription>
              </DescriptionListGroup>

              <DescriptionListGroup>
                <DescriptionListTerm>{t('Access Mode')}</DescriptionListTerm>
                <DescriptionListDescription>
                  <VolumeAccessModeLabel accessMode={volume.spec?.accessMode} />
                </DescriptionListDescription>
              </DescriptionListGroup>

              <DescriptionListGroup>
                <DescriptionListTerm>{t('Tenant')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {displayValue(volume.metadata?.tenant)}
                </DescriptionListDescription>
              </DescriptionListGroup>

              <DescriptionListGroup>
                <DescriptionListTerm>{t('Project')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {volume.metadata?.project || t('Default')}
                </DescriptionListDescription>
              </DescriptionListGroup>

              <DescriptionListGroup>
                <DescriptionListTerm>{t('Created')}</DescriptionListTerm>
                <DescriptionListDescription>
                  <Timestamp value={volume.metadata?.creationTimestamp} />
                </DescriptionListDescription>
              </DescriptionListGroup>

              {volume.status?.message ? (
                <DescriptionListGroup>
                  <DescriptionListTerm>{t('Message')}</DescriptionListTerm>
                  <DescriptionListDescription>{volume.status.message}</DescriptionListDescription>
                </DescriptionListGroup>
              ) : null}
            </DescriptionList>
          </CardBody>
        </Card>
      </PageSection>
    </>
  );
};

export default VolumeDetails;
