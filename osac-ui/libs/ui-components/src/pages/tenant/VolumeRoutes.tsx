import { Route, Routes } from 'react-router-dom';

import VolumeWizardPage from '@osac/ui-components/components/Volume/VolumeWizardPage';

import { VolumeDetailsPage } from './VolumeDetailsPage';
import { VolumesListPage } from './VolumesListPage';

export const VolumeRoutes = () => (
  <Routes>
    <Route index element={<VolumesListPage />} />
    <Route path="create" element={<VolumeWizardPage />} />
    <Route path=":id" element={<VolumeDetailsPage />} />
  </Routes>
);
