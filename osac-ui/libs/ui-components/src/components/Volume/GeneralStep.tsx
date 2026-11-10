import { Stack, StackItem, Title } from '@patternfly/react-core';

import { useTranslation } from '../../hooks/useTranslation';
import NameField from '../Form/NameField';
import OsacForm from '../Form/OsacForm';

const GeneralStep = () => {
  const { t } = useTranslation();

  return (
    <Stack hasGutter>
      <StackItem>
        <Title headingLevel="h2" size="lg">
          {t('General')}
        </Title>
      </StackItem>
      <StackItem>
        <OsacForm>
          <NameField />
        </OsacForm>
      </StackItem>
    </Stack>
  );
};

export default GeneralStep;
