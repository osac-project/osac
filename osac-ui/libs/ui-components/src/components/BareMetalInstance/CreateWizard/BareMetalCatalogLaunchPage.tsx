import { Link, useNavigate, useParams } from 'react-router-dom';
import {
  Breadcrumb,
  BreadcrumbItem,
  Content,
  PageSection,
  Stack,
  Title,
} from '@patternfly/react-core';

import { BareMetalInstanceCatalogItems } from '@osac/types';

import BareMetalInstanceCreateWizard from './BareMetalInstanceCreateWizard';
import { useGetResource } from '../../../api/use-resource';
import { useTranslation } from '../../../hooks/useTranslation';
import { wizardPreloadQueryOptions } from '../../ProvisioningWizard/wizardPreloadQueryOptions';
import ResourceDetailsPage from '../../Resource/ResourceDetailsPage';

const BareMetalCatalogLaunchPage = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { id = '' } = useParams<{ id: string }>();
  const query = useGetResource(BareMetalInstanceCatalogItems, { id }, wizardPreloadQueryOptions);
  const catalogItemName = query.data?.object?.metadata?.name || '';

  return (
    <ResourceDetailsPage
      error={query.error}
      found={!!query.data?.object}
      isLoading={query.isLoading}
      parentTo="/catalog/bare-metal"
      parentLabel={t('Catalog')}
      refetch={() => void query.refetch()}
      resourceLabel={t('bare metal catalog item')}
    >
      <>
        <PageSection hasBodyWrapper={false}>
          <Stack hasGutter>
            <Breadcrumb>
              <BreadcrumbItem>
                <Link to="/catalog/bare-metal">{t('Catalog')}</Link>
              </BreadcrumbItem>
              <BreadcrumbItem>
                <Link to={`/catalog/bare-metal/${id}`}>{catalogItemName}</Link>
              </BreadcrumbItem>
              <BreadcrumbItem isActive>{t('Launch instance')}</BreadcrumbItem>
            </Breadcrumb>
            <Title headingLevel="h1" size="3xl">
              {t('Create a bare metal instance')}
            </Title>
            <Content component="p">
              {t('Configure the bare metal instance and submit it for provisioning.')}
            </Content>
          </Stack>
        </PageSection>
        <BareMetalInstanceCreateWizard
          catalogItem={query.data?.object}
          onClosed={() => navigate(`/catalog/bare-metal/${id}`)}
        />
      </>
    </ResourceDetailsPage>
  );
};

export default BareMetalCatalogLaunchPage;
