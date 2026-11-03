import { Route, Routes } from 'react-router-dom';

import VmCreatePage from '@osac/ui-components/components/vm/CreateWizard/VmCreatePage';
import VmConsolePage from '@osac/ui-components/components/vm/VmConsolePage';
import { VmDetailsPage } from '@osac/ui-components/components/vm/VmDetailsPage';

import { VmListPage } from './VmListPage';

export const VmRoutes = () => (
  <Routes>
    <Route index element={<VmListPage />} />
    <Route path="create/:catalogItemId?" element={<VmCreatePage />} />
    <Route path=":id/console" element={<VmConsolePage />} />
    <Route path=":id" element={<VmDetailsPage />} />
  </Routes>
);
