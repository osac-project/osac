import { useCallback, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { type MessageInitShape } from '@bufbuild/protobuf';
import {
  Breadcrumb,
  BreadcrumbItem,
  Button,
  Content,
  PageSection,
  Stack,
  Title,
} from '@patternfly/react-core';

import { ClusterSchema } from '@osac/types';
import { useProvisionCluster } from '@osac/ui-components/api/v1/cluster';
import {
  type CatalogProvisionPayload,
  CatalogProvisionWizard,
  type CatalogProvisionWizardCloseHandler,
} from '@osac/ui-components/components/catalogProvision/CatalogProvisionWizard';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

export const ClusterCreatePage = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { catalogItemId } = useParams<{ catalogItemId?: string }>();
  const provisionCluster = useProvisionCluster();
  const [closeHandler, setCloseHandler] = useState<CatalogProvisionWizardCloseHandler | null>(null);

  const handleCloseHandlerChange = useCallback((handler: CatalogProvisionWizardCloseHandler) => {
    setCloseHandler(handler);
  }, []);

  const handleWizardClosed = useCallback(() => {
    navigate('/clusters');
  }, [navigate]);

  const handleWizardProvision = useCallback(
    async (payload: CatalogProvisionPayload) => {
      const created = await provisionCluster.mutateAsync(
        payload as MessageInitShape<typeof ClusterSchema>,
      );
      if (!created?.id) {
        throw new Error('Create response missing id');
      }
      navigate(`/clusters/${created.id}`);
    },
    [navigate, provisionCluster],
  );

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
                {t('Clusters')}
              </Button>
            </BreadcrumbItem>
            <BreadcrumbItem isActive>{t('Launch instance for cluster')}</BreadcrumbItem>
          </Breadcrumb>
          <Title headingLevel="h1" size="3xl">
            {t('Launch instance for cluster')}
          </Title>
          <Content component="p">
            {t('Select a catalog item, configure, and provision an OpenShift cluster.')}
          </Content>
        </Stack>
      </PageSection>
      <CatalogProvisionWizard
        kind="cluster"
        initialCatalogItemId={catalogItemId}
        onProvision={handleWizardProvision}
        onClosed={handleWizardClosed}
        onCloseHandlerChange={handleCloseHandlerChange}
      />
    </>
  );
};
