import { Route, Routes } from 'react-router-dom';

import { VolumeWizardPage } from '@osac/ui-components/components/Volume/VolumeWizardPage';

import { VolumesListPage } from './VolumesListPage';

const VolumeDetailPlaceholder = () => <div>Volume detail — coming soon</div>;

export const VolumeRoutes = () => (
  <Routes>
    <Route index element={<VolumesListPage />} />
    <Route path="create" element={<VolumeWizardPage />} />
    <Route path=":id" element={<VolumeDetailPlaceholder />} />
    <Route path=":id/edit" element={<VolumeWizardPage />} />
  </Routes>
);
