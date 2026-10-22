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

import BareMetalInstanceCreateWizard from './BareMetalInstanceCreateWizard';
import { useTranslation } from '../../../hooks/useTranslation';

const BareMetalInstanceCreatePage = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { catalogItemId } = useParams<{ catalogItemId?: string }>();
  const navigateToParent = () => navigate('/bare-metal');

  return (
    <>
      <PageSection hasBodyWrapper={false}>
        <Stack hasGutter>
          <Breadcrumb>
            <BreadcrumbItem>
              <Button variant="link" isInline onClick={navigateToParent}>
                {t('Bare Metal')}
              </Button>
            </BreadcrumbItem>
            <BreadcrumbItem isActive>{t('Launch bare metal instance')}</BreadcrumbItem>
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
        initialCatalogItemId={catalogItemId}
        onClosed={navigateToParent}
      />
    </>
  );
};

export default BareMetalInstanceCreatePage;
