import { Route, Routes } from 'react-router-dom';

import VmCreatePage from '@osac/ui-components/components/vm/CreateWizard/VmCreatePage';
import { VmDetailsPage } from '@osac/ui-components/components/vm/VmDetailsPage';
import CatalogLaunchRedirect from '@osac/ui-components/pages/tenant/CatalogLaunchRedirect';

import { VmListPage } from './VmListPage';

export const VmRoutes = () => (
  <Routes>
    <Route index element={<VmListPage />} />
    <Route path="create" element={<VmCreatePage />} />
    <Route
      path="create/:catalogItemId"
      element={<CatalogLaunchRedirect catalogPath="/catalog/vms" />}
    />
    <Route path=":id" element={<VmDetailsPage />} />
  </Routes>
);
