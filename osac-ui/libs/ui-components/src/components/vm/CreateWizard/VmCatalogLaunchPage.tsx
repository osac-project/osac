import { Link, useNavigate, useParams } from 'react-router-dom';
import {
  Breadcrumb,
  BreadcrumbItem,
  Content,
  PageSection,
  Stack,
  Title,
} from '@patternfly/react-core';

import { ComputeInstanceCatalogItems } from '@osac/types';

import VmCreateWizard from './VmCreateWizard';
import { useGetResource } from '../../../api/use-resource';
import { useTranslation } from '../../../hooks/useTranslation';
import ResourceDetailsPage from '../../Resource/ResourceDetailsPage';

const VmCatalogLaunchPage = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { id = '' } = useParams<{ id: string }>();
  const query = useGetResource(ComputeInstanceCatalogItems, { id });
  const catalogItemName = query.data?.object?.metadata?.name || id;

  return (
    <ResourceDetailsPage
      error={query.error && !query.data ? query.error : undefined}
      found={!!query.data?.object}
      isLoading={query.isLoading}
      parentTo="/catalog/vms"
      parentLabel={t('Catalog')}
      refetch={() => void query.refetch()}
      resourceLabel={t('VM catalog item')}
    >
      <>
        <PageSection hasBodyWrapper={false}>
          <Stack hasGutter>
            <Breadcrumb>
              <BreadcrumbItem>
                <Link to="/catalog/vms">{t('Catalog')}</Link>
              </BreadcrumbItem>
              <BreadcrumbItem>
                <Link to={`/catalog/vms/${id}`}>{catalogItemName}</Link>
              </BreadcrumbItem>
              <BreadcrumbItem isActive>{t('Launch instance')}</BreadcrumbItem>
            </Breadcrumb>
            <Title headingLevel="h1" size="3xl">
              {t('Create a virtual machine')}
            </Title>
            <Content component="p">
              {t('Configure the virtual machine and submit it for provisioning.')}
            </Content>
          </Stack>
        </PageSection>
        <VmCreateWizard initialCatalogItemId={id} onClosed={() => navigate(`/catalog/vms/${id}`)} />
      </>
    </ResourceDetailsPage>
  );
};

export default VmCatalogLaunchPage;
