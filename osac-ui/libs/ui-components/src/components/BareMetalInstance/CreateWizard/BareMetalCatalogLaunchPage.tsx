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

import { BareMetalInstanceCatalogItems } from '@osac/types';

import BareMetalInstanceCreateWizard from './BareMetalInstanceCreateWizard';
import { useGetResource } from '../../../api/use-resource';
import { useTranslation } from '../../../hooks/useTranslation';
import ResourceDetailsPage from '../../Resource/ResourceDetailsPage';

const BareMetalCatalogLaunchPage = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { id = '' } = useParams<{ id: string }>();
  const query = useGetResource(BareMetalInstanceCatalogItems, { id });
  const catalogItemName = query.data?.object?.metadata?.name || id;

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
                <Button variant="link" isInline onClick={() => navigate('/catalog/bare-metal')}>
                  {t('Catalog')}
                </Button>
              </BreadcrumbItem>
              <BreadcrumbItem>
                <Button
                  variant="link"
                  isInline
                  onClick={() => navigate(`/catalog/bare-metal/${id}`)}
                >
                  {catalogItemName}
                </Button>
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
          initialCatalogItemId={id}
          onClosed={() => navigate(`/catalog/bare-metal/${id}`)}
        />
      </>
    </ResourceDetailsPage>
  );
};

export default BareMetalCatalogLaunchPage;
