import { Navigate, useParams } from 'react-router-dom';

interface CatalogLaunchRedirectProps {
  catalogPath: string;
}

const CatalogLaunchRedirect = ({ catalogPath }: CatalogLaunchRedirectProps) => {
  const { catalogItemId } = useParams<{ catalogItemId?: string }>();

  return catalogItemId ? (
    <Navigate to={`${catalogPath}/${catalogItemId}/launch`} replace />
  ) : (
    <Navigate to={catalogPath} replace />
  );
};

export default CatalogLaunchRedirect;
