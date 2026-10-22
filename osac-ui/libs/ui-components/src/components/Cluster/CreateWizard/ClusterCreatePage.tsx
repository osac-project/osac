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

import ClusterCreateWizard from './ClusterCreateWizard';
import { useTranslation } from '../../../hooks/useTranslation';

const ClusterCreatePage = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { catalogItemId } = useParams<{ catalogItemId?: string }>();
  const navigateToParent = () => navigate('/clusters');

  return (
    <>
      <PageSection hasBodyWrapper={false}>
        <Stack hasGutter>
          <Breadcrumb>
            <BreadcrumbItem>
              <Button variant="link" isInline onClick={navigateToParent}>
                {t('Clusters')}
              </Button>
            </BreadcrumbItem>
            <BreadcrumbItem isActive>{t('Launch cluster')}</BreadcrumbItem>
          </Breadcrumb>
          <Title headingLevel="h1" size="3xl">
            {t('Create a cluster')}
          </Title>
          <Content component="p">
            {t('Configure the cluster and submit it for provisioning.')}
          </Content>
        </Stack>
      </PageSection>
      <ClusterCreateWizard initialCatalogItemId={catalogItemId} onClosed={navigateToParent} />
    </>
  );
};

export default ClusterCreatePage;
