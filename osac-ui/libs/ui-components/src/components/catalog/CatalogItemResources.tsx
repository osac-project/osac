import { FC } from 'react';

import { CatalogItem } from '@osac/ui-components/components/catalog/catalogItemDisplay';

import BareMetalCatalogItemResources from './BareMetalCatalogItemResources';
import type { CatalogItemResourceLookups } from './catalogItemResourceLookups';
import ClusterCatalogItemResources from './ClusterCatalogItemResources';
import ComputeCatalogItemResources from './ComputeCatalogItemResources';

interface CatalogItemResourcesProps {
  catalogItem: CatalogItem;
  resourceLookups: CatalogItemResourceLookups;
}

const CatalogItemResources: FC<CatalogItemResourcesProps> = ({ catalogItem, resourceLookups }) => {
  if (catalogItem.$typeName === 'osac.public.v1.ComputeInstanceCatalogItem') {
    return (
      <ComputeCatalogItemResources catalogItem={catalogItem} resourceLookups={resourceLookups} />
    );
  }

  if (catalogItem.$typeName === 'osac.public.v1.BareMetalInstanceCatalogItem') {
    return (
      <BareMetalCatalogItemResources catalogItem={catalogItem} resourceLookups={resourceLookups} />
    );
  }

  if (catalogItem.$typeName === 'osac.public.v1.ClusterCatalogItem') {
    return (
      <ClusterCatalogItemResources catalogItem={catalogItem} resourceLookups={resourceLookups} />
    );
  }

  return null;
};

export default CatalogItemResources;
