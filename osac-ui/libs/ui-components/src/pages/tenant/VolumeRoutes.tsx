import { Route, Routes } from 'react-router-dom';

import { VolumeWizardPage } from '@osac/ui-components/components/Volume/VolumeWizardPage';
import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import { VolumesListPage } from './VolumesListPage';

const VolumeDetailPlaceholder = () => {
  const { t } = useTranslation();
  return <div>{t('Volume detail — coming soon')}</div>;
};

export const VolumeRoutes = () => (
  <Routes>
    <Route index element={<VolumesListPage />} />
    <Route path="create" element={<VolumeWizardPage />} />
    <Route path=":id" element={<VolumeDetailPlaceholder />} />
    <Route path=":id/edit" element={<VolumeWizardPage />} />
  </Routes>
);
