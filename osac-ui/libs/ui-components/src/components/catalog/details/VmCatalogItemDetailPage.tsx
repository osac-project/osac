import { useParams } from 'react-router-dom';

import CatalogItemDetails from './CatalogItemDetails';
import VmCatalogItemDetailContent from './ComputeCatalogItemDetailContent';
import { useComputeInstanceCatalogItem } from '../../../api/v1/compute-instance-catalog-item.ts';
import { useTranslation } from '../../../hooks/useTranslation.ts';
import { CatalogItemIcon } from '../../../icons';
import ResourceDetailsPage from '../../Resource/ResourceDetailsPage.tsx';
import { ResourceDetailsPageError } from '../../Resource/ResourceDetailsPageError.tsx';

export const VmCatalogItemDetailPage = () => {
  const { t } = useTranslation();
  const { id } = useParams() as { id?: string };
  const { data: item, isLoading, isError, error, refetch } = useComputeInstanceCatalogItem(id);

  if (!id) {
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
      cardCount={4}
    >
      {item ? (
        <CatalogItemDetails
          item={item}
          showStatus={false}
          titlePrefix={<CatalogItemIcon kind="vm" isActive />}
        >
          <VmCatalogItemDetailContent item={item} />
        </CatalogItemDetails>
      ) : null}
    </ResourceDetailsPage>
  );
};
