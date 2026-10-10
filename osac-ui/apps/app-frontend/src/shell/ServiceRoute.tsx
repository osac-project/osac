import { type ReactNode } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  Button,
  EmptyState,
  EmptyStateActions,
  EmptyStateBody,
  EmptyStateFooter,
  PageSection,
} from '@patternfly/react-core';
import { TFunction } from 'i18next';

import { ServiceTier } from '@osac/types';
import { useSession } from '@osac/ui-components/hooks/use-session';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

const serviceLabels = (t: TFunction) => ({
  [ServiceTier.BMAAS]: t('The Bare Metal service is not enabled on this server.'),
  [ServiceTier.CAAS]: t('The Cluster service is not enabled on this server.'),
  [ServiceTier.VMAAS]: t('The Virtual Machine service is not enabled on this server.'),
  [ServiceTier.MAAS]: t('The Model service is not enabled on this server.'),
  [ServiceTier.UNSPECIFIED]: t('The service is not enabled on this server.'),
});

interface ServiceRouteProps {
  children: ReactNode;
  service: ServiceTier;
}

export const ServiceRoute = ({ children, service }: ServiceRouteProps) => {
  const { enabledServices } = useSession();
  const { t } = useTranslation();
  const navigate = useNavigate();

  if (enabledServices.includes(service)) {
    return children;
  }

  return (
    <PageSection hasBodyWrapper={false}>
      <EmptyState titleText={t('Service unavailable')} headingLevel="h1" status="warning">
        <EmptyStateBody>{serviceLabels(t)[service]}</EmptyStateBody>
        <EmptyStateFooter>
          <EmptyStateActions>
            <Button variant="primary" onClick={() => navigate('/catalog')}>
              {t('Go to catalog')}
            </Button>
          </EmptyStateActions>
        </EmptyStateFooter>
      </EmptyState>
    </PageSection>
  );
};
