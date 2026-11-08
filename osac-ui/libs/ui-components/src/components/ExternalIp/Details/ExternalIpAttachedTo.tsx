import { Link } from 'react-router-dom';

import type { ExternalIpAttachedTarget } from '../../../api/v1/external-ip-data';
import { useTranslation } from '../../../hooks/useTranslation';
import { attachedTargetKindLabel } from '../utils';

interface ExternalIpAttachedToProps {
  attached?: boolean;
  target?: ExternalIpAttachedTarget;
}

const ExternalIpAttachedTo = ({ attached, target }: ExternalIpAttachedToProps) => {
  const { t } = useTranslation();

  if (!attached || !target) {
    return null;
  }

  const kindLabel = attachedTargetKindLabel(t, target.kind);

  // One line: the name links to the resource, its kind follows in parentheses.
  return (
    <span className="pf-v6-u-text-nowrap">
      <Link to={target.href} aria-label={`${kindLabel}: ${target.name}`}>
        {target.name}
      </Link>
      <span>{` (${kindLabel})`}</span>
    </span>
  );
};

export default ExternalIpAttachedTo;
