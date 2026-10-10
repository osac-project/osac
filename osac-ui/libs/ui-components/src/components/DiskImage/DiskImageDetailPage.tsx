import { useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import {
  ActionList,
  ActionListGroup,
  ActionListItem,
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
import type { TFunction } from 'i18next';

import { type DiskImage, DiskImageLifecycle, SourceType } from '@osac/types';
import DeleteResourceButton from '@osac/ui-components/components/Resource/DeleteResourceButton';

import DiskImageDeleteConfirmModal from './DiskImageDeleteConfirmModal';
import DiskImageLifecycleLabel from './DiskImageLifecycleLabel';
import { architectureLabels, guestOsFamilyLabels } from './DiskImageTable';
import {
  type DiskImageLifecycleAction,
  getDiskImageLifecycleActions,
  useDiskImageLifecycleAction,
} from './useDiskImageLifecycleAction';
import { useDiskImage } from '../../api/v1/disk-image';
import { useTranslation } from '../../hooks/useTranslation';
import { displayValue } from '../../utils/detailFormatters';
import { Timestamp } from '../Primitives/Timestamp';
import { ResourceDetailHeader } from '../Resource/ResourceDetailHeader';
import { ResourceDetailsPageError } from '../Resource/ResourceDetailsPageError';
import { ResourceDetailsPageLoading } from '../Resource/ResourceDetailsPageLoading';

export const DISK_IMAGES_LIST_ROUTE = '/admin/infrastructure/disk-images';

interface DiskImageDetailActionsProps {
  diskImage: DiskImage;
  onDeleted: () => void;
}

const DiskImageDetailActions = ({ diskImage, onDeleted }: DiskImageDetailActionsProps) => {
  const { t } = useTranslation();
  const [deleteOpen, setDeleteOpen] = useState(false);
  const { runLifecycleAction } = useDiskImageLifecycleAction();

  const { canDeprecate, canObsolete, canReactivate, canDelete } = getDiskImageLifecycleActions(
    diskImage.spec?.lifecycle,
  );

  const runTransition = (action: DiskImageLifecycleAction) =>
    runLifecycleAction(diskImage.id, action);

  return (
    <>
      <ActionList>
        <ActionListGroup>
          {canDeprecate && (
            <ActionListItem>
              <Button
                variant="secondary"
                onClick={() => runTransition(DiskImageLifecycle.DEPRECATED)}
              >
                {t('Deprecate')}
              </Button>
            </ActionListItem>
          )}
          {canObsolete && (
            <ActionListItem>
              <Button
                variant="secondary"
                onClick={() => runTransition(DiskImageLifecycle.OBSOLETE)}
              >
                {t('Obsolete')}
              </Button>
            </ActionListItem>
          )}
          {canReactivate && (
            <ActionListItem>
              <Button
                variant="secondary"
                onClick={() => runTransition(DiskImageLifecycle.AVAILABLE)}
              >
                {t('Reactivate')}
              </Button>
            </ActionListItem>
          )}
          {canDelete && (
            <ActionListItem>
              <DeleteResourceButton onClick={() => setDeleteOpen(true)} />
            </ActionListItem>
          )}
        </ActionListGroup>
      </ActionList>

      {deleteOpen && (
        <DiskImageDeleteConfirmModal
          diskImage={diskImage}
          onClose={() => setDeleteOpen(false)}
          onSuccess={onDeleted}
        />
      )}
    </>
  );
};

const sourceTypeLabels = (t: TFunction): Record<SourceType, string> => ({
  [SourceType.UNSPECIFIED]: t('Unspecified'),
  [SourceType.REGISTRY]: t('Registry'),
});

const DiskImageDetailPage = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { id = '' } = useParams<{ id: string }>();
  const { data: diskImage, isLoading, isError, error, refetch } = useDiskImage(id);

  if (isLoading) {
    return (
      <ResourceDetailsPageLoading
        parentTo={DISK_IMAGES_LIST_ROUTE}
        parentLabel={t('Disk images')}
        cardCount={1}
      />
    );
  }

  if (isError) {
    return (
      <ResourceDetailsPageError
        parentTo={DISK_IMAGES_LIST_ROUTE}
        parentLabel={t('Disk images')}
        resourceLabel={t('disk image')}
        error={error}
        onRetry={() => void refetch()}
      />
    );
  }

  if (!diskImage) {
    return (
      <ResourceDetailsPageError
        parentTo={DISK_IMAGES_LIST_ROUTE}
        parentLabel={t('Disk images')}
        resourceLabel={t('disk image')}
        variant="not-found"
      />
    );
  }

  const architecture = diskImage.spec?.architecture ?? [];
  const architectureText = architectureLabels(t);
  const deprecation = diskImage.spec?.deprecation;
  const isGlobal = diskImage.metadata?.tenant === 'shared';

  return (
    <>
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
                  parentTo={DISK_IMAGES_LIST_ROUTE}
                  parentLabel={t('Disk images')}
                  resourceName={diskImage.metadata?.name || diskImage.id}
                />
              </FlexItem>
              <FlexItem>
                <DiskImageDetailActions
                  diskImage={diskImage}
                  onDeleted={() => navigate(DISK_IMAGES_LIST_ROUTE)}
                />
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
            <DescriptionList isCompact columnModifier={{ default: '2Col' }}>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Source type')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {sourceTypeLabels(t)[diskImage.spec?.sourceType ?? SourceType.UNSPECIFIED]}
                </DescriptionListDescription>
              </DescriptionListGroup>

              <DescriptionListGroup>
                <DescriptionListTerm>{t('Source reference')}</DescriptionListTerm>
                <DescriptionListDescription>
                  <code>{displayValue(diskImage.spec?.sourceRef)}</code>
                </DescriptionListDescription>
              </DescriptionListGroup>

              <DescriptionListGroup>
                <DescriptionListTerm>{t('Guest OS family')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {guestOsFamilyLabels(t)[diskImage.spec?.guestOsFamily ?? 0]}
                </DescriptionListDescription>
              </DescriptionListGroup>

              <DescriptionListGroup>
                <DescriptionListTerm>{t('Architecture')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {architecture.length
                    ? architecture
                        .map((value) => architectureText[value] ?? t('Unspecified'))
                        .join(', ')
                    : '—'}
                </DescriptionListDescription>
              </DescriptionListGroup>

              <DescriptionListGroup>
                <DescriptionListTerm>{t('Lifecycle')}</DescriptionListTerm>
                <DescriptionListDescription>
                  <DiskImageLifecycleLabel lifecycle={diskImage.spec?.lifecycle} />
                </DescriptionListDescription>
              </DescriptionListGroup>

              {deprecation?.deprecationTimestamp && (
                <DescriptionListGroup>
                  <DescriptionListTerm>{t('Deprecation timestamp')}</DescriptionListTerm>
                  <DescriptionListDescription>
                    <Timestamp value={deprecation.deprecationTimestamp} />
                  </DescriptionListDescription>
                </DescriptionListGroup>
              )}

              {deprecation?.obsolescenceTimestamp && (
                <DescriptionListGroup>
                  <DescriptionListTerm>{t('Obsolescence timestamp')}</DescriptionListTerm>
                  <DescriptionListDescription>
                    <Timestamp value={deprecation.obsolescenceTimestamp} />
                  </DescriptionListDescription>
                </DescriptionListGroup>
              )}

              <DescriptionListGroup>
                <DescriptionListTerm>{t('Scope')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {isGlobal ? t('Global') : t('Tenant')}
                </DescriptionListDescription>
              </DescriptionListGroup>

              <DescriptionListGroup>
                <DescriptionListTerm>{t('Created')}</DescriptionListTerm>
                <DescriptionListDescription>
                  <Timestamp value={diskImage.metadata?.creationTimestamp} />
                </DescriptionListDescription>
              </DescriptionListGroup>
            </DescriptionList>
          </CardBody>
        </Card>
      </PageSection>
    </>
  );
};

export default DiskImageDetailPage;
