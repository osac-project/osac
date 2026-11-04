import { Route, Routes } from 'react-router-dom';

import VmCreatePage from '@osac/ui-components/components/vm/CreateWizard/VmCreatePage';
import { VmDetailsPage } from '@osac/ui-components/components/vm/VmDetailsPage';

import { VmListPage } from './VmListPage';

export const VmRoutes = () => (
  <Routes>
    <Route index element={<VmListPage />} />
    <Route path="create/:catalogItemId?" element={<VmCreatePage />} />
    <Route path=":id" element={<VmDetailsPage />} />
  </Routes>
);
