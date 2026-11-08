import { useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';

import { BareMetalInstanceCatalogItems } from '@osac/types';

import BareMetalInstanceCatalogItemConfiguration from './BareMetalInstanceCatalogItemConfiguration';
import BareMetalInstanceCatalogItemDeleteModal from './BareMetalInstanceCatalogItemDeleteModal';
import { useGetResource } from '../../../api/use-resource';
import { useTranslation } from '../../../hooks/useTranslation';
import { CatalogItemIcon } from '../../../icons';
import { CatalogItemDetailContent } from '../../CatalogItem/Details/CatalogItemDetailContent';
import CatalogItemDetails from '../../CatalogItem/Details/CatalogItemDetails';
import ResourceDetailsPage from '../../Resource/ResourceDetailsPage';

export const BareMetalInstanceCatalogItemDetailPage = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { id = '' } = useParams() as { id: string };
  const [deleteOpen, setDeleteOpen] = useState(false);
  const { data, isLoading, error, refetch } = useGetResource(BareMetalInstanceCatalogItems, { id });
  const item = data?.object;

  return (
    <ResourceDetailsPage
      error={error}
      found={!!item}
      isLoading={isLoading}
      parentTo="/catalog"
      parentLabel={t('Catalog')}
      refetch={() => void refetch()}
      resourceLabel={t('bare metal catalog item')}
      cardCount={2}
    >
      {item ? (
        <>
          {deleteOpen ? (
            <BareMetalInstanceCatalogItemDeleteModal
              item={item}
              onClose={() => setDeleteOpen(false)}
              onSuccess={() => navigate('/catalog')}
            />
          ) : null}
          <CatalogItemDetails
            item={item}
            onDelete={() => setDeleteOpen(true)}
            titlePrefix={<CatalogItemIcon kind="bm" isActive />}
          >
            <CatalogItemDetailContent
              item={item}
              configuration={<BareMetalInstanceCatalogItemConfiguration item={item} />}
            />
          </CatalogItemDetails>
        </>
      ) : null}
    </ResourceDetailsPage>
  );
};

export default BareMetalInstanceCatalogItemDetailPage;
