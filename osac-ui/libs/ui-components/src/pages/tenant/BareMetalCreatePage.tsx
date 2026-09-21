import { useCallback, useRef, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import type { MessageInitShape } from '@bufbuild/protobuf';
import {
  Breadcrumb,
  BreadcrumbItem,
  Button,
  Content,
  PageSection,
  Stack,
  Title,
} from '@patternfly/react-core';

import { BareMetalInstanceSchema } from '@osac/types';
import { useCreateBareMetalInstance } from '@osac/ui-components/api/v1/baremetal-instance';
import {
  CatalogProvisionPayload,
  CatalogProvisionWizard,
  type CatalogProvisionWizardCloseHandler,
} from '@osac/ui-components/components/catalogProvision/CatalogProvisionWizard';
import { hasBareMetalAuthentication } from '@osac/ui-components/components/catalogProvision/wizard/adapters/bareMetalInstance/fields';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

export const BareMetalCreatePage = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { catalogItemId } = useParams<{ catalogItemId?: string }>();
  const createBareMetalInstance = useCreateBareMetalInstance();
  const [closeHandler, setCloseHandler] = useState<CatalogProvisionWizardCloseHandler | null>(null);
  const provisionInFlightRef = useRef<Promise<void> | null>(null);

  const handleCloseHandlerChange = useCallback((handler: CatalogProvisionWizardCloseHandler) => {
    setCloseHandler(handler);
  }, []);

  const handleWizardClosed = useCallback(() => {
    navigate('/bare-metal');
  }, [navigate]);

  const handleWizardProvision = useCallback(
    async (payload: CatalogProvisionPayload) => {
      if (provisionInFlightRef.current) {
        return provisionInFlightRef.current;
      }

      const provisionPromise = (async () => {
        const bareMetalPayload = payload as MessageInitShape<typeof BareMetalInstanceSchema>;
        if (
          !hasBareMetalAuthentication(
            bareMetalPayload.spec?.sshPublicKey,
            bareMetalPayload.spec?.userData,
          )
        ) {
          throw new Error(
            t('Provide either an SSH public key or user data containing access credentials.'),
          );
        }
        const instance = await createBareMetalInstance.mutateAsync(bareMetalPayload);
        if (!instance) {
          throw new Error('Create response missing instance');
        }
        navigate(`/bare-metal/${instance.id}`);
      })();
      provisionInFlightRef.current = provisionPromise;
      try {
        await provisionPromise;
      } finally {
        if (provisionInFlightRef.current === provisionPromise) {
          provisionInFlightRef.current = null;
        }
      }
    },
    [createBareMetalInstance, navigate, t],
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
                {t('Bare Metal')}
              </Button>
            </BreadcrumbItem>
            <BreadcrumbItem isActive>{t('Provision bare metal')}</BreadcrumbItem>
          </Breadcrumb>
          <Title headingLevel="h1" size="3xl">
            {t('Provision bare metal')}
          </Title>
          <Content component="p">
            {t('Provision a bare metal instance from a catalog item.')}
          </Content>
        </Stack>
      </PageSection>
      <CatalogProvisionWizard
        kind="bare_metal_instance"
        initialCatalogItemId={catalogItemId}
        onProvision={handleWizardProvision}
        onClosed={handleWizardClosed}
        onCloseHandlerChange={handleCloseHandlerChange}
      />
    </>
  );
};
