import { useCallback, useState } from 'react';
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

import BareMetalInstanceCreateWizard, {
  type BareMetalInstanceCreateWizardCloseHandler,
} from './BareMetalInstanceCreateWizard';
import { useTranslation } from '../../../hooks/useTranslation';

const BareMetalInstanceCreatePage = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { catalogItemId } = useParams<{ catalogItemId?: string }>();
  const [closeHandler, setCloseHandler] =
    useState<BareMetalInstanceCreateWizardCloseHandler | null>(null);

  const handleClosed = useCallback(() => navigate('/bare-metal'), [navigate]);

  return (
    <>
      <PageSection hasBodyWrapper={false}>
        <Stack hasGutter>
          <Breadcrumb>
            <BreadcrumbItem>
              <Button
                variant="link"
                isInline
                onClick={() => closeHandler?.requestClose()}
                isDisabled={closeHandler?.pending}
              >
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
        onClosed={handleClosed}
        onCloseHandlerChange={setCloseHandler}
      />
    </>
  );
};

export default BareMetalInstanceCreatePage;
