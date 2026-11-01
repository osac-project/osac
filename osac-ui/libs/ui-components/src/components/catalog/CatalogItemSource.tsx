import { FC } from 'react';

import {
  CatalogItem,
  GLOBAL_TENANT_VALUE,
} from '@osac/ui-components/components/catalog/catalogItemDisplay';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

interface CatalogItemSourceProps {
  catalogItem: CatalogItem;
}

const CatalogItemSource: FC<CatalogItemSourceProps> = ({ catalogItem }) => {
  const { t } = useTranslation();

  return catalogItem.metadata?.tenant === GLOBAL_TENANT_VALUE
    ? t('From provider')
    : t('Added by you');
};

export default CatalogItemSource;
