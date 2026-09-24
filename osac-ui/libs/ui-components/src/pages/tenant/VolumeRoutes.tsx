import { Route, Routes } from 'react-router-dom';

import { VolumeWizardPage } from '@osac/ui-components/components/Volume/VolumeWizardPage';

export const VolumeRoutes = () => (
  <Routes>
    <Route path="create" element={<VolumeWizardPage />} />
    <Route path=":id/edit" element={<VolumeWizardPage />} />
  </Routes>
);
