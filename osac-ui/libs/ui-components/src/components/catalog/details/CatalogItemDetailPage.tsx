import { useParams } from 'react-router-dom';

import CatalogItemDetails from './CatalogItemDetails.tsx';
import { useCatalogItemByKind } from './useCatalogItemByKind';
import { useTranslation } from '../../../hooks/useTranslation.ts';
import ResourceDetailsPage from '../../Resource/ResourceDetailsPage.tsx';
import { ResourceDetailsPageError } from '../../Resource/ResourceDetailsPageError.tsx';
import { isCatalogItemKind } from '../catalogItemDisplay.ts';

export const CatalogItemDetailPage = () => {
  const { t } = useTranslation();
  const { kind: kindParam, id = '' } = useParams() as { kind?: string; id?: string };
  const kind = isCatalogItemKind(kindParam) && kindParam !== 'vm' ? kindParam : undefined;
  const { data: item, isLoading, isError, error, refetch } = useCatalogItemByKind(kind, id);

  if (!kind || !id) {
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
    <ResourceDetailsPage
      error={isError ? error : undefined}
      found={!!item}
      isLoading={isLoading}
      parentTo="/catalog"
      parentLabel={t('Catalog')}
      refetch={() => void refetch()}
      resourceLabel={t('catalog item')}
      cardCount={2}
    >
      {item && <CatalogItemDetails item={item} />}
    </ResourceDetailsPage>
  );
};
