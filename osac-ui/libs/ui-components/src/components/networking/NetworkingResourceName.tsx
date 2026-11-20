import { Link } from 'react-router-dom';

interface NetworkingResourceNameProps {
  name: string;
  href: string;
  /** A value shown after the name in parentheses, such as an address or kind. */
  detail?: string;
}

/** One link style for networking resources: the name, then its detail inline. */
const NetworkingResourceName = ({ name, href, detail }: NetworkingResourceNameProps) => (
  <span className="pf-v6-u-text-nowrap">
    <Link to={href}>{name}</Link>
    {detail ? <span>{` (${detail})`}</span> : null}
  </span>
);

export default NetworkingResourceName;
