import { Route, Routes } from 'react-router-dom';

import { ClusterDetailsPage } from '@osac/ui-components/components/Cluster/ClusterDetailsPage';
import { ClustersPage } from '@osac/ui-components/components/Cluster/ClustersPage';
import ClusterCreatePage from '@osac/ui-components/components/Cluster/CreateWizard/ClusterCreatePage';

import CatalogLaunchRedirect from './CatalogLaunchRedirect';

export const ClusterRoutes = () => {
  return (
    <Routes>
      <Route index element={<ClustersPage />} />
      <Route path="create" element={<ClusterCreatePage />} />
      <Route
        path="create/:catalogItemId"
        element={<CatalogLaunchRedirect catalogPath="/catalog/clusters" />}
      />
      <Route path=":clusterId" element={<ClusterDetailsPage />} />
    </Routes>
  );
};
