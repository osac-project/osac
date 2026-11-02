import { Content } from '@patternfly/react-core';

import { useTranslation } from '../../../hooks/useTranslation';

interface CatalogFieldPolicyDescriptionProps {
  description: string;
}

const CatalogFieldPolicyDescription = ({ description }: CatalogFieldPolicyDescriptionProps) => {
  const { t } = useTranslation();

  return (
    <Content component="p">
      {description}{' '}
      {t(
        'Users can change editable fields, which may have a default value. Lock fields to enforce their configured values.',
      )}
    </Content>
  );
};

export default CatalogFieldPolicyDescription;
