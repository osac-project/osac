import { type ServiceWithMethod, usePermissions } from '../../../hooks/use-permissions';
import { displayValue } from '../../../utils/detailFormatters';
import ResourceLink from '../../Resource/ResourceLink';

interface ClusterResourceLinkProps {
  service: ServiceWithMethod<'get'>;
  basePath:
    | '/projects'
    | '/catalog/cluster'
    | '/networking/virtual-networks'
    | '/networking/security-groups';
  id?: string;
  name?: string;
}

const ClusterResourceLink = ({ service, basePath, id, name }: ClusterResourceLinkProps) => {
  const { allowed, error } = usePermissions(service, 'get');
  const label = displayValue(name || id);

  return id?.trim() && allowed && !error ? (
    <ResourceLink basePath={basePath} id={id} name={label} />
  ) : (
    <>{label}</>
  );
};

export default ClusterResourceLink;
