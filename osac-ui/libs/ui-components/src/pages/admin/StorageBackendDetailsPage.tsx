import { useParams } from 'react-router-dom';
import {
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

import { StorageBackends } from '@osac/types/private';
import { useGetResource } from '@osac/ui-components/api/use-resource';

import { ResourceDetailHeader } from '../../components/Resource/ResourceDetailHeader';
import { ResourceDetailsPageError } from '../../components/Resource/ResourceDetailsPageError';
import { ResourceDetailsPageLoading } from '../../components/Resource/ResourceDetailsPageLoading';
import StorageBackendDetailsActionButtons from '../../components/Storage/StorageBackendDetailsActionButtons';
import StorageBackendStatusLabel from '../../components/Storage/StorageBackendStatusLabel';
import { useTranslation } from '../../hooks/useTranslation';
import { displayValue } from '../../utils/detailFormatters';
import { getStorageBackendProviderLabel } from '../../utils/storageBackendProvider';

const BACKENDS_LIST_PATH = '/admin/infrastructure/storage/backends';

export const StorageBackendDetailsPage = () => {
  const { t } = useTranslation();
  const { id = '' } = useParams<{ id: string }>();
  const { data, isLoading, isError, error, refetch } = useGetResource(
    StorageBackends,
    { id },
    { enabled: !!id },
  );

  const backend = data?.object;

  if (isLoading) {
    return (
      <ResourceDetailsPageLoading
        parentTo={BACKENDS_LIST_PATH}
        parentLabel={t('Storage Backends')}
        cardCount={1}
      />
    );
  }

  if (isError) {
    return (
      <ResourceDetailsPageError
        parentTo={BACKENDS_LIST_PATH}
        parentLabel={t('Storage Backends')}
        resourceLabel={t('storage backend')}
        error={error}
        onRetry={() => void refetch()}
      />
    );
  }

  if (!backend) {
    return (
      <ResourceDetailsPageError
        parentTo={BACKENDS_LIST_PATH}
        parentLabel={t('Storage Backends')}
        resourceLabel={t('storage backend')}
        variant="not-found"
      />
    );
  }

  return (
    <>
      <PageSection hasBodyWrapper={false}>
        <Stack hasGutter>
          <StackItem>
            <Flex justifyContent={{ default: 'justifyContentSpaceBetween' }}>
              <FlexItem>
                <ResourceDetailHeader
                  parents={[
                    {
                      to: BACKENDS_LIST_PATH,
                      label: t('Storage backends'),
                    },
                  ]}
                  resourceName={backend.metadata?.name || backend.id}
                  titleAddon={<StorageBackendStatusLabel state={backend.status?.state} />}
                />
              </FlexItem>
              <FlexItem>
                <StorageBackendDetailsActionButtons backend={backend} />
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
            <DescriptionList isCompact>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Provider')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {displayValue(getStorageBackendProviderLabel(backend.spec?.provider ?? '', t))}
                </DescriptionListDescription>
              </DescriptionListGroup>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Ownership mode')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {backend.spec?.cspManaged ? t('CSP-managed') : t('OSAC-managed')}
                </DescriptionListDescription>
              </DescriptionListGroup>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Endpoint')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {displayValue(backend.spec?.endpoint)}
                </DescriptionListDescription>
              </DescriptionListGroup>
              <DescriptionListGroup>
                <DescriptionListTerm>{t('Description')}</DescriptionListTerm>
                <DescriptionListDescription>
                  {displayValue(backend.spec?.description)}
                </DescriptionListDescription>
              </DescriptionListGroup>
              {backend.status?.message && (
                <DescriptionListGroup>
                  <DescriptionListTerm>{t('Status message')}</DescriptionListTerm>
                  <DescriptionListDescription>{backend.status.message}</DescriptionListDescription>
                </DescriptionListGroup>
              )}
            </DescriptionList>
          </CardBody>
        </Card>
      </PageSection>
    </>
  );
};
