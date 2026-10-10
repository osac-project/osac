import { Label } from '@patternfly/react-core';

import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

import { type CatalogFieldPolicyBehavior } from './catalogFieldPolicyDisplay';

interface CatalogFieldEditabilityLabelProps {
  behavior: CatalogFieldPolicyBehavior | undefined;
}

const CatalogFieldEditabilityLabel = ({ behavior }: CatalogFieldEditabilityLabelProps) => {
  const { t } = useTranslation();

  if (!behavior) {
    return null;
  }

  if (behavior === 'editable') {
    return (
      <Label color="purple" isCompact>
        {t('Editable')}
      </Label>
    );
  }

  return <Label isCompact>{t('Locked')}</Label>;
};

export default CatalogFieldEditabilityLabel;
