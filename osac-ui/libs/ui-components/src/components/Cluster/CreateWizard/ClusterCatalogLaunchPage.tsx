import { useNavigate, useParams } from 'react-router-dom';
import {
  Breadcrumb,
  BreadcrumbItem,
  Button,
  Content,
  PageSection,
  Stack,
  Title,
} from '@patternfly/react-core';

import { ClusterCatalogItems } from '@osac/types';

import ClusterCreateWizard from './ClusterCreateWizard';
import { useGetResource } from '../../../api/use-resource';
import { useTranslation } from '../../../hooks/useTranslation';
import ResourceDetailsPage from '../../Resource/ResourceDetailsPage';

const ClusterCatalogLaunchPage = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { id = '' } = useParams<{ id: string }>();
  const query = useGetResource(ClusterCatalogItems, { id });
  const catalogItemName = query.data?.object?.metadata?.name || id;

  return (
    <ResourceDetailsPage
      error={query.error}
      found={!!query.data?.object}
      isLoading={query.isLoading}
      parentTo="/catalog/clusters"
      parentLabel={t('Catalog')}
      refetch={() => void query.refetch()}
      resourceLabel={t('cluster catalog item')}
    >
      <>
        <PageSection hasBodyWrapper={false}>
          <Stack hasGutter>
            <Breadcrumb>
              <BreadcrumbItem>
                <Button variant="link" isInline onClick={() => navigate('/catalog/clusters')}>
                  {t('Catalog')}
                </Button>
              </BreadcrumbItem>
              <BreadcrumbItem>
                <Button variant="link" isInline onClick={() => navigate(`/catalog/clusters/${id}`)}>
                  {catalogItemName}
                </Button>
              </BreadcrumbItem>
              <BreadcrumbItem isActive>{t('Launch instance')}</BreadcrumbItem>
            </Breadcrumb>
            <Title headingLevel="h1" size="3xl">
              {t('Create a cluster')}
            </Title>
            <Content component="p">
              {t('Configure the cluster and submit it for provisioning.')}
            </Content>
          </Stack>
        </PageSection>
        <ClusterCreateWizard
          initialCatalogItemId={id}
          onClosed={() => navigate(`/catalog/clusters/${id}`)}
        />
      </>
    </ResourceDetailsPage>
  );
};

export default ClusterCatalogLaunchPage;
