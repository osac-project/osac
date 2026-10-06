import { Route, Routes } from 'react-router-dom';

import { ExternalIpPoolDetailsPage } from '@osac/ui-components/components/ExternalIpPool/ExternalIpPoolDetailsPage';
import { ExternalIpPoolsListPage } from '@osac/ui-components/components/ExternalIpPool/ExternalIpPoolsListPage';
import { ExternalIpPoolWizardPage } from '@osac/ui-components/components/ExternalIpPool/ExternalIpPoolWizardPage';

export const ExternalIpPoolRoutes = () => (
  <Routes>
    <Route index element={<ExternalIpPoolsListPage />} />
    <Route path="create" element={<ExternalIpPoolWizardPage />} />
    <Route path=":id" element={<ExternalIpPoolDetailsPage />} />
  </Routes>
);
