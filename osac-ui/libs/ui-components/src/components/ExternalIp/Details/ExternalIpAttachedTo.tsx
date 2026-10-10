import { Link } from 'react-router-dom';
import { Content } from '@patternfly/react-core';

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

  return (
    <Content component="small">
      {kindLabel}{' '}
      <Link to={target.href} aria-label={`${kindLabel}: ${target.name}`}>
        {target.name}
      </Link>
    </Content>
  );
};

export default ExternalIpAttachedTo;
