import { Route, Routes } from 'react-router-dom';

import ExternalIpWizardPage from './CreatePage/ExternalIpWizardPage';
import ExternalIpListPage from './ExternalIpListPage';

const ExternalIpRoutes = () => (
  <Routes>
    <Route index element={<ExternalIpListPage />} />
    <Route path="create" element={<ExternalIpWizardPage />} />
  </Routes>
);

export default ExternalIpRoutes;
