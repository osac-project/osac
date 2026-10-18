import { useParams } from 'react-router-dom';
import { Bullseye, EmptyState, EmptyStateBody, PageSection } from '@patternfly/react-core';

import { isCatalogItemKind } from './catalogItemDisplay';
import { useTranslation } from '../../hooks/useTranslation';
import { ResourceDetailsPageError } from '../Resource/ResourceDetailsPageError';

export const CatalogItemCreatePage = () => {
  const { t } = useTranslation();
  const { kind: kindParam } = useParams() as { kind?: string };

  if (!isCatalogItemKind(kindParam)) {
    return (
      <ResourceDetailsPageError
        parentTo="/catalog"
        parentLabel={t('Catalog')}
        resourceLabel={t('catalog item')}
        variant="not-found"
      />
    );
  }

  return (
    <PageSection hasBodyWrapper={false}>
      <Bullseye>
        <EmptyState titleText={t('Catalog item authoring is coming soon')} headingLevel="h2">
          <EmptyStateBody>
            {t('The create wizard for this catalog item type is not available yet.')}
          </EmptyStateBody>
        </EmptyState>
      </Bullseye>
    </PageSection>
  );
};
