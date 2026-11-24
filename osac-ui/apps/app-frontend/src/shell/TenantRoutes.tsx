import { Route, Routes } from 'react-router-dom';

import TenantCreatePage from '@osac/ui-components/components/Tenant/TenantCreatePage/TenantCreatePage';
import TenantEditPage from '@osac/ui-components/components/Tenant/TenantEditPage';
import TenantListPage from '@osac/ui-components/components/Tenant/TenantListPage';

export const TenantRoutes = () => (
  <Routes>
    <Route index element={<TenantListPage />} />
    <Route path="create" element={<TenantCreatePage />} />
    <Route path=":id/edit" element={<TenantEditPage />} />
  </Routes>
);
