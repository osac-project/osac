import { Navigate, Route, Routes } from 'react-router-dom';

import ListPage from '@osac/ui-components/components/Page/ListPage';
import StorageTierCreatePage from '@osac/ui-components/components/Storage/StorageTierCreatePage';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';
import { StorageBackendCreatePage } from '@osac/ui-components/pages/admin/StorageBackendCreatePage';
import { StorageBackendDetailsPage } from '@osac/ui-components/pages/admin/StorageBackendDetailsPage';
import { StorageBackendsListPage } from '@osac/ui-components/pages/admin/StorageBackendsListPage';
import { StorageTierDetailsPage } from '@osac/ui-components/pages/admin/StorageTierDetailsPage';
import { StorageTiersListPage } from '@osac/ui-components/pages/admin/StorageTiersListPage';

const StorageBackendsPage = () => {
  const { t } = useTranslation();
  return (
    <ListPage
      title={t('Storage Backends')}
      description={t('Manage storage backends for this cloud platform.')}
    >
      <StorageBackendsListPage />
    </ListPage>
  );
};

const StorageTiersPage = () => {
  const { t } = useTranslation();
  return (
    <ListPage
      title={t('Storage Tiers')}
      description={t('Manage storage tiers for this cloud platform.')}
    >
      <StorageTiersListPage />
    </ListPage>
  );
};

export const StorageRoutes = () => {
  return (
    <Routes>
      <Route index element={<Navigate to="backends" replace />} />
      <Route path="backends" element={<StorageBackendsPage />} />
      <Route path="backends/create" element={<StorageBackendCreatePage />} />
      <Route path="backends/:id" element={<StorageBackendDetailsPage />} />
      <Route path="backends/:id/edit" element={<StorageBackendCreatePage />} />
      <Route path="tiers" element={<StorageTiersPage />} />
      <Route path="tiers/create" element={<StorageTierCreatePage />} />
      <Route path="tiers/:id" element={<StorageTierDetailsPage />} />
      <Route path="tiers/:id/edit" element={<StorageTierCreatePage />} />
    </Routes>
  );
};
