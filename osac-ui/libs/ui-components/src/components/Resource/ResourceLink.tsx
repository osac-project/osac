import { Link } from 'react-router-dom';

export interface ResourceLinkProps {
  basePath: string;
  id: string;
  name: string;
}

const ResourceLink = ({ basePath, id, name }: ResourceLinkProps) => (
  <Link to={`${basePath.replace(/\/$/, '')}/${encodeURIComponent(id)}`}>{name}</Link>
);

export default ResourceLink;
