import { Navigate, Route, Routes } from 'react-router-dom';

import StorageTierCreatePage from '@osac/ui-components/components/Storage/StorageTierCreatePage';
import { StorageBackendCreatePage } from '@osac/ui-components/pages/admin/StorageBackendCreatePage';
import { StorageBackendDetailsPage } from '@osac/ui-components/pages/admin/StorageBackendDetailsPage';
import { StorageBackendsListPage } from '@osac/ui-components/pages/admin/StorageBackendsListPage';
import { StorageTierDetailsPage } from '@osac/ui-components/pages/admin/StorageTierDetailsPage';
import { StorageTiersListPage } from '@osac/ui-components/pages/admin/StorageTiersListPage';

export const StorageRoutes = () => {
  return (
    <Routes>
      <Route index element={<Navigate to="backends" replace />} />
      <Route path="backends" element={<StorageBackendsListPage />} />
      <Route path="backends/create" element={<StorageBackendCreatePage />} />
      <Route path="backends/:id" element={<StorageBackendDetailsPage />} />
      <Route path="backends/:id/edit" element={<StorageBackendCreatePage />} />
      <Route path="tiers" element={<StorageTiersListPage />} />
      <Route path="tiers/create" element={<StorageTierCreatePage />} />
      <Route path="tiers/:id" element={<StorageTierDetailsPage />} />
      <Route path="tiers/:id/edit" element={<StorageTierCreatePage />} />
    </Routes>
  );
};
