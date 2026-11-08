import type { ReactNode } from 'react';

import CatalogItemActionsMenu, { type CatalogItemDeleteModalProps } from './CatalogItemActionsMenu';
import {
  BareMetalInstanceCatalogItemDeleteModal,
  ClusterCatalogItemDeleteModal,
  VMCatalogItemDeleteModal,
} from './catalogItemDeleteModals';
import {
  type CatalogItem,
  catalogItemDetailsPath,
  catalogItemEditPath,
  getCatalogCreateActionPath,
} from './catalogItemDisplay';
import { UserRole } from '../../shellTypes';
import { canManageCatalogItem } from '../CatalogItem/canManageCatalogItem';
import { useSetCatalogItemPublished } from '../CatalogItem/useSetCatalogItemPublished';

interface CatalogItemActionsMenuForItemProps {
  item: CatalogItem;
  role: UserRole;
  tenantId: string;
}

const CatalogItemActionsMenuForItem = ({
  item,
  role,
  tenantId,
}: CatalogItemActionsMenuForItemProps) => {
  const { setPublished } = useSetCatalogItemPublished(item);
  const canManage = canManageCatalogItem(role, tenantId, item);

  const renderDeleteModal = ({ onClose, onSuccess }: CatalogItemDeleteModalProps): ReactNode => {
    switch (item.$typeName) {
      case 'osac.public.v1.ComputeInstanceCatalogItem':
        return <VMCatalogItemDeleteModal item={item} onClose={onClose} onSuccess={onSuccess} />;
      case 'osac.public.v1.ClusterCatalogItem':
        return (
          <ClusterCatalogItemDeleteModal item={item} onClose={onClose} onSuccess={onSuccess} />
        );
      case 'osac.public.v1.BareMetalInstanceCatalogItem':
        return (
          <BareMetalInstanceCatalogItemDeleteModal
            item={item}
            onClose={onClose}
            onSuccess={onSuccess}
          />
        );
    }
  };

  return (
    <CatalogItemActionsMenu
      name={item.metadata?.name || ''}
      published={item.published}
      detailsPath={catalogItemDetailsPath(item)}
      launchPath={getCatalogCreateActionPath(item)}
      editPath={catalogItemEditPath(item)}
      role={role}
      canManage={canManage}
      onSetPublished={setPublished}
      renderDeleteModal={renderDeleteModal}
    />
  );
};

export default CatalogItemActionsMenuForItem;
