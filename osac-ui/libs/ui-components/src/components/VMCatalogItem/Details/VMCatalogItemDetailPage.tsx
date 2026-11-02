import { useParams } from 'react-router-dom';

import { ComputeInstanceCatalogItems } from '@osac/types';

import VMCatalogItemDetailContent from './VMCatalogItemDetailContent';
import { useGetResource } from '../../../api/use-resource';
import { useTranslation } from '../../../hooks/useTranslation';
import { CatalogItemIcon } from '../../../icons';
import CatalogItemDetails from '../../CatalogItem/Details/CatalogItemDetails';
import ResourceDetailsPage from '../../Resource/ResourceDetailsPage';

export const VMCatalogItemDetailPage = () => {
  const { t } = useTranslation();
  const { id = '' } = useParams() as { id: string };
  const { data, isLoading, isError, error, refetch } = useGetResource(ComputeInstanceCatalogItems, {
    id,
  });
  const item = data?.object;

  return (
    <ResourceDetailsPage
      error={isError ? error : undefined}
      found={!!item}
      isLoading={isLoading}
      parentTo="/catalog"
      parentLabel={t('Catalog')}
      refetch={() => void refetch()}
      resourceLabel={t('VM catalog item')}
      cardCount={4}
    >
      {item ? (
        <CatalogItemDetails
          item={item}
          showStatus={false}
          titlePrefix={<CatalogItemIcon kind="vm" isActive />}
        >
          <VMCatalogItemDetailContent item={item} />
        </CatalogItemDetails>
      ) : null}
    </ResourceDetailsPage>
  );
};

export default VMCatalogItemDetailPage;
