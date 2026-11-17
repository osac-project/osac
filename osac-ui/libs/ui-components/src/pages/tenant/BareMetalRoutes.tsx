import { Route, Routes } from 'react-router-dom';

import BareMetalCreatePage from '@osac/ui-components/components/BareMetalInstance/CreateWizard/BareMetalInstanceCreatePage';

import { BareMetalDetailsPage } from './BareMetalDetailsPage';
import { BareMetalListPage } from './BareMetalListPage';
import CatalogLaunchRedirect from './CatalogLaunchRedirect';

export const BareMetalRoutes = () => (
  <Routes>
    <Route index element={<BareMetalListPage />} />
    <Route path="create" element={<BareMetalCreatePage />} />
    <Route
      path="create/:catalogItemId"
      element={<CatalogLaunchRedirect catalogPath="/catalog/bare-metal" />}
    />
    <Route path=":id" element={<BareMetalDetailsPage />} />
  </Routes>
);
