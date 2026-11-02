import { useParams } from 'react-router-dom';

import { BareMetalInstanceCatalogItems } from '@osac/types';

import BareMetalInstanceCatalogItemConfiguration from './BareMetalInstanceCatalogItemConfiguration';
import { useGetResource } from '../../../api/use-resource';
import { useTranslation } from '../../../hooks/useTranslation';
import { CatalogItemIcon } from '../../../icons';
import { CatalogItemDetailContent } from '../../CatalogItem/Details/CatalogItemDetailContent';
import CatalogItemDetails from '../../CatalogItem/Details/CatalogItemDetails';
import ResourceDetailsPage from '../../Resource/ResourceDetailsPage';

export const BareMetalInstanceCatalogItemDetailPage = () => {
  const { t } = useTranslation();
  const { id = '' } = useParams() as { id: string };
  const { data, isLoading, isError, error, refetch } = useGetResource(
    BareMetalInstanceCatalogItems,
    { id },
  );
  const item = data?.object;

  return (
    <ResourceDetailsPage
      error={isError ? error : undefined}
      found={!!item}
      isLoading={isLoading}
      parentTo="/catalog"
      parentLabel={t('Catalog')}
      refetch={() => void refetch()}
      resourceLabel={t('bare metal catalog item')}
      cardCount={2}
    >
      {item ? (
        <CatalogItemDetails item={item} titlePrefix={<CatalogItemIcon kind="bm" isActive />}>
          <CatalogItemDetailContent
            item={item}
            configuration={<BareMetalInstanceCatalogItemConfiguration item={item} />}
          />
        </CatalogItemDetails>
      ) : null}
    </ResourceDetailsPage>
  );
};

export default BareMetalInstanceCatalogItemDetailPage;
