import { Route, Routes } from 'react-router-dom';

import BareMetalCreatePage from '@osac/ui-components/components/BareMetalInstance/CreateWizard/BareMetalInstanceCreatePage';

import { BareMetalDetailsPage } from './BareMetalDetailsPage';
import { BareMetalListPage } from './BareMetalListPage';

export const BareMetalRoutes = () => (
  <Routes>
    <Route index element={<BareMetalListPage />} />
    <Route path="create" element={<BareMetalCreatePage />} />
    <Route path="create/:catalogItemId" element={<BareMetalCreatePage />} />
    <Route path=":id" element={<BareMetalDetailsPage />} />
  </Routes>
);
