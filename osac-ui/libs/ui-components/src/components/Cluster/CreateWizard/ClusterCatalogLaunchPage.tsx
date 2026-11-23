import { Link, useNavigate, useParams } from 'react-router-dom';
import {
  Breadcrumb,
  BreadcrumbItem,
  Content,
  PageSection,
  Stack,
  Title,
} from '@patternfly/react-core';

import { ClusterCatalogItems } from '@osac/types';

import ClusterCreateWizard from './ClusterCreateWizard';
import { useGetResource } from '../../../api/use-resource';
import { useTranslation } from '../../../hooks/useTranslation';
import { wizardPreloadQueryOptions } from '../../ProvisioningWizard/wizardPreloadQueryOptions';
import ResourceDetailsPage from '../../Resource/ResourceDetailsPage';

const ClusterCatalogLaunchPage = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { id = '' } = useParams<{ id: string }>();
  const query = useGetResource(ClusterCatalogItems, { id }, wizardPreloadQueryOptions);
  const catalogItemName = query.data?.object?.metadata?.name || '';

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
                <Link to="/catalog/clusters">{t('Catalog')}</Link>
              </BreadcrumbItem>
              <BreadcrumbItem>
                <Link to={`/catalog/clusters/${id}`}>{catalogItemName}</Link>
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
          catalogItem={query.data?.object}
          onClosed={() => navigate(`/catalog/clusters/${id}`)}
        />
      </>
    </ResourceDetailsPage>
  );
};

export default ClusterCatalogLaunchPage;
