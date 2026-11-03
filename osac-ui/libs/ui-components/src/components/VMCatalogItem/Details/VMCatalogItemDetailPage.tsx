import { useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';

import { ComputeInstanceCatalogItems } from '@osac/types';

import VMCatalogItemDeleteModal from './VMCatalogItemDeleteModal';
import VMCatalogItemDetailContent from './VMCatalogItemDetailContent';
import { useGetResource } from '../../../api/use-resource';
import { useTranslation } from '../../../hooks/useTranslation';
import { CatalogItemIcon } from '../../../icons';
import CatalogItemDetails from '../../CatalogItem/Details/CatalogItemDetails';
import ResourceDetailsPage from '../../Resource/ResourceDetailsPage';

export const VMCatalogItemDetailPage = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { id = '' } = useParams() as { id: string };
  const [deleteOpen, setDeleteOpen] = useState(false);
  const { data, isLoading, error, refetch } = useGetResource(ComputeInstanceCatalogItems, {
    id,
  });
  const item = data?.object;

  return (
    <ResourceDetailsPage
      error={error}
      found={!!item}
      isLoading={isLoading}
      parentTo="/catalog"
      parentLabel={t('Catalog')}
      refetch={() => void refetch()}
      resourceLabel={t('VM catalog item')}
      cardCount={4}
    >
      {item ? (
        <>
          {deleteOpen ? (
            <VMCatalogItemDeleteModal
              item={item}
              onClose={() => setDeleteOpen(false)}
              onSuccess={() => navigate('/catalog')}
            />
          ) : null}
          <CatalogItemDetails
            item={item}
            onDelete={() => setDeleteOpen(true)}
            showStatus={false}
            titlePrefix={<CatalogItemIcon kind="vm" isActive />}
          >
            <VMCatalogItemDetailContent item={item} />
          </CatalogItemDetails>
        </>
      ) : null}
    </ResourceDetailsPage>
  );
};

export default VMCatalogItemDetailPage;
