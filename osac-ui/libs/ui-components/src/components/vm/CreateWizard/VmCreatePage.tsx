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

import VmCreateWizard, { type VmCreateWizardCloseHandler } from './VmCreateWizard';
import { useTranslation } from '../../../hooks/useTranslation';

const VmCreatePage = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { catalogItemId } = useParams<{ catalogItemId?: string }>();
  const [closeHandler, setCloseHandler] = useState<VmCreateWizardCloseHandler | null>(null);

  const handleClosed = useCallback(() => navigate('/vms'), [navigate]);

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
                {t('Virtual Machines')}
              </Button>
            </BreadcrumbItem>
            <BreadcrumbItem isActive>{t('Launch virtual machine')}</BreadcrumbItem>
          </Breadcrumb>
          <Title headingLevel="h1" size="3xl">
            {t('Create a virtual machine')}
          </Title>
          <Content component="p">
            {t('Configure the virtual machine and submit it for provisioning.')}
          </Content>
        </Stack>
      </PageSection>
      <VmCreateWizard
        initialCatalogItemId={catalogItemId}
        onClosed={handleClosed}
        onCloseHandlerChange={setCloseHandler}
      />
    </>
  );
};

export default VmCreatePage;
