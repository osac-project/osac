import { useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { Divider, Flex, FlexItem, PageSection, Stack, StackItem } from '@patternfly/react-core';

import { ExternalIPPools } from '@osac/types/private';
import DeleteResourceButton from '@osac/ui-components/components/Resource/DeleteResourceButton';

import ExternalIpPoolDeleteConfirmModal from './ExternalIpPoolDeleteConfirmModal';
import ExternalIpPoolDetailsPageContent from './ExternalIpPoolDetailsPageContent';
import { useGetResource } from '../../api/use-resource';
import { useTranslation } from '../../hooks/useTranslation';
import { ResourceDetailHeader } from '../Resource/ResourceDetailHeader';
import ResourceDetailsPage from '../Resource/ResourceDetailsPage';

const POOLS_LIST_PATH = '/admin/infrastructure/external-ip-pools';

export const ExternalIpPoolDetailsPage = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { id = '' } = useParams<{ id: string }>();
  const { data, isLoading, error, refetch } = useGetResource(ExternalIPPools, { id });
  const pool = data?.object;
  const [deleteOpen, setDeleteOpen] = useState(false);

  return (
    <ResourceDetailsPage
      error={error}
      found={!!pool}
      isLoading={isLoading}
      parentLabel={t('External IP pools')}
      parentTo={POOLS_LIST_PATH}
      refetch={refetch}
      resourceLabel={t('external IP pool')}
      cardCount={3}
    >
      {pool && (
        <>
          {deleteOpen && (
            <ExternalIpPoolDeleteConfirmModal
              pool={pool}
              onClose={() => setDeleteOpen(false)}
              onSuccess={() => navigate(POOLS_LIST_PATH)}
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
                      parentTo={POOLS_LIST_PATH}
                      parentLabel={t('External IP pools')}
                      resourceName={pool.metadata?.name || pool.id}
                      description={pool.metadata?.description}
                    />
                  </FlexItem>
                  <FlexItem>
                    <DeleteResourceButton onClick={() => setDeleteOpen(true)} />
                  </FlexItem>
                </Flex>
              </StackItem>
              <StackItem>
                <Divider />
              </StackItem>
            </Stack>
          </PageSection>
          <ExternalIpPoolDetailsPageContent pool={pool} />
        </>
      )}
    </ResourceDetailsPage>
  );
};
