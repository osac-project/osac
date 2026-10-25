import { useParams } from 'react-router-dom';

import { useCatalogItemByKind } from './useCatalogItemByKind';
import { useTranslation } from '../../../hooks/useTranslation';
import ResourceDetailsPage from '../../Resource/ResourceDetailsPage';
import { ResourceDetailsPageError } from '../../Resource/ResourceDetailsPageError';
import BareMetalCatalogItemWizard from '../authoring/baremetal/BareMetalCatalogItemWizard';
import ClusterCatalogItemWizard from '../authoring/cluster/ClusterCatalogItemWizard';
import ComputeCatalogItemWizard from '../authoring/compute/ComputeCatalogItemWizard';
import { isCatalogItemKind } from '../catalogItemDisplay';

const CatalogItemEditWizard = ({
  item,
}: {
  item: NonNullable<ReturnType<typeof useCatalogItemByKind>['data']>;
}) => {
  switch (item.$typeName) {
    case 'osac.public.v1.ComputeInstanceCatalogItem':
      return <ComputeCatalogItemWizard item={item} />;
    case 'osac.public.v1.ClusterCatalogItem':
      return <ClusterCatalogItemWizard item={item} />;
    case 'osac.public.v1.BareMetalInstanceCatalogItem':
      return <BareMetalCatalogItemWizard item={item} />;
    default:
      return null;
  }
};

export const CatalogItemEditPage = () => {
  const { t } = useTranslation();
  const { kind: kindParam, id = '' } = useParams() as { kind?: string; id?: string };
  const kind = isCatalogItemKind(kindParam) ? kindParam : undefined;
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
      cardCount={1}
    >
      {item ? <CatalogItemEditWizard item={item} /> : null}
    </ResourceDetailsPage>
  );
};

export default CatalogItemEditPage;
