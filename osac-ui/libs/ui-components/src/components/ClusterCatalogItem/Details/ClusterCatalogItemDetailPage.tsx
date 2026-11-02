import { useParams } from 'react-router-dom';

import { ClusterCatalogItems } from '@osac/types';

import ClusterCatalogItemConfiguration from './ClusterCatalogItemConfiguration';
import { useGetResource } from '../../../api/use-resource';
import { useTranslation } from '../../../hooks/useTranslation';
import { CatalogItemIcon } from '../../../icons';
import { CatalogItemDetailContent } from '../../CatalogItem/Details/CatalogItemDetailContent';
import CatalogItemDetails from '../../CatalogItem/Details/CatalogItemDetails';
import ResourceDetailsPage from '../../Resource/ResourceDetailsPage';

export const ClusterCatalogItemDetailPage = () => {
  const { t } = useTranslation();
  const { id = '' } = useParams() as { id: string };
  const { data, isLoading, isError, error, refetch } = useGetResource(ClusterCatalogItems, { id });
  const item = data?.object;

  return (
    <ResourceDetailsPage
      error={isError ? error : undefined}
      found={!!item}
      isLoading={isLoading}
      parentTo="/catalog"
      parentLabel={t('Catalog')}
      refetch={() => void refetch()}
      resourceLabel={t('cluster catalog item')}
      cardCount={2}
    >
      {item ? (
        <CatalogItemDetails item={item} titlePrefix={<CatalogItemIcon kind="cluster" isActive />}>
          <CatalogItemDetailContent
            item={item}
            configuration={<ClusterCatalogItemConfiguration item={item} />}
          />
        </CatalogItemDetails>
      ) : null}
    </ResourceDetailsPage>
  );
};

export default ClusterCatalogItemDetailPage;
