import { useParams } from 'react-router-dom';
import { PageSection, Title } from '@patternfly/react-core';

import { useTranslation } from '@osac/ui-components/hooks/useTranslation';

export const VolumeWizardPage = () => {
  const { t } = useTranslation();
  const { id } = useParams<{ id: string }>();
  const isEdit = !!id;

  return (
    <PageSection hasBodyWrapper={false}>
      <Title headingLevel="h1" size="3xl">
        {isEdit ? t('Edit volume') : t('Create volume')}
      </Title>
    </PageSection>
  );
};
